package db

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testMigrator opens a *migrate.Migrate against the same database and
// migrations directory testPool uses, for tests that need to step the
// schema down and back up (migration round-trip coverage). Unlike
// testPool, the returned instance is not closed until t.Cleanup so
// callers can call Steps repeatedly within one test.
func testMigrator(t *testing.T) *migrate.Migrate {
	t.Helper()

	url := os.Getenv(testDBURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test", testDBURLEnv)
	}

	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations path: %v", err)
	}

	m, err := migrate.New("file:///"+migrationsPath, url)
	if err != nil {
		t.Fatalf("migrate init: %v", err)
	}
	t.Cleanup(func() {
		if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
			t.Errorf("migrate close: src=%v db=%v", srcErr, dbErr)
		}
	})
	return m
}

// createTestPlanForReview creates a minimal single-repository plan for
// plan review tests to attach reviews to, cleaning up both the plan and
// its repository via t.Cleanup.
func createTestPlanForReview(t *testing.T, pool *pgxpool.Pool, suffix string) Plan {
	t.Helper()
	repo := createTestPlanRepo(t, pool, "review-"+suffix)
	plan, err := CreatePlan(context.Background(), pool, []int64{repo.ID}, "plan-review-"+suffix, "summary", "content")
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	t.Cleanup(func() { deleteTestPlan(t, pool, plan.ID) })
	return plan
}

// jsonEqual reports whether a and b encode the same JSON value,
// ignoring formatting differences (whitespace, key insertion order in
// nested objects as re-serialized by Postgres's JSONB storage, etc.)
// that a byte-for-byte comparison would wrongly treat as a mismatch.
func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return reflect.DeepEqual(av, bv)
}

func validCreatePlanReviewParams(planID int64) CreatePlanReviewParams {
	return CreatePlanReviewParams{
		PlanID:          planID,
		InputSnapshot:   json.RawMessage(`{"plan":"snapshot","steps":[1,2,3]}`),
		ContractVersion: "v1",
		ContractContent: "# Engineering Contract v1\n\nExact contract body.",
		Model:           "economical-model-1",
		Session:         "session-abc",
	}
}

