package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// createTestSessionPlanLinksSession is a small wrapper around
// EnsureAgentSession that gives each session a unique session/source id
// derived from suffix, for session_plan_links tests that only need a
// session to attach links to and do not care about its telemetry
// content.
func createTestSessionPlanLinksSession(t *testing.T, pool *pgxpool.Pool, suffix string) AgentSession {
	t.Helper()
	return createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "session-plan-links-" + suffix,
		SourceSessionID: "session-plan-links-src-" + suffix,
		Origin:          "test",
	})
}

// createTestPlanForSessionLinks creates a minimal single-repository plan
// for session_plan_links tests to attach links to, cleaning up both the
// plan and its repository via t.Cleanup.
func createTestPlanForSessionLinks(t *testing.T, pool *pgxpool.Pool, suffix string) Plan {
	t.Helper()
	repo := createTestPlanRepo(t, pool, "session-link-"+suffix)
	plan, err := CreatePlan(context.Background(), pool, []int64{repo.ID}, "plan-session-link-"+suffix, "summary", "content")
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	t.Cleanup(func() { deleteTestPlan(t, pool, plan.ID) })
	return plan
}

// TestSessionPlanLinks_CompositeStepFK_Postgres exercises the
// session_plan_links.plan_step_id composite foreign key added in
// migration 040: a NULL plan_step_id must always be accepted (MATCH
// SIMPLE), a plan_step_id belonging to a different plan than the linked
// plan_id must be rejected, and a plan_step_id that actually belongs to
// the linked plan_id must be accepted.
func TestSessionPlanLinks_CompositeStepFK_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("NULL plan_step_id is accepted", func(t *testing.T) {
		session := createTestSessionPlanLinksSession(t, pool, "null-step")
		plan := createTestPlanForSessionLinks(t, pool, "null-step")

		var id int64
		err := pool.QueryRow(ctx,
			`INSERT INTO session_plan_links (agent_session_id, plan_id, plan_step_id, method)
			 VALUES ($1, $2, NULL, 'system_derived') RETURNING id`,
			session.ID, plan.ID,
		).Scan(&id)
		if err != nil {
			t.Fatalf("insert with NULL plan_step_id error = %v", err)
		}
	})

	t.Run("plan_step_id belonging to a different plan is rejected", func(t *testing.T) {
		session := createTestSessionPlanLinksSession(t, pool, "wrong-plan")
		plan := createTestPlanForSessionLinks(t, pool, "wrong-plan-a")
		otherPlan := createTestPlanForSessionLinks(t, pool, "wrong-plan-b")

		otherStep, err := CreatePlanStep(ctx, pool, otherPlan.ID, 0, "step", nil)
		if err != nil {
			t.Fatalf("CreatePlanStep() error = %v", err)
		}

		_, err = pool.Exec(ctx,
			`INSERT INTO session_plan_links (agent_session_id, plan_id, plan_step_id, method)
			 VALUES ($1, $2, $3, 'system_derived')`,
			session.ID, plan.ID, otherStep.ID,
		)
		if err == nil {
			t.Fatal("insert with cross-plan plan_step_id error = nil, want a foreign key violation")
		}
	})

	t.Run("plan_step_id belonging to the linked plan is accepted", func(t *testing.T) {
		session := createTestSessionPlanLinksSession(t, pool, "right-plan")
		plan := createTestPlanForSessionLinks(t, pool, "right-plan")

		step, err := CreatePlanStep(ctx, pool, plan.ID, 0, "step", nil)
		if err != nil {
			t.Fatalf("CreatePlanStep() error = %v", err)
		}

		var id int64
		err = pool.QueryRow(ctx,
			`INSERT INTO session_plan_links (agent_session_id, plan_id, plan_step_id, method)
			 VALUES ($1, $2, $3, 'explicit') RETURNING id`,
			session.ID, plan.ID, step.ID,
		).Scan(&id)
		if err != nil {
			t.Fatalf("insert with matching plan_step_id error = %v", err)
		}
	})
}

