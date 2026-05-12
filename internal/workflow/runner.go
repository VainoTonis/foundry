package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/review"
	"github.com/tonis2/foundry/internal/spec"
)

// Config holds runner configuration.
type Config struct {
	DefaultPhaseTimeoutSeconds int
	DefaultWorkflowBudgetUSD   float64
	MaxConcurrentWorkflows     int
}

// Runner owns the long-running workflow execution loop.
type Runner struct {
	pool     *pgxpool.Pool
	cerb     *cerberus.Client
	reviewer *review.Reviewer
	cfg      Config
}

func NewRunner(pool *pgxpool.Pool, cerb *cerberus.Client, rev *review.Reviewer, cfg Config) *Runner {
	return &Runner{pool: pool, cerb: cerb, reviewer: rev, cfg: cfg}
}

// Start launches the workflow for the given workflowID in a goroutine and returns immediately.
func (r *Runner) Start(workflowID int64) {
	go func() {
		ctx := context.Background()
		if err := r.run(ctx, workflowID); err != nil {
			log.Printf("workflow %d error: %v", workflowID, err)
		}
	}()
}

func (r *Runner) run(ctx context.Context, workflowID int64) error {
	wf, err := db.GetWorkflow(ctx, r.pool, workflowID)
	if err != nil {
		return fmt.Errorf("get workflow: %w", err)
	}
	sp, err := db.GetSpec(ctx, r.pool, wf.SpecID)
	if err != nil {
		return fmt.Errorf("get spec: %w", err)
	}
	proj, err := db.GetProject(ctx, r.pool, sp.ProjectID)
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}

	parsed := spec.Parse(sp.Content)
	// ensure phase rows exist
	for _, ph := range parsed.Phases {
		timeout := r.cfg.DefaultPhaseTimeoutSeconds
		if _, err := db.CreatePhase(ctx, r.pool, workflowID, ph.Position, ph.Name, ph.Goal, timeout); err != nil {
			// ignore duplicate — phase may already exist on resume
			log.Printf("createPhase pos=%d: %v", ph.Position, err)
		}
	}

	trackOverlay := spec.OverlayPoC
	if wf.Track == "polish" {
		trackOverlay = spec.OverlayPolish
	}

	for {
		// budget check
		if wf.MaxCostUSD != nil {
			total, err := db.WorkflowTotalCost(ctx, r.pool, workflowID)
			if err == nil && total >= *wf.MaxCostUSD {
				log.Printf("workflow %d budget exhausted (%.4f >= %.4f), pausing", workflowID, total, *wf.MaxCostUSD)
				_ = db.UpdateWorkflowStatus(ctx, r.pool, workflowID, "paused")
				return nil
			}
		}

		phase, err := db.NextPendingPhase(ctx, r.pool, workflowID)
		if err == db.ErrNotFound {
			// all phases done
			_ = db.UpdateWorkflowStatus(ctx, r.pool, workflowID, "done")
			specStatus := "done"
			_, _ = db.UpdateSpec(ctx, r.pool, sp.ID, db.UpdateSpecParams{Status: &specStatus})
			return nil
		}
		if err != nil {
			return fmt.Errorf("next phase: %w", err)
		}

		if err := r.runPhase(ctx, wf, sp, proj, phase, parsed.GlobalContext, trackOverlay); err != nil {
			log.Printf("phase %d failed: %v", phase.ID, err)
			_ = db.UpdateWorkflowStatus(ctx, r.pool, workflowID, "paused")
			return nil
		}
	}
}

func (r *Runner) runPhase(
	ctx context.Context,
	wf db.Workflow,
	sp db.Spec,
	proj db.Project,
	phase db.Phase,
	globalCtx, trackOverlay string,
) error {
	prompt := spec.BuildPrompt(globalCtx, phase.Goal, trackOverlay)
	if phase.AdjustedPrompt != nil && *phase.AdjustedPrompt != "" {
		prompt = *phase.AdjustedPrompt
	}
	return r.execPhase(ctx, wf, sp, proj, phase, prompt, false)
}

