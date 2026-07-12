package workflow

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/hub"
	"github.com/tonis2/foundry/internal/spec"
)

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
	existing, _ := db.ListPhasesByWorkflow(ctx, r.pool, workflowID)
	if len(existing) == 0 {
		plan, planErr := db.GetPlanByWorkflow(ctx, r.pool, workflowID)
		steps, stepsErr := db.ListPlanSteps(ctx, r.pool, plan.ID)
		if planErr == nil && stepsErr == nil && len(steps) > 0 {
			for _, step := range steps {
				if _, err := db.CreatePhaseWithParallelGroup(ctx, r.pool, workflowID, step.Position, fmt.Sprintf("Step %d", step.Position), step.Text, r.cfg.DefaultPhaseTimeoutSeconds, step.ParallelGroup); err != nil {
					return fmt.Errorf("create phase at position %d: %w", step.Position, err)
				}
			}
		} else {
			for _, ph := range parsed.Phases {
				if _, err := db.CreatePhase(ctx, r.pool, workflowID, ph.Position, ph.Name, ph.Goal, r.cfg.DefaultPhaseTimeoutSeconds); err != nil {
					return fmt.Errorf("create phase at position %d: %w", ph.Position, err)
				}
			}
		}
	}
	phases, err := db.ListPhasesByWorkflow(ctx, r.pool, workflowID)
	if err != nil {
		return fmt.Errorf("list phases: %w", err)
	}
	if len(phases) == 0 {
		log.Printf("workflow %d: plan has no executable steps or phases, pausing", workflowID)
		_ = db.UpdateWorkflowStatus(ctx, r.pool, workflowID, "paused")
		r.publishWorkflowUpdate(workflowID, "paused")
		failStatus := "paused"
		_, _ = db.UpdateSpec(ctx, r.pool, sp.ID, db.UpdateSpecParams{Status: &failStatus})
		return fmt.Errorf("workflow %d has no executable steps", workflowID)
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

		var runErr error
		if phase.ParallelGroup == nil {
			runErr = r.runPhase(ctx, wf, proj, phase, parsed.GlobalContext, trackOverlay, nil)
		} else {
			phases, listErr := db.ListPhasesByWorkflow(ctx, r.pool, workflowID)
			if listErr != nil {
				return fmt.Errorf("list parallel phases: %w", listErr)
			}
			runErr = r.runParallelGroup(ctx, wf, proj, pendingParallelBatch(phases, *phase.ParallelGroup), parsed.GlobalContext, trackOverlay)
		}
		if runErr != nil {
			log.Printf("phase/group starting at %d failed: %v", phase.ID, runErr)
			r.finishWorkflow(workflowID, "failed")
			specStatus := "failed"
			_, _ = db.UpdateSpec(context.Background(), r.pool, sp.ID, db.UpdateSpecParams{Status: &specStatus})
			return nil
		}
	}
}

func pendingParallelBatch(phases []db.Phase, group int) []db.Phase {
	batch := make([]db.Phase, 0)
	for _, phase := range phases {
		if phase.Status == "pending" && phase.ParallelGroup != nil && *phase.ParallelGroup == group {
			batch = append(batch, phase)
		}
	}
	return batch
}

func (r *Runner) runParallelGroup(ctx context.Context, wf db.Workflow, proj db.Project, phases []db.Phase, globalContext, trackOverlay string) error {
	if len(phases) == 0 {
		return nil
	}
	// Agents execute concurrently in isolated Cerberus worktrees. Application is
	// gated by zero-based position so commits reach the target repository in a
	// deterministic order; a conflict fails the group and pauses progression.
	gates := make([]chan struct{}, len(phases)+1)
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	close(gates[0])

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, phase := range phases {
		i, phase := i, phase
		wg.Add(1)
		go func() {
			defer wg.Done()
			beforeApply := func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-gates[i]:
				}
				mu.Lock()
				defer mu.Unlock()
				if firstErr != nil {
					return fmt.Errorf("parallel group blocked by earlier phase: %w", firstErr)
				}
				return nil
			}
			err := r.runPhase(ctx, wf, proj, phase, globalContext, trackOverlay, beforeApply)
			// Even a phase that fails before integration must not release the next
			// position until all earlier positions have finished.
			<-gates[i]
			mu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
			close(gates[i+1])
		}()
	}
	wg.Wait()
	return firstErr
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