// TestPlanReviewLifecycle_Postgres exercises the full queued -> running ->
// completed and queued -> running -> failed transitions, checking that
// CreatePlanReview fingerprints its exact input, and that
// StartPlanReview/CompletePlanReview/FailPlanReview persist the right
// fields at each stage.
func TestPlanReviewLifecycle_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("create persists exact snapshot and fingerprints", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "lifecycle-create")
		params := validCreatePlanReviewParams(plan.ID)

		review, err := CreatePlanReview(ctx, pool, params)
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}
		if review.Status != PlanReviewStatusQueued {
			t.Fatalf("Status = %q, want %q", review.Status, PlanReviewStatusQueued)
		}
		if !jsonEqual(t, review.InputSnapshot, params.InputSnapshot) {
			t.Fatalf("InputSnapshot = %s, want %s", review.InputSnapshot, params.InputSnapshot)
		}
		wantInputHash := sha256Hex(params.InputSnapshot)
		if review.InputSnapshotSHA256 != wantInputHash {
			t.Fatalf("InputSnapshotSHA256 = %q, want %q", review.InputSnapshotSHA256, wantInputHash)
		}
		wantContractHash := sha256Hex([]byte(params.ContractContent))
		if review.ContractSHA256 != wantContractHash {
			t.Fatalf("ContractSHA256 = %q, want %q", review.ContractSHA256, wantContractHash)
		}
		if review.ContractVersion != params.ContractVersion {
			t.Fatalf("ContractVersion = %q, want %q", review.ContractVersion, params.ContractVersion)
		}
		if review.Model != params.Model || review.Session != params.Session {
			t.Fatalf("Model/Session = %q/%q, want %q/%q", review.Model, review.Session, params.Model, params.Session)
		}
		if review.StartedAt != nil || review.Verdict != nil || review.Report != nil || review.Error != nil || review.CompletedAt != nil {
			t.Fatalf("queued review has started/terminal fields set: %+v", review)
		}

		fetched, err := GetPlanReview(ctx, pool, review.ID)
		if err != nil {
			t.Fatalf("GetPlanReview() error = %v", err)
		}
		if fetched.ID != review.ID {
			t.Fatalf("GetPlanReview() returned id %d, want %d", fetched.ID, review.ID)
		}
	})

	t.Run("start transitions queued to running with started_at set", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "lifecycle-start")
		review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}

		running, err := StartPlanReview(ctx, pool, review.ID)
		if err != nil {
			t.Fatalf("StartPlanReview() error = %v", err)
		}
		if running.Status != PlanReviewStatusRunning {
			t.Fatalf("Status = %q, want %q", running.Status, PlanReviewStatusRunning)
		}
		if running.StartedAt == nil {
			t.Fatal("StartedAt = nil, want set")
		}
		if running.Verdict != nil || running.Report != nil || running.Error != nil || running.CompletedAt != nil {
			t.Fatalf("running review has terminal fields set: %+v", running)
		}
	})

	t.Run("complete transitions running to completed with verdict and report", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "lifecycle-complete")
		review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}
		if _, err := StartPlanReview(ctx, pool, review.ID); err != nil {
			t.Fatalf("StartPlanReview() error = %v", err)
		}

		report := json.RawMessage(`{"summary":"looks fine","evidence":[]}`)
		completed, err := CompletePlanReview(ctx, pool, review.ID, PlanReviewVerdictPass, report)
		if err != nil {
			t.Fatalf("CompletePlanReview() error = %v", err)
		}
		if completed.Status != PlanReviewStatusCompleted {
			t.Fatalf("Status = %q, want %q", completed.Status, PlanReviewStatusCompleted)
		}
		if completed.Verdict == nil || *completed.Verdict != PlanReviewVerdictPass {
			t.Fatalf("Verdict = %v, want %q", completed.Verdict, PlanReviewVerdictPass)
		}
		if !jsonEqual(t, completed.Report, report) {
			t.Fatalf("Report = %s, want %s", completed.Report, report)
		}
		if completed.Error != nil {
			t.Fatalf("Error = %v, want nil", completed.Error)
		}
		if completed.StartedAt == nil {
			t.Fatal("StartedAt = nil, want set")
		}
		if completed.CompletedAt == nil {
			t.Fatal("CompletedAt = nil, want set")
		}
	})

	t.Run("fail transitions running to failed with error and no verdict/report", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "lifecycle-fail")
		review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}
		if _, err := StartPlanReview(ctx, pool, review.ID); err != nil {
			t.Fatalf("StartPlanReview() error = %v", err)
		}

		failed, err := FailPlanReview(ctx, pool, review.ID, "session timed out")
		if err != nil {
			t.Fatalf("FailPlanReview() error = %v", err)
		}
		if failed.Status != PlanReviewStatusFailed {
			t.Fatalf("Status = %q, want %q", failed.Status, PlanReviewStatusFailed)
		}
		if failed.Error == nil || *failed.Error != "session timed out" {
			t.Fatalf("Error = %v, want %q", failed.Error, "session timed out")
		}
		if failed.Verdict != nil || failed.Report != nil {
			t.Fatalf("failed review has verdict/report set: %+v", failed)
		}
		if failed.StartedAt == nil {
			t.Fatal("StartedAt = nil, want set")
		}
		if failed.CompletedAt == nil {
			t.Fatal("CompletedAt = nil, want set")
		}
	})
}

