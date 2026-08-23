package db

import (
	"context"
	"testing"
)

// TestAddPhaseCost_AccumulatesAdditively_Postgres exercises AddPhaseCost's
// COALESCE-based additive update against real PostgreSQL: applying 0.01
// then 0.02 to the same phase must leave cost_usd at 0.03, and
// WorkflowTotalCost must reflect the sum across every phase of the
// workflow.
func TestAddPhaseCost_AccumulatesAdditively_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	repo := createTestPlanRepo(t, pool, "phase-cost")

	spec, err := CreateSpec(ctx, pool, repo.ID, "spec for phase cost", "content", []byte(`[]`))
	if err != nil {
		t.Fatalf("CreateSpec() error = %v", err)
	}
	t.Cleanup(func() { _ = DeleteSpec(context.Background(), pool, spec.ID) })

	wf, err := CreateWorkflow(ctx, pool, spec.ID, "poc", nil)
	if err != nil {
		t.Fatalf("CreateWorkflow() error = %v", err)
	}
	t.Cleanup(func() { _ = DeleteWorkflow(context.Background(), pool, wf.ID) })

	phaseOne, err := CreatePhase(ctx, pool, wf.ID, 0, "phase-one", "goal-one", 60)
	if err != nil {
		t.Fatalf("CreatePhase() error = %v", err)
	}
	phaseTwo, err := CreatePhase(ctx, pool, wf.ID, 1, "phase-two", "goal-two", 60)
	if err != nil {
		t.Fatalf("CreatePhase() error = %v", err)
	}

	if err := AddPhaseCost(ctx, pool, phaseOne.ID, 0.01); err != nil {
		t.Fatalf("AddPhaseCost() error = %v", err)
	}
	if err := AddPhaseCost(ctx, pool, phaseOne.ID, 0.02); err != nil {
		t.Fatalf("AddPhaseCost() error = %v", err)
	}

	got, err := GetPhase(ctx, pool, phaseOne.ID)
	if err != nil {
		t.Fatalf("GetPhase() error = %v", err)
	}
	if got.CostUSD == nil || *got.CostUSD != 0.03 {
		t.Fatalf("GetPhase().CostUSD = %v, want pointer to 0.03", got.CostUSD)
	}

	if err := AddPhaseCost(ctx, pool, phaseTwo.ID, 0.5); err != nil {
		t.Fatalf("AddPhaseCost() error = %v", err)
	}

	total, err := WorkflowTotalCost(ctx, pool, wf.ID)
	if err != nil {
		t.Fatalf("WorkflowTotalCost() error = %v", err)
	}
	if total != 0.53 {
		t.Fatalf("WorkflowTotalCost() = %v, want 0.53", total)
	}
}

// TestAddPhaseCost_UnknownPhase_Postgres exercises AddPhaseCost's
// not-found mapping when the phase id does not exist.
func TestAddPhaseCost_UnknownPhase_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	const neverIssuedPhaseID int64 = 1 << 40

	if err := AddPhaseCost(ctx, pool, neverIssuedPhaseID, 0.01); err != ErrNotFound {
		t.Fatalf("AddPhaseCost() error = %v, want ErrNotFound", err)
	}
}