func (r *Runner) execPhase(
	ctx context.Context,
	wf db.Workflow,
	sp db.Spec,
	proj db.Project,
	phase db.Phase,
	prompt string,
	isRetry bool,
) error {
	sessionName := cerberus.SessionName(fmt.Sprintf("%d", sp.ID)[:min(8, len(fmt.Sprintf("%d", sp.ID)))], phase.Position)

	now := time.Now()
	status := "running"
	_, err := db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{
		Status:          &status,
		PromptSent:      &prompt,
		CerberusSession: &sessionName,
		StartedAt:       &now,
	})
	if err != nil {
		return fmt.Errorf("update phase running: %w", err)
	}

	// start cerberus with timeout
	phaseCtx, cancel := context.WithTimeout(ctx, time.Duration(phase.TimeoutSeconds)*time.Second)
	defer cancel()

	// run cerberus blocking in goroutine, collect logs in parallel
	cerberusDone := make(chan error, 1)
	go func() {
		cerberusDone <- r.cerb.Start(phaseCtx, sessionName, prompt)
	}()

	// poll logs every 2s
	logTicker := time.NewTicker(2 * time.Second)
	defer logTicker.Stop()

	var lastLogLine string
loop:
	for {
		select {
		case <-logTicker.C:
			r.collectLogs(ctx, phase.ID, sessionName, &lastLogLine)
		case cerberusErr := <-cerberusDone:
			r.collectLogs(ctx, phase.ID, sessionName, &lastLogLine)
			if cerberusErr != nil {
				failStatus := "failed"
				now2 := time.Now()
				_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{
					Status:     &failStatus,
					FinishedAt: &now2,
				})
				return fmt.Errorf("cerberus: %w", cerberusErr)
			}
			break loop
		}
	}

	// awaiting review
	awaitStatus := "awaiting_review"
	_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{Status: &awaitStatus})

	// get diff
	diff, err := r.cerb.Diff(ctx, sessionName)
	if err != nil {
		diff = "(diff unavailable: " + err.Error() + ")"
	}

	// run reviewer
	res, reviewErr := r.reviewer.Review(ctx, phase.Goal, diff, wf.Track, "")
	if reviewErr != nil {
		log.Printf("review error phase %d: %v — treating as fail", phase.ID, reviewErr)
		res = review.Result{Verdict: "fail", Notes: reviewErr.Error()}
	}

	filesJSON, _ := json.Marshal(res.FilesTouched)
	now3 := time.Now()
	verdict := res.Verdict
	_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{
		ReviewVerdict:     &verdict,
		ReviewNotes:       &res.Notes,
		DecisionSummary:   &res.DecisionSummary,
		DecisionRationale: &res.Rationale,
		FilesTouched:      filesJSON,
		FinishedAt:        &now3,
	})

	if res.Verdict == "pass" {
		// cherry-pick commit to base branch
		commit, err := r.cerb.Commit(ctx, sessionName)
		if err == nil && commit != "" {
			if err := cherryPick(proj.RepoPath, commit); err != nil {
				log.Printf("cherry-pick %s: %v", commit, err)
			} else {
				_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{CerberusCommit: &commit})
			}
		}
		_ = r.cerb.Clean(ctx, sessionName)
		doneStatus := "done"
		_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{Status: &doneStatus})
		return nil
	}

	// fail path
	if isRetry || phase.RetryCount >= 1 {
		failStatus := "failed"
		_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{Status: &failStatus})
		return fmt.Errorf("phase %d failed after retry", phase.ID)
	}

	// retry once with adjusted prompt
	adjusted := res.AdjustedPrompt
	if adjusted == "" {
		adjusted = prompt + "\n\n[Previous attempt failed. Reviewer notes: " + res.Notes + "]"
	}
	newRetry := phase.RetryCount + 1
	_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{
		AdjustedPrompt: &adjusted,
		RetryCount:     &newRetry,
		Status:         strPtr("pending"),
	})

	// reload phase for retry
	phase2, err := db.GetPhase(ctx, r.pool, phase.ID)
	if err != nil {
		return fmt.Errorf("reload phase for retry: %w", err)
	}
	return r.execPhase(ctx, wf, sp, proj, phase2, adjusted, true)
}

func (r *Runner) collectLogs(ctx context.Context, phaseID int64, session string, lastLine *string) {
	logs, err := r.cerb.Logs(ctx, session)
	if err != nil {
		return
	}
	lines := strings.Split(logs, "\n")
	writing := *lastLine == ""
	for _, line := range lines {
		if !writing {
			if line == *lastLine {
				writing = true
			}
			continue
		}
		if line == "" {
			continue
		}
		_ = db.InsertPhaseLog(ctx, r.pool, phaseID, line)
		*lastLine = line
	}
}

func cherryPick(repoPath, commit string) error {
	cmd := exec.Command("git", "-C", repoPath, "cherry-pick", commit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func strPtr(s string) *string { return &s }
