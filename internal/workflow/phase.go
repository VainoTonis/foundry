package workflow

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

func (r *Runner) runPhase(
	ctx context.Context,
	wf db.Workflow,
	proj db.Project,
	phase db.Phase,
	globalCtx, trackOverlay string,
	beforeApply func() error,
) error {
	prompt := buildPhasePrompt(proj.RepoPath, globalCtx, phase.Goal, trackOverlay, phase.AdjustedPrompt)
	return r.execPhase(ctx, wf, proj, phase, prompt, false, beforeApply)
}

func (r *Runner) execPhase(
	ctx context.Context,
	wf db.Workflow,
	proj db.Project,
	phase db.Phase,
	prompt string,
	isRetry bool,
	beforeApply func() error,
) error {
	sessionName := phaseSessionName(wf.ID, phase.ID)

	profilePath, err := r.writeProfileFile(ctx, r.cerberusProfile(), sessionName)
	if err != nil {
		log.Printf("phase %d: write profile file: %v (proceeding without profile)", phase.ID, err)
	}
	cerb := r.cerb.WithRepoProfile(proj.RepoPath, profilePath)

	if err := cerb.Clean(ctx, sessionName); err != nil {
		log.Printf("pre-clean session %s: %v (ignored)", sessionName, err)
		cleanupCerberusGitState(ctx, proj.RepoPath, sessionName)
	}

	now := time.Now()
	status := "running"
	_, err = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{
		Status:          &status,
		PromptSent:      &prompt,
		CerberusSession: &sessionName,
		StartedAt:       &now,
	})
	if err != nil {
		return fmt.Errorf("update phase running: %w", err)
	}
	r.publishPhaseUpdate(wf.ID, phase.ID, "running")

	phaseCtx, cancel := context.WithTimeout(ctx, time.Duration(phase.TimeoutSeconds)*time.Second)
	defer cancel()

	cerberusDone := make(chan error, 1)
	callbackURL := r.cfg.CerberusCallbackURL
	go func() {
		cerberusDone <- cerb.Start(phaseCtx, sessionName, prompt, callbackURL)
	}()

	var logTicker *time.Ticker
	if callbackURL == "" {
		logTicker = time.NewTicker(2 * time.Second)
		defer logTicker.Stop()
	}

	var lastLogLine string