// TestPlanReviewInvalidTransitionsAndVerdicts_Postgres checks that a
// terminal review can never be transitioned again (in either direction),
// that completing or failing a still-queued review is rejected, that
// starting an already-started review is rejected, that CompletePlanReview
// rejects an out-of-range verdict, and that transitioning a nonexistent
// review reports ErrNotFound.
func TestPlanReviewInvalidTransitionsAndVerdicts_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("completing a still-queued review fails with ErrPlanReviewNotRunning", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "queued-complete")
		review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}

		_, err = CompletePlanReview(ctx, pool, review.ID, PlanReviewVerdictPass, json.RawMessage(`{}`))
		if !errors.Is(err, ErrPlanReviewNotRunning) {
			t.Fatalf("CompletePlanReview() on queued review error = %v, want ErrPlanReviewNotRunning", err)
		}
	})

	t.Run("failing a still-queued review fails with ErrPlanReviewNotRunning", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "queued-fail")
		review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}

		_, err = FailPlanReview(ctx, pool, review.ID, "too early")
		if !errors.Is(err, ErrPlanReviewNotRunning) {
			t.Fatalf("FailPlanReview() on queued review error = %v, want ErrPlanReviewNotRunning", err)
		}
	})

	t.Run("starting an already-running review fails with ErrPlanReviewNotQueued", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "double-start")
		review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}
		if _, err := StartPlanReview(ctx, pool, review.ID); err != nil {
			t.Fatalf("first StartPlanReview() error = %v", err)
		}

		_, err = StartPlanReview(ctx, pool, review.ID)
		if !errors.Is(err, ErrPlanReviewNotQueued) {
			t.Fatalf("second StartPlanReview() error = %v, want ErrPlanReviewNotQueued", err)
		}
	})

	t.Run("invalid verdict is rejected without mutating the row", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "invalid-verdict")
		review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}
		if _, err := StartPlanReview(ctx, pool, review.ID); err != nil {
			t.Fatalf("StartPlanReview() error = %v", err)
		}

		_, err = CompletePlanReview(ctx, pool, review.ID, "maybe", json.RawMessage(`{}`))
		if err == nil {
			t.Fatal("CompletePlanReview() error = nil, want an error for an invalid verdict")
		}

		got, err := GetPlanReview(ctx, pool, review.ID)
		if err != nil {
			t.Fatalf("GetPlanReview() error = %v", err)
		}
		if got.Status != PlanReviewStatusRunning {
			t.Fatalf("Status after rejected verdict = %q, want %q", got.Status, PlanReviewStatusRunning)
		}
	})

	t.Run("completing an already-completed review fails", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "double-complete")
		review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}
		if _, err := StartPlanReview(ctx, pool, review.ID); err != nil {
			t.Fatalf("StartPlanReview() error = %v", err)
		}
		if _, err := CompletePlanReview(ctx, pool, review.ID, PlanReviewVerdictPass, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("first CompletePlanReview() error = %v", err)
		}

		_, err = CompletePlanReview(ctx, pool, review.ID, PlanReviewVerdictRevise, json.RawMessage(`{}`))
		if !errors.Is(err, ErrPlanReviewNotRunning) {
			t.Fatalf("second CompletePlanReview() error = %v, want ErrPlanReviewNotRunning", err)
		}
	})

	t.Run("failing an already-failed review fails", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "double-fail")
		review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}
		if _, err := StartPlanReview(ctx, pool, review.ID); err != nil {
			t.Fatalf("StartPlanReview() error = %v", err)
		}
		if _, err := FailPlanReview(ctx, pool, review.ID, "first failure"); err != nil {
			t.Fatalf("first FailPlanReview() error = %v", err)
		}

		_, err = FailPlanReview(ctx, pool, review.ID, "second failure")
		if !errors.Is(err, ErrPlanReviewNotRunning) {
			t.Fatalf("second FailPlanReview() error = %v, want ErrPlanReviewNotRunning", err)
		}
	})

	t.Run("failing an already-completed review fails", func(t *testing.T) {
		plan := createTestPlanForReview(t, pool, "complete-then-fail")
		review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
		if err != nil {
			t.Fatalf("CreatePlanReview() error = %v", err)
		}
		if _, err := StartPlanReview(ctx, pool, review.ID); err != nil {
			t.Fatalf("StartPlanReview() error = %v", err)
		}
		if _, err := CompletePlanReview(ctx, pool, review.ID, PlanReviewVerdictEscalate, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("CompletePlanReview() error = %v", err)
		}

		_, err = FailPlanReview(ctx, pool, review.ID, "too late")
		if !errors.Is(err, ErrPlanReviewNotRunning) {
			t.Fatalf("FailPlanReview() on completed review error = %v, want ErrPlanReviewNotRunning", err)
		}
	})

	t.Run("transitioning a nonexistent review reports ErrNotFound", func(t *testing.T) {
		const neverIssuedReviewID int64 = 1 << 40

		if _, err := StartPlanReview(ctx, pool, neverIssuedReviewID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("StartPlanReview() error = %v, want ErrNotFound", err)
		}
		if _, err := CompletePlanReview(ctx, pool, neverIssuedReviewID, PlanReviewVerdictPass, json.RawMessage(`{}`)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("CompletePlanReview() error = %v, want ErrNotFound", err)
		}
		if _, err := FailPlanReview(ctx, pool, neverIssuedReviewID, "boom"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("FailPlanReview() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("creating a review for a nonexistent plan reports ErrNotFound", func(t *testing.T) {
		const neverIssuedPlanID int64 = 1 << 40
		_, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(neverIssuedPlanID))
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("CreatePlanReview() error = %v, want ErrNotFound", err)
		}
	})
}