// TestCreateSessionPlanLink_IdempotentPlanLevel_Postgres verifies that
// calling CreateSessionPlanLink twice with the identical
// (agent_session_id, plan_id, method) tuple at plan level (PlanStepID
// nil) returns the same row both times, with no error and without
// creating a second, duplicate row -- exercising the
// session_plan_links_plan_level_unique_idx partial unique index added
// in migration 041 and CreateSessionPlanLink's ON CONFLICT DO UPDATE
// handling for it.
func TestCreateSessionPlanLink_IdempotentPlanLevel_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := createTestSessionPlanLinksSession(t, pool, "idempotent-plan-level")
	plan := createTestPlanForSessionLinks(t, pool, "idempotent-plan-level")

	params := CreateSessionPlanLinkParams{
		AgentSessionID: session.ID,
		PlanID:         plan.ID,
		Method:         SessionPlanLinkMethodSystemDerived,
	}

	first, err := CreateSessionPlanLink(ctx, pool, params)
	if err != nil {
		t.Fatalf("CreateSessionPlanLink() first call error = %v", err)
	}

	second, err := CreateSessionPlanLink(ctx, pool, params)
	if err != nil {
		t.Fatalf("CreateSessionPlanLink() second call error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second.ID = %d, want %d (same row as first call)", second.ID, first.ID)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM session_plan_links WHERE agent_session_id = $1 AND plan_id = $2 AND method = $3`,
		session.ID, plan.ID, SessionPlanLinkMethodSystemDerived,
	).Scan(&count); err != nil {
		t.Fatalf("count session_plan_links: %v", err)
	}
	if count != 1 {
		t.Fatalf("count session_plan_links rows = %d, want 1", count)
	}
}

// TestCreateSessionPlanLink_IdempotentStepLevel_Postgres is the
// step-level counterpart of
// TestCreateSessionPlanLink_IdempotentPlanLevel_Postgres: calling
// CreateSessionPlanLink twice with the identical
// (agent_session_id, plan_id, plan_step_id, method) tuple must return
// the same row both times, exercising
// session_plan_links_step_level_unique_idx.
func TestCreateSessionPlanLink_IdempotentStepLevel_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := createTestSessionPlanLinksSession(t, pool, "idempotent-step-level")
	plan := createTestPlanForSessionLinks(t, pool, "idempotent-step-level")
	step, err := CreatePlanStep(ctx, pool, plan.ID, 0, "step", nil)
	if err != nil {
		t.Fatalf("CreatePlanStep() error = %v", err)
	}

	params := CreateSessionPlanLinkParams{
		AgentSessionID: session.ID,
		PlanID:         plan.ID,
		PlanStepID:     &step.ID,
		Method:         SessionPlanLinkMethodExplicit,
	}

	first, err := CreateSessionPlanLink(ctx, pool, params)
	if err != nil {
		t.Fatalf("CreateSessionPlanLink() first call error = %v", err)
	}

	second, err := CreateSessionPlanLink(ctx, pool, params)
	if err != nil {
		t.Fatalf("CreateSessionPlanLink() second call error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second.ID = %d, want %d (same row as first call)", second.ID, first.ID)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM session_plan_links WHERE agent_session_id = $1 AND plan_id = $2 AND plan_step_id = $3 AND method = $4`,
		session.ID, plan.ID, step.ID, SessionPlanLinkMethodExplicit,
	).Scan(&count); err != nil {
		t.Fatalf("count session_plan_links: %v", err)
	}
	if count != 1 {
		t.Fatalf("count session_plan_links rows = %d, want 1", count)
	}
}

// TestSessionPlanLinks_StepDeleteSetsNull_Postgres verifies the
// column-specific ON DELETE SET NULL (plan_step_id) behavior: deleting
// the referenced plan_steps row must leave the session_plan_links row in
// place with plan_step_id cleared to NULL, and must not touch plan_id
// (which is NOT NULL and independently protected by its own cascading
// FK to plans).
func TestSessionPlanLinks_StepDeleteSetsNull_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := createTestSessionPlanLinksSession(t, pool, "step-delete")
	plan := createTestPlanForSessionLinks(t, pool, "step-delete")
	step, err := CreatePlanStep(ctx, pool, plan.ID, 0, "step", nil)
	if err != nil {
		t.Fatalf("CreatePlanStep() error = %v", err)
	}

	var linkID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO session_plan_links (agent_session_id, plan_id, plan_step_id, method)
		 VALUES ($1, $2, $3, 'heuristic') RETURNING id`,
		session.ID, plan.ID, step.ID,
	).Scan(&linkID); err != nil {
		t.Fatalf("insert session_plan_links error = %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM plan_steps WHERE id = $1`, step.ID); err != nil {
		t.Fatalf("delete plan_steps row: %v", err)
	}

	var gotPlanID int64
	var gotStepID *int64
	if err := pool.QueryRow(ctx,
		`SELECT plan_id, plan_step_id FROM session_plan_links WHERE id = $1`, linkID,
	).Scan(&gotPlanID, &gotStepID); err != nil {
		t.Fatalf("query session_plan_links after step delete: %v", err)
	}
	if gotStepID != nil {
		t.Fatalf("plan_step_id after referenced step delete = %v, want NULL", *gotStepID)
	}
	if gotPlanID != plan.ID {
		t.Fatalf("plan_id after referenced step delete = %d, want unchanged %d", gotPlanID, plan.ID)
	}
}