loop:
	for {
		select {
		case <-tickerC(logTicker):
			r.collectLogs(ctx, cerb, wf.ID, phase.ID, sessionName, &lastLogLine)
		case cerberusErr := <-cerberusDone:
			if callbackURL == "" {
				r.collectLogs(ctx, cerb, wf.ID, phase.ID, sessionName, &lastLogLine)
			}
			if cerberusErr != nil {
				failStatus := "failed"
				failVerdict := "fail"
				now2 := time.Now()
				notes := fmt.Sprintf("cerberus start failed:\n%v", cerberusErr)
				phaseFeedback := buildPhaseFeedback(failVerdict, notes, []byte("[]"), "")
				_, _ = db.UpdatePhase(context.Background(), r.pool, phase.ID, db.UpdatePhaseParams{
					Status:        &failStatus,
					FinishedAt:    &now2,
					ReviewVerdict: &failVerdict,
					ReviewNotes:   &notes,
					PhaseFeedback: phaseFeedback,
				})
				r.publishPhaseUpdate(wf.ID, phase.ID, "failed")
				return fmt.Errorf("cerberus: %w", cerberusErr)
			}
			break loop
		}
	}

	if ctx.Err() != nil {
		failStatus := "failed"
		failVerdict := "fail"
		now2 := time.Now()
		notes := ctx.Err().Error()
		phaseFeedback := buildPhaseFeedback(failVerdict, notes, []byte("[]"), "")
		_, _ = db.UpdatePhase(context.Background(), r.pool, phase.ID, db.UpdatePhaseParams{
			Status:        &failStatus,
			FinishedAt:    &now2,
			ReviewVerdict: &failVerdict,
			ReviewNotes:   &notes,
			PhaseFeedback: phaseFeedback,
		})
		r.publishPhaseUpdate(wf.ID, phase.ID, "failed")
		return ctx.Err()
	}

	awaitStatus := "awaiting_review"
	_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{Status: &awaitStatus})
	r.publishPhaseUpdate(wf.ID, phase.ID, "awaiting_review")

	diff, err := cerb.Diff(ctx, sessionName)
	if err != nil {
		diff = ""
	}

	now3 := time.Now()
	verdict := "pass"
	notes := "cerberus produced changes"
	if strings.TrimSpace(diff) == "" {
		verdict = "fail"
		notes = "cerberus exited 0 but produced no diff"
	}

	reviewOut, _ := cerb.Review(ctx, sessionName)
	filesJSON := extractFilesJSON(reviewOut)
	commitHash := cerberusCommitHash(ctx, proj.RepoPath, sessionName)

	phaseFeedback := buildPhaseFeedback(verdict, notes, filesJSON, commitHash)
	_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{
		ReviewVerdict:  &verdict,
		ReviewNotes:    &notes,
		FilesTouched:   filesJSON,
		PhaseFeedback:  phaseFeedback,
		CerberusCommit: &commitHash,
		FinishedAt:     &now3,
	})

	if verdict == "pass" {
		if commitHash == "" {
			verdict = "fail"
			notes = "cerberus produced diff but no commit hash found"
			phaseFeedback = buildPhaseFeedback(verdict, notes, filesJSON, commitHash)
			_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{
				ReviewVerdict: &verdict,
				ReviewNotes:   &notes,
				PhaseFeedback: phaseFeedback,
			})
		} else {
			if beforeApply != nil {
				if err := beforeApply(); err != nil {
					failed := "failed"
					failVerdict := "fail"
					notes := err.Error()
					feedback := buildPhaseFeedback(failVerdict, notes, filesJSON, commitHash)
					_, _ = db.UpdatePhase(context.Background(), r.pool, phase.ID, db.UpdatePhaseParams{
						Status: &failed, ReviewVerdict: &failVerdict, ReviewNotes: &notes, PhaseFeedback: feedback,
					})
					r.publishPhaseUpdate(wf.ID, phase.ID, failed)
					return err
				}
			}
			cmd := exec.CommandContext(ctx, "git", "-C", proj.RepoPath, "cherry-pick", commitHash)
			if out, err := cmd.CombinedOutput(); err != nil {
				_ = exec.CommandContext(ctx, "git", "-C", proj.RepoPath, "cherry-pick", "--abort").Run()
				failStatus := "failed"
				failVerdict := "fail"
				cherryErr := fmt.Sprintf("cherry-pick %s failed: %v - %s", commitHash, err, strings.TrimSpace(string(out)))
				phaseFeedback = buildPhaseFeedback(failVerdict, cherryErr, filesJSON, commitHash)
				_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{
					Status:        &failStatus,
					ReviewVerdict: &failVerdict,
					ReviewNotes:   &cherryErr,
					PhaseFeedback: phaseFeedback,
				})
				r.publishPhaseUpdate(wf.ID, phase.ID, "failed")
				return fmt.Errorf("phase %d cherry-pick: %w", phase.ID, err)
			}
			doneStatus := "done"
			_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{Status: &doneStatus})
			r.publishPhaseUpdate(wf.ID, phase.ID, "done")
			return nil
		}
	}

	if isRetry || phase.RetryCount >= 1 {
		failStatus := "failed"
		_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{Status: &failStatus})
		r.publishPhaseUpdate(wf.ID, phase.ID, "failed")
		return fmt.Errorf("phase %d failed after retry", phase.ID)
	}

	adjusted := prompt + "\n\n[Previous attempt produced no changes. Try again.]"
	newRetry := phase.RetryCount + 1
	_, _ = db.UpdatePhase(ctx, r.pool, phase.ID, db.UpdatePhaseParams{
		AdjustedPrompt: &adjusted,
		RetryCount:     &newRetry,
		Status:         strPtr("pending"),
	})

	phase2, err := db.GetPhase(ctx, r.pool, phase.ID)
	if err != nil {
		return fmt.Errorf("reload phase for retry: %w", err)
	}
	return r.execPhase(ctx, wf, proj, phase2, adjusted, true, beforeApply)
}

func (r *Runner) collectLogs(ctx context.Context, cerb interface {
	Logs(context.Context, string) (string, error)
}, workflowID, phaseID int64, session string, lastLine *string) {
	logs, err := cerb.Logs(ctx, session)
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
		r.publishLog(workflowID, phaseID, line)
	}
}

func tickerC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func phaseSessionName(workflowID, phaseID int64) string {
	return fmt.Sprintf("foundry-w%d-p%d", workflowID, phaseID)
}

func strPtr(s string) *string { return &s }