// TestPlanReviewCascade_Postgres verifies that deleting a plan cascades
// to delete every plan review that belongs to it.
func TestPlanReviewCascade_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	repo := createTestPlanRepo(t, pool, "review-cascade")
	plan, err := CreatePlan(ctx, pool, []int64{repo.ID}, "plan-review-cascade", "summary", "content")
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}

	review1, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
	if err != nil {
		t.Fatalf("CreatePlanReview() error = %v", err)
	}
	review2, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
	if err != nil {
		t.Fatalf("CreatePlanReview() error = %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, plan.ID); err != nil {
		t.Fatalf("delete plan: %v", err)
	}

	for _, id := range []int64{review1.ID, review2.ID} {
		if _, err := GetPlanReview(ctx, pool, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetPlanReview(%d) after plan delete error = %v, want ErrNotFound", id, err)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM plan_reviews WHERE plan_id = $1`, plan.ID).Scan(&count); err != nil {
		t.Fatalf("count plan_reviews: %v", err)
	}
	if count != 0 {
		t.Fatalf("plan_reviews rows remaining for deleted plan = %d, want 0", count)
	}
}

// TestGetLatestPlanReviewByInputHash_Postgres verifies that lookup by
// exact input fingerprint returns the most recently created review
// matching that fingerprint, ignores reviews for other input snapshots,
// and reports ErrNotFound when no review matches.
func TestGetLatestPlanReviewByInputHash_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	plan := createTestPlanForReview(t, pool, "latest-hash")

	snapshotA := json.RawMessage(`{"version":"a"}`)
	snapshotB := json.RawMessage(`{"version":"b"}`)

	paramsA := validCreatePlanReviewParams(plan.ID)
	paramsA.InputSnapshot = snapshotA
	older, err := CreatePlanReview(ctx, pool, paramsA)
	if err != nil {
		t.Fatalf("CreatePlanReview() (older, snapshot A) error = %v", err)
	}

	paramsB := validCreatePlanReviewParams(plan.ID)
	paramsB.InputSnapshot = snapshotB
	if _, err := CreatePlanReview(ctx, pool, paramsB); err != nil {
		t.Fatalf("CreatePlanReview() (snapshot B) error = %v", err)
	}

	newer, err := CreatePlanReview(ctx, pool, paramsA)
	if err != nil {
		t.Fatalf("CreatePlanReview() (newer, snapshot A) error = %v", err)
	}

	hashA := sha256Hex(snapshotA)
	latest, err := GetLatestPlanReviewByInputHash(ctx, pool, plan.ID, hashA)
	if err != nil {
		t.Fatalf("GetLatestPlanReviewByInputHash() error = %v", err)
	}
	if latest.ID != newer.ID {
		t.Fatalf("GetLatestPlanReviewByInputHash() returned id %d, want the newer review id %d (older id %d)", latest.ID, newer.ID, older.ID)
	}

	hashC := sha256Hex(json.RawMessage(`{"version":"never-created"}`))
	if _, err := GetLatestPlanReviewByInputHash(ctx, pool, plan.ID, hashC); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLatestPlanReviewByInputHash() for unmatched hash error = %v, want ErrNotFound", err)
	}
}

