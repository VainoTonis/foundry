package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/hub"
	"github.com/tonis2/foundry/internal/spec"
)

type Config struct {
	DefaultPhaseTimeoutSeconds int
	DefaultWorkflowBudgetUSD   float64
	MaxConcurrentWorkflows     int
	CerberusProfile            string
	CerberusCallbackURL        string
}

type Runner struct {
	pool    *pgxpool.Pool
	cerb    *cerberus.Client
	cfg     Config
	hub     *hub.EventHub
	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
}

func NewRunner(pool *pgxpool.Pool, cerb *cerberus.Client, cfg Config, eventHub *hub.EventHub) *Runner {
	return &Runner{
		pool:    pool,
		cerb:    cerb,
		cfg:     cfg,
		hub:     eventHub,
		cancels: make(map[int64]context.CancelFunc),
	}
}

func (r *Runner) SetCerberusProfile(profile string) {
	r.mu.Lock()
	r.cfg.CerberusProfile = strings.TrimSpace(profile)
	r.mu.Unlock()
}

func (r *Runner) cerberusProfile() string {
	r.mu.Lock()
	profile := r.cfg.CerberusProfile
	r.mu.Unlock()
	return profile
}

func (r *Runner) Stop(workflowID int64) {
	r.mu.Lock()
	cancel, ok := r.cancels[workflowID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
}

func (r *Runner) Start(workflowID int64) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancels[workflowID] = cancel
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.cancels, workflowID)
			r.mu.Unlock()
			cancel()
		}()
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
	if len(parsed.Phases) == 0 {
		log.Printf("workflow %d: spec has no phases, pausing", workflowID)
		_ = db.UpdateWorkflowStatus(ctx, r.pool, workflowID, "paused")
		r.publishWorkflowUpdate(workflowID, "paused")
		failStatus := "paused"
		_, _ = db.UpdateSpec(ctx, r.pool, sp.ID, db.UpdateSpecParams{Status: &failStatus})
		return fmt.Errorf("spec %d has no ## Phase N: sections", sp.ID)
	}
	existing, _ := db.ListPhasesByWorkflow(ctx, r.pool, workflowID)
	if len(existing) == 0 {
		for _, ph := range parsed.Phases {
			timeout := r.cfg.DefaultPhaseTimeoutSeconds
			if _, err := db.CreatePhase(ctx, r.pool, workflowID, ph.Position, ph.Name, ph.Goal, timeout); err != nil {
				log.Printf("createPhase pos=%d: %v", ph.Position, err)
			}
		}
	}

	trackOverlay := spec.OverlayPoC
	if wf.Track == "polish" {
		trackOverlay = spec.OverlayPolish
	}

	for {
		if ctx.Err() != nil {
			r.finishWorkflow(workflowID, "paused")
			return ctx.Err()
		}

		if wf.MaxCostUSD != nil {
			total, err := db.WorkflowTotalCost(ctx, r.pool, workflowID)
			if err == nil && total >= *wf.MaxCostUSD {
				log.Printf("workflow %d budget exhausted (%.4f >= %.4f), pausing", workflowID, total, *wf.MaxCostUSD)
				r.finishWorkflow(workflowID, "paused")
				return nil
			}
		}

		phase, err := db.NextPendingPhase(ctx, r.pool, workflowID)
		if err == db.ErrNotFound {
			status, err := r.workflowStatusFromPhases(ctx, workflowID)
			if err != nil {
				return fmt.Errorf("list phases: %w", err)
			}
			r.finishWorkflow(workflowID, status)
			if status == "done" || status == "failed" {
				specStatus := status
				_, _ = db.UpdateSpec(context.Background(), r.pool, sp.ID, db.UpdateSpecParams{Status: &specStatus})
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("next phase: %w", err)
		}

		if err := r.runPhase(ctx, wf, proj, phase, parsed.GlobalContext, trackOverlay); err != nil {
			log.Printf("phase %d failed: %v", phase.ID, err)
			r.finishWorkflow(workflowID, "failed")
			specStatus := "failed"
			_, _ = db.UpdateSpec(context.Background(), r.pool, sp.ID, db.UpdateSpecParams{Status: &specStatus})
			return nil
		}
	}
}

func (r *Runner) runPhase(
	ctx context.Context,
	wf db.Workflow,
	proj db.Project,
	phase db.Phase,
	globalCtx, trackOverlay string,
) error {
	prompt := spec.BuildPrompt(globalCtx, phase.Goal, trackOverlay)
	if phase.AdjustedPrompt != nil && *phase.AdjustedPrompt != "" {
		prompt = *phase.AdjustedPrompt
	}
	prompt = prependRepoRootContext(proj.RepoPath, prompt)
	return r.execPhase(ctx, wf, proj, phase, prompt, false)
}

