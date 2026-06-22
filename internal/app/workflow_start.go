package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/db"
)

type WorkflowStartUseCase struct {
	pool   *pgxpool.Pool
	runner interface{ Start(int64) }
}

type WorkflowStartRequest struct {
	SpecID     int64
	MaxCostUSD *float64
}

type WorkflowStartResponse struct {
	WorkflowID int64
}

func NewWorkflowStartUseCase(pool *pgxpool.Pool, runner interface{ Start(int64) }) *WorkflowStartUseCase {
	return &WorkflowStartUseCase{
		pool:   pool,
		runner: runner,
	}
}

func (uc *WorkflowStartUseCase) Execute(ctx context.Context, req WorkflowStartRequest) (WorkflowStartResponse, error) {
	// Get spec
	sp, err := db.GetSpec(ctx, uc.pool, req.SpecID)
	if errors.Is(err, db.ErrNotFound) {
		return WorkflowStartResponse{}, fmt.Errorf("spec not found")
	}
	if err != nil {
		return WorkflowStartResponse{}, fmt.Errorf("get spec: %w", err)
	}

	// Create workflow
	wf, err := db.CreateWorkflow(ctx, uc.pool, sp.ID, sp.Track, req.MaxCostUSD)
	if err != nil {
		return WorkflowStartResponse{}, fmt.Errorf("create workflow: %w", err)
	}

	// Update spec status to running
	runStatus := "running"
	_, _ = db.UpdateSpec(ctx, uc.pool, sp.ID, db.UpdateSpecParams{Status: &runStatus})

	// Start workflow runner
	uc.runner.Start(wf.ID)

	return WorkflowStartResponse{WorkflowID: wf.ID}, nil
}