// TestSessionPlanLinks_MethodCheck_Postgres verifies that all four
// documented method values are accepted and an arbitrary fifth value is
// rejected by session_plan_links_method_check.
func TestSessionPlanLinks_MethodCheck_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := createTestSessionPlanLinksSession(t, pool, "method-check")
	plan := createTestPlanForSessionLinks(t, pool, "method-check")

	for _, method := range []string{"system_derived", "explicit", "api_inferred", "heuristic"} {
		t.Run("accepts "+method, func(t *testing.T) {
			_, err := pool.Exec(ctx,
				`INSERT INTO session_plan_links (agent_session_id, plan_id, plan_step_id, method)
				 VALUES ($1, $2, NULL, $3)`,
				session.ID, plan.ID, method,
			)
			if err != nil {
				t.Fatalf("insert with method %q error = %v", method, err)
			}
		})
	}

	t.Run("rejects an arbitrary method value", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`INSERT INTO session_plan_links (agent_session_id, plan_id, plan_step_id, method)
			 VALUES ($1, $2, NULL, 'guesswork')`,
			session.ID, plan.ID,
		)
		if err == nil {
			t.Fatal("insert with invalid method error = nil, want a check constraint violation")
		}
	})
}

// TestPlanAndPlanStepsStatusCheck_Postgres verifies the shared
// {pending,running,done,failed} status vocabulary added to plans and
// plan_steps in migration 040: all four values are accepted by each
// table's status CHECK and an arbitrary value is rejected by both.
func TestPlanAndPlanStepsStatusCheck_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("plans.status", func(t *testing.T) {
		plan := createTestPlanForSessionLinks(t, pool, "status-check")

		for _, status := range []string{"pending", "running", "done", "failed"} {
			if _, err := pool.Exec(ctx, `UPDATE plans SET status = $1 WHERE id = $2`, status, plan.ID); err != nil {
				t.Fatalf("update plans.status to %q error = %v", status, err)
			}
		}

		if _, err := pool.Exec(ctx, `UPDATE plans SET status = 'bogus' WHERE id = $1`, plan.ID); err == nil {
			t.Fatal("update plans.status to an invalid value error = nil, want a check constraint violation")
		}
	})

	t.Run("plan_steps.status", func(t *testing.T) {
		plan := createTestPlanForSessionLinks(t, pool, "step-status-check")
		step, err := CreatePlanStep(ctx, pool, plan.ID, 0, "step", nil)
		if err != nil {
			t.Fatalf("CreatePlanStep() error = %v", err)
		}

		for _, status := range []string{"pending", "running", "done", "failed"} {
			if _, err := pool.Exec(ctx, `UPDATE plan_steps SET status = $1 WHERE id = $2`, status, step.ID); err != nil {
				t.Fatalf("update plan_steps.status to %q error = %v", status, err)
			}
		}

		if _, err := pool.Exec(ctx, `UPDATE plan_steps SET status = 'bogus' WHERE id = $1`, step.ID); err == nil {
			t.Fatal("update plan_steps.status to an invalid value error = nil, want a check constraint violation")
		}
	})
}

// TestPlanAndPlanStepsUpdatedAtTrigger_Postgres verifies the
// set_updated_at() trigger attached to plans and plan_steps in migration
// 040: an UPDATE that does not explicitly set updated_at must still
// result in a changed updated_at value.
func TestPlanAndPlanStepsUpdatedAtTrigger_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("plans", func(t *testing.T) {
		plan := createTestPlanForSessionLinks(t, pool, "updated-at-trigger")

		var before time.Time
		if err := pool.QueryRow(ctx, `SELECT updated_at FROM plans WHERE id = $1`, plan.ID).Scan(&before); err != nil {
			t.Fatalf("query updated_at before update: %v", err)
		}

		time.Sleep(5 * time.Millisecond)
		if _, err := pool.Exec(ctx, `UPDATE plans SET title = title WHERE id = $1`, plan.ID); err != nil {
			t.Fatalf("update plans without setting updated_at: %v", err)
		}

		var after time.Time
		if err := pool.QueryRow(ctx, `SELECT updated_at FROM plans WHERE id = $1`, plan.ID).Scan(&after); err != nil {
			t.Fatalf("query updated_at after update: %v", err)
		}
		if !after.After(before) {
			t.Fatalf("plans.updated_at after implicit update = %v, want after %v", after, before)
		}
	})

	t.Run("plan_steps", func(t *testing.T) {
		plan := createTestPlanForSessionLinks(t, pool, "step-updated-at-trigger")
		step, err := CreatePlanStep(ctx, pool, plan.ID, 0, "step", nil)
		if err != nil {
			t.Fatalf("CreatePlanStep() error = %v", err)
		}

		var before time.Time
		if err := pool.QueryRow(ctx, `SELECT updated_at FROM plan_steps WHERE id = $1`, step.ID).Scan(&before); err != nil {
			t.Fatalf("query updated_at before update: %v", err)
		}

		time.Sleep(5 * time.Millisecond)
		if _, err := pool.Exec(ctx, `UPDATE plan_steps SET text = text WHERE id = $1`, step.ID); err != nil {
			t.Fatalf("update plan_steps without setting updated_at: %v", err)
		}

		var after time.Time
		if err := pool.QueryRow(ctx, `SELECT updated_at FROM plan_steps WHERE id = $1`, step.ID).Scan(&after); err != nil {
			t.Fatalf("query updated_at after update: %v", err)
		}
		if !after.After(before) {
			t.Fatalf("plan_steps.updated_at after implicit update = %v, want after %v", after, before)
		}
	})
}
