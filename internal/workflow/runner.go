package workflow

import (
	"context"
	"fmt"
	"log"
	"strings"

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