func (r *Runner) execPhase(
	ctx context.Context,
	wf db.Workflow,
	proj db.Project,
	phase db.Phase,
	prompt string,
	isRetry bool,
) error {
	sessionName := cerberus.SessionName(wf.ID, phase.ID)

	r.cerb.SetRepoPath(proj.RepoPath)

	if err := r.cerb.Clean(ctx, sessionName); err != nil {
		log.Printf("pre-clean session %s: %v (ignored)", sessionName, err)
		// cerberus state file may be gone but git branch/worktree can linger after a crash.
		// Force-remove worktree then branch so the next Start can create them fresh.
		_ = exec.CommandContext(ctx, "git", "-C", proj.RepoPath, "worktree", "remove", "--force",
			".cerberus/sessions/"+sessionName+"/worktrees/solve").Run()
		_ = exec.CommandContext(ctx, "git", "-C", proj.RepoPath, "worktree", "prune").Run()
		_ = exec.CommandContext(ctx, "git", "-C", proj.RepoPath, "branch", "-D", "cerberus/"+sessionName).Run()
	}

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
	r.publishPhaseUpdate(wf.ID, phase.ID, "running")

	phaseCtx, cancel := context.WithTimeout(ctx, time.Duration(phase.TimeoutSeconds)*time.Second)
	defer cancel()

	profilePath, err := r.writeProfileFile(ctx, r.cerberusProfile(), sessionName)
	if err != nil {
		log.Printf("phase %d: write profile file: %v (proceeding without profile)", phase.ID, err)
	}
	if profilePath != "" {
		r.cerb.SetProfile(profilePath)
	}

	cerberusDone := make(chan error, 1)
	callbackURL := r.cfg.CerberusCallbackURL
	go func() {
		cerberusDone <- r.cerb.Start(phaseCtx, sessionName, prompt, callbackURL)
	}()

	var logTicker *time.Ticker
	if callbackURL == "" {
		// Legacy fallback: only poll full logs when callback delivery is unavailable.
		logTicker = time.NewTicker(2 * time.Second)
		defer logTicker.Stop()
	}

	var lastLogLine string
loop:
	for {
		select {
		case <-tickerC(logTicker):
			r.collectLogs(ctx, wf.ID, phase.ID, sessionName, &lastLogLine)
		case cerberusErr := <-cerberusDone:
			if callbackURL == "" {
				r.collectLogs(ctx, wf.ID, phase.ID, sessionName, &lastLogLine)
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

	diff, err := r.cerb.Diff(ctx, sessionName)
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

	reviewOut, _ := r.cerb.Review(ctx, sessionName)
	filesJSON := extractFilesJSON(reviewOut)

	// get full commit hash from the cerberus branch directly
	commitHash := ""
	if hashOut, err := exec.CommandContext(ctx, "git", "-C", proj.RepoPath, "rev-parse", "cerberus/"+sessionName).Output(); err == nil {
		commitHash = strings.TrimSpace(string(hashOut))
	}

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
			cmd := exec.CommandContext(ctx, "git", "-C", proj.RepoPath, "cherry-pick", commitHash)
			if out, err := cmd.CombinedOutput(); err != nil {
				// abort any partial cherry-pick so repo stays clean
				_ = exec.CommandContext(ctx, "git", "-C", proj.RepoPath, "cherry-pick", "--abort").Run()
				failStatus := "failed"
				failVerdict := "fail"
				cherryErr := fmt.Sprintf("cherry-pick %s failed: %v — %s", commitHash, err, strings.TrimSpace(string(out)))
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
	return r.execPhase(ctx, wf, proj, phase2, adjusted, true)
}

func (r *Runner) collectLogs(ctx context.Context, workflowID, phaseID int64, session string, lastLine *string) {
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
		r.publishLog(workflowID, phaseID, line)
	}
}

func (r *Runner) workflowStatusFromPhases(ctx context.Context, workflowID int64) (string, error) {
	phases, err := db.ListPhasesByWorkflow(ctx, r.pool, workflowID)
	if err != nil {
		return "", err
	}
	for _, ph := range phases {
		if ph.Status == "failed" {
			return "failed", nil
		}
	}
	// Prefer running over review waits: a workflow may have an active phase
	// while earlier phases are still awaiting review.
	for _, ph := range phases {
		if ph.Status == "running" {
			return "running", nil
		}
	}
	for _, ph := range phases {
		if ph.Status == "awaiting_review" {
			return "paused", nil
		}
	}
	return "done", nil
}

func (r *Runner) finishWorkflow(workflowID int64, status string) {
	_ = db.UpdateWorkflowStatus(context.Background(), r.pool, workflowID, status)
	r.publishWorkflowUpdate(workflowID, status)
}

func (r *Runner) publishLog(workflowID, phaseID int64, line string) {
	if r.hub == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"event":    "log",
		"phase_id": phaseID,
		"line":     line,
		"ts":       time.Now().Format(time.RFC3339),
	})
	r.hub.Publish(fmt.Sprintf("wf:%d", workflowID), data)
}

func (r *Runner) publishPhaseUpdate(workflowID, phaseID int64, status string) {
	if r.hub == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"event":       "phase_update",
		"workflow_id": workflowID,
		"phase_id":    phaseID,
		"status":      status,
		"ts":          time.Now().Format(time.RFC3339),
	})
	r.hub.Publish(fmt.Sprintf("wf:%d", workflowID), data)
}