// TestPlanReviewsTableMigrationRoundTrip_Postgres exercises migrations
// 038 and 039's combined down/up round trip: dropping and recreating the
// plan_reviews table (039 down, then 038 down, then both back up) must
// leave it empty and immediately usable via CreatePlanReview in the
// queued state.
func TestPlanReviewsTableMigrationRoundTrip_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	m := testMigrator(t)

	if err := m.Steps(-2); err != nil {
		t.Fatalf("migrate down two steps (039, 038 down): %v", err)
	}
	restored := false
	t.Cleanup(func() {
		if restored {
			return
		}
		if err := m.Steps(2); err != nil {
			t.Errorf("re-apply migrations 038, 039 in cleanup: %v", err)
		}
	})

	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'plan_reviews')`,
	).Scan(&exists); err != nil {
		t.Fatalf("check plan_reviews table existence: %v", err)
	}
	if exists {
		t.Fatal("plan_reviews table still exists after migrating down, want it dropped")
	}

	if err := m.Steps(2); err != nil {
		t.Fatalf("migrate up two steps (038, 039 up): %v", err)
	}
	restored = true

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM plan_reviews`).Scan(&count); err != nil {
		t.Fatalf("count plan_reviews after re-applying migrations: %v", err)
	}
	if count != 0 {
		t.Fatalf("plan_reviews row count after fresh migrate up = %d, want 0", count)
	}

	plan := createTestPlanForReview(t, pool, "migration-roundtrip")
	review, err := CreatePlanReview(ctx, pool, validCreatePlanReviewParams(plan.ID))
	if err != nil {
		t.Fatalf("CreatePlanReview() after migration round trip error = %v", err)
	}
	if review.Status != PlanReviewStatusQueued {
		t.Fatalf("Status = %q, want %q", review.Status, PlanReviewStatusQueued)
	}
}

// TestPlanReviewLifecycleMigrationRoundTrip_Postgres exercises migration
// 039 in isolation: migrating it down must fold queued/running rows back
// to the legacy pending status and drop started_at without losing any
// row's immutable provenance columns, and migrating back up must restore
// the queued/running/completed/failed lifecycle and started_at column
// so CreatePlanReview and StartPlanReview work again.
func TestPlanReviewLifecycleMigrationRoundTrip_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	m := testMigrator(t)

	plan := createTestPlanForReview(t, pool, "lifecycle-migration-roundtrip")
	params := validCreatePlanReviewParams(plan.ID)
	queued, err := CreatePlanReview(ctx, pool, params)
	if err != nil {
		t.Fatalf("CreatePlanReview() error = %v", err)
	}
	running, err := CreatePlanReview(ctx, pool, params)
	if err != nil {
		t.Fatalf("CreatePlanReview() error = %v", err)
	}
	if _, err := StartPlanReview(ctx, pool, running.ID); err != nil {
		t.Fatalf("StartPlanReview() error = %v", err)
	}

	if err := m.Steps(-1); err != nil {
		t.Fatalf("migrate down one step (039 down): %v", err)
	}
	restored := false
	t.Cleanup(func() {
		if restored {
			return
		}
		if err := m.Steps(1); err != nil {
			t.Errorf("re-apply migration 039 in cleanup: %v", err)
		}
	})

	var hasStartedAt bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'plan_reviews' AND column_name = 'started_at')`,
	).Scan(&hasStartedAt); err != nil {
		t.Fatalf("check started_at column existence: %v", err)
	}
	if hasStartedAt {
		t.Fatal("started_at column still exists after migrating 039 down, want it dropped")
	}

	for _, id := range []int64{queued.ID, running.ID} {
		var status string
		var inputHash string
		if err := pool.QueryRow(ctx,
			`SELECT status, input_snapshot_sha256 FROM plan_reviews WHERE id = $1`, id,
		).Scan(&status, &inputHash); err != nil {
			t.Fatalf("query downgraded review %d: %v", id, err)
		}
		if status != "pending" {
			t.Fatalf("review %d status after 039 down = %q, want %q", id, status, "pending")
		}
		if inputHash != sha256Hex(params.InputSnapshot) {
			t.Fatalf("review %d input_snapshot_sha256 after 039 down = %q, provenance was not preserved", id, inputHash)
		}
	}

	if err := m.Steps(1); err != nil {
		t.Fatalf("migrate up one step (039 up): %v", err)
	}
	restored = true

	for _, id := range []int64{queued.ID, running.ID} {
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM plan_reviews WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("query restored review %d: %v", id, err)
		}
		if status != PlanReviewStatusQueued {
			t.Fatalf("review %d status after 039 up = %q, want %q", id, status, PlanReviewStatusQueued)
		}
	}

	started, err := StartPlanReview(ctx, pool, queued.ID)
	if err != nil {
		t.Fatalf("StartPlanReview() after migration round trip error = %v", err)
	}
	if started.Status != PlanReviewStatusRunning || started.StartedAt == nil {
		t.Fatalf("StartPlanReview() after migration round trip = %+v, want running with started_at set", started)
	}
}
