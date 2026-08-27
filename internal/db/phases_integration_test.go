package db

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// defaultPhaseFeedback is the literal jsonb default that ClearPhaseFeedback
// resets phase_feedback to (see UpdatePhase in phases.go). phase_feedback is
// a NOT NULL column, so clearing it yields this value rather than NULL.
var defaultPhaseFeedback = []byte(`{"result":"","useful_context":[],"problems":[],"confidence":0}`)

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

// TestPhaseRetryCycle_Postgres exercises a full retry cycle against real
// PostgreSQL, matching the actual write path in
// internal/workflow/phase.go:
//
//  1. The phase starts (status=running, started_at set).
//  2. It moves to awaiting_review (status only).
//  3. Its review outcome is recorded (review_verdict, cerberus_commit,
//     phase_feedback, finished_at) in a call that does NOT also set
//     status -- so the row legitimately sits at status=awaiting_review
//     with finished_at already populated. This must satisfy
//     phases_lifecycle_check (migrations/040_session_plan_links.up.sql),
//     which is the exact bug that constraint fix addresses.
//  4. The phase is retried: AdjustedPrompt/RetryCount/Status=pending are
//     set together with the Clear* flags that wipe started_at,
//     finished_at, review_verdict, cerberus_commit and phase_feedback
//     back to NULL, and the resulting row is confirmed both to satisfy
//     the constraint and to have those fields actually cleared on
//     read-back.
func TestPhaseRetryCycle_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	repo := createTestPlanRepo(t, pool, "phase-retry-cycle")

	spec, err := CreateSpec(ctx, pool, repo.ID, "spec for phase retry cycle", "content", []byte(`[]`))
	if err != nil {
		t.Fatalf("CreateSpec() error = %v", err)
	}
	t.Cleanup(func() { _ = DeleteSpec(context.Background(), pool, spec.ID) })

	wf, err := CreateWorkflow(ctx, pool, spec.ID, "poc", nil)
	if err != nil {
		t.Fatalf("CreateWorkflow() error = %v", err)
	}
	t.Cleanup(func() { _ = DeleteWorkflow(context.Background(), pool, wf.ID) })

	phase, err := CreatePhase(ctx, pool, wf.ID, 0, "phase-retry", "goal-retry", 60)
	if err != nil {
		t.Fatalf("CreatePhase() error = %v", err)
	}

	// 1. Start the phase.
	startedAt := time.Now()
	running := "running"
	if _, err := UpdatePhase(ctx, pool, phase.ID, UpdatePhaseParams{
		Status:    &running,
		StartedAt: &startedAt,
	}); err != nil {
		t.Fatalf("UpdatePhase(running) error = %v", err)
	}

	// 2. Move to awaiting_review, status-only, as execPhase does.
	awaitingReview := "awaiting_review"
	if _, err := UpdatePhase(ctx, pool, phase.ID, UpdatePhaseParams{Status: &awaitingReview}); err != nil {
		t.Fatalf("UpdatePhase(awaiting_review) error = %v", err)
	}

	// 3. Record the review outcome without touching status: this leaves
	// the row at status=awaiting_review with finished_at set, which must
	// be accepted by phases_lifecycle_check.
	finishedAt := time.Now()
	failVerdict := "fail"
	commit := ""
	phaseFeedback := []byte(`{"verdict":"fail"}`)
	afterOutcome, err := UpdatePhase(ctx, pool, phase.ID, UpdatePhaseParams{
		ReviewVerdict:  &failVerdict,
		CerberusCommit: &commit,
		PhaseFeedback:  phaseFeedback,
		FinishedAt:     &finishedAt,
	})
	if err != nil {
		t.Fatalf("UpdatePhase(outcome) error = %v, want phases_lifecycle_check to accept awaiting_review with finished_at set", err)
	}
	if afterOutcome.Status != "awaiting_review" {
		t.Fatalf("afterOutcome.Status = %q, want %q", afterOutcome.Status, "awaiting_review")
	}
	if afterOutcome.FinishedAt == nil {
		t.Fatal("afterOutcome.FinishedAt = nil, want set")
	}
	if afterOutcome.ReviewVerdict == nil || *afterOutcome.ReviewVerdict != "fail" {
		t.Fatalf("afterOutcome.ReviewVerdict = %v, want pointer to %q", afterOutcome.ReviewVerdict, "fail")
	}

	// 4. Retry: reset to pending and clear the previous attempt's outcome
	// fields, as the bottom-of-execPhase retry branch now does.
	adjusted := "retry prompt"
	newRetryCount := phase.RetryCount + 1
	pending := "pending"
	afterRetry, err := UpdatePhase(ctx, pool, phase.ID, UpdatePhaseParams{
		AdjustedPrompt:      &adjusted,
		RetryCount:          &newRetryCount,
		Status:              &pending,
		ClearStartedAt:      true,
		ClearFinishedAt:     true,
		ClearReviewVerdict:  true,
		ClearCerberusCommit: true,
		ClearPhaseFeedback:  true,
	})
	if err != nil {
		t.Fatalf("UpdatePhase(retry reset) error = %v", err)
	}

	if afterRetry.Status != "pending" {
		t.Fatalf("afterRetry.Status = %q, want %q", afterRetry.Status, "pending")
	}
	if afterRetry.RetryCount != newRetryCount {
		t.Fatalf("afterRetry.RetryCount = %d, want %d", afterRetry.RetryCount, newRetryCount)
	}
	if afterRetry.StartedAt != nil {
		t.Fatalf("afterRetry.StartedAt = %v, want nil after retry reset", afterRetry.StartedAt)
	}
	if afterRetry.FinishedAt != nil {
		t.Fatalf("afterRetry.FinishedAt = %v, want nil after retry reset", afterRetry.FinishedAt)
	}
	if afterRetry.ReviewVerdict != nil {
		t.Fatalf("afterRetry.ReviewVerdict = %v, want nil after retry reset", afterRetry.ReviewVerdict)
	}
	if afterRetry.CerberusCommit != nil {
		t.Fatalf("afterRetry.CerberusCommit = %v, want nil after retry reset", afterRetry.CerberusCommit)
	}
	if !bytes.Equal(afterRetry.PhaseFeedback, defaultPhaseFeedback) {
		t.Fatalf("afterRetry.PhaseFeedback = %s, want default %s after retry reset", afterRetry.PhaseFeedback, defaultPhaseFeedback)
	}

	// Read back independently to confirm the clear actually persisted
	// (not just reflected in the RETURNING clause of the same statement).
	reread, err := GetPhase(ctx, pool, phase.ID)
	if err != nil {
		t.Fatalf("GetPhase() error = %v", err)
	}
	if reread.Status != "pending" || reread.StartedAt != nil || reread.FinishedAt != nil ||
		reread.ReviewVerdict != nil || reread.CerberusCommit != nil || !bytes.Equal(reread.PhaseFeedback, defaultPhaseFeedback) {
		t.Fatalf("GetPhase() reread = %+v, want pending with started_at/finished_at/review_verdict/cerberus_commit nil and phase_feedback = %s", reread, defaultPhaseFeedback)
	}
}