func (r *Runner) publishWorkflowUpdate(workflowID int64, status string) {
	if r.hub == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"event":       "workflow_update",
		"workflow_id": workflowID,
		"status":      status,
		"ts":          time.Now().Format(time.RFC3339),
	})
	r.hub.Publish(fmt.Sprintf("wf:%d", workflowID), data)
}

func strPtr(s string) *string { return &s }

func extractTags(goal string) []string {
	goal = strings.ToLower(goal)
	goal = strings.NewReplacer(
		",", " ",
		".", " ",
		";", " ",
		":", " ",
		"(", " ",
		")", " ",
		"!", " ",
	).Replace(goal)

	seen := make(map[string]bool)
	var tags []string
	for _, word := range strings.Fields(goal) {
		if seen[word] {
			continue
		}
		seen[word] = true
		tags = append(tags, word)
	}
	return tags
}

const repoRootPromptHeader = `## Target Repository Root

You are running in the configured target repository root: %s
Treat the current working directory as the workspace root. All file paths in the spec are relative to this root. If the spec mentions the repository directory name, do not create a nested copy of that directory; modify files in this root.

---

`

func prependRepoRootContext(repoPath, prompt string) string {
	if strings.HasPrefix(prompt, "## Target Repository Root\n") {
		return prompt
	}
	return fmt.Sprintf(repoRootPromptHeader, repoPath) + prompt
}

func tickerC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func profileFilePath(session string) string {
	return "/tmp/foundry-profile-" + session + ".json"
}

func removeProfileFile(session string) {
	os.Remove(profileFilePath(session))
}

func (r *Runner) writeProfileFile(ctx context.Context, profileName, session string) (string, error) {
	if profileName == "" {
		return "", nil
	}
	p, err := db.GetProfileByName(ctx, r.pool, profileName)
	if err == db.ErrNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup profile %q: %w", profileName, err)
	}
	payload := map[string]any{}
	if p.DefaultModel != "" {
		payload["default_model"] = p.DefaultModel
	}
	if p.DefaultImage != "" {
		payload["default_image"] = p.DefaultImage
	}
	if p.AWSProfile != "" {
		payload["aws_profile"] = p.AWSProfile
	}
	if p.AWSRegion != "" {
		payload["aws_region"] = p.AWSRegion
	}
	if len(p.ExtraEnv) > 0 {
		payload["extra_env"] = p.ExtraEnv
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal profile: %w", err)
	}
	path := profileFilePath(session)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write profile file: %w", err)
	}
	return path, nil
}

type phaseFeedbackPayload struct {
	Result        string   `json:"result"`
	UsefulContext []string `json:"useful_context"`
	Problems      []string `json:"problems"`
	Confidence    float64  `json:"confidence"`
}

func buildPhaseFeedback(verdict, notes string, filesJSON []byte, commitHash string) []byte {
	feedback := phaseFeedbackPayload{Result: strings.TrimSpace(verdict), Confidence: 0.6}
	if feedback.Result == "pass" {
		feedback.Confidence = 0.85
	} else if feedback.Result == "fail" {
		feedback.Confidence = 0.4
	}
	var files []string
	if len(filesJSON) > 0 {
		_ = json.Unmarshal(filesJSON, &files)
	}
	for _, f := range files {
		if f = strings.TrimSpace(f); f != "" {
			feedback.UsefulContext = append(feedback.UsefulContext, "touched "+f)
		}
	}
	if commitHash = strings.TrimSpace(commitHash); commitHash != "" {
		feedback.UsefulContext = append(feedback.UsefulContext, "commit "+commitHash)
	}
	if notes = strings.TrimSpace(notes); notes != "" {
		if feedback.Result == "fail" {
			feedback.Problems = append(feedback.Problems, notes)
		} else {
			feedback.UsefulContext = append(feedback.UsefulContext, notes)
		}
	}
	b, err := json.Marshal(feedback)
	if err != nil {
		return []byte(`{"result":"` + verdict + `","useful_context":[],"problems":["phase feedback marshal failed"],"confidence":0}`)
	}
	return b
}

func extractFilesJSON(reviewOut string) []byte {
	var files []string
	for _, line := range strings.Split(reviewOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "commit") || strings.HasPrefix(line, "status") {
			continue
		}
		files = append(files, line)
	}
	if len(files) == 0 {
		return []byte("[]")
	}
	b := []byte(`["`)
	b = append(b, []byte(strings.Join(files, `","`))...)
	b = append(b, []byte(`"]`)...)
	return b
}
