package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
	"github.com/tonis2/foundry/internal/telemetry"
)

// testDBURLEnv names the environment variable that opts this suite into
// running against a real PostgreSQL instance, mirroring internal/db's
// and internal/httpapi's integration test gating.
const testDBURLEnv = "FOUNDRY_TEST_DATABASE_URL"

func testPool(t *testing.T) *pgxpool.Pool {
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
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
		t.Fatalf("migrate close: src=%v db=%v", srcErr, dbErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool.Ping: %v", err)
	}
	return pool
}

func createTestPlanForSessionLinkEvents(t *testing.T, pool *pgxpool.Pool, suffix string) db.Plan {
	t.Helper()
	remote := "https://example.com/foo/session-link-" + suffix + ".git"
	repo, err := db.CreateRepository(context.Background(), pool, repository.Repository{
		Name:      "session-link-repo-" + suffix,
		RemoteURL: &remote,
	})
	if err != nil {
		t.Fatalf("CreateRepository() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM repositories WHERE id = $1`, repo.ID)
	})

	plan, err := db.CreatePlan(context.Background(), pool, []int64{repo.ID}, "plan-session-link-"+suffix, "summary", "content")
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE id = $1`, plan.ID)
	})
	return plan
}

func sessionStartEventJSON(session, sourceSessionID string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":       "session_start",
		"session":    session,
		"session_id": sourceSessionID,
		"ts":         time.Now().UTC().Format(time.RFC3339),
	})
	return b
}

// TestIngestCerberusTelemetryWith_StewardSessionLinksToPlan_Postgres
// verifies that a session-start event for a Cerberus session named
// following the Steward review naming convention
// (foundry-steward-<planID>-<fingerprint>-<timestamp>, see
// internal/review.stewardSessionName) produces exactly one
// system_derived session_plan_links row, with zero manual steps beyond
// processing the event itself.
func TestIngestCerberusTelemetryWith_StewardSessionLinksToPlan_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := &Server{pool: pool}

	plan := createTestPlanForSessionLinkEvents(t, pool, "steward-ok")
	session := fmt.Sprintf("foundry-steward-%d-abcdef123456-%d", plan.ID, time.Now().UnixNano())

	raw := sessionStartEventJSON(session, "src-"+session)
	evt := compactCerberusEvent{Type: "session_start", Session: session}
	s.ingestCerberusTelemetryWith(ctx, raw, evt, telemetry.Ingest)

	agentSession, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() error = %v", err)
	}

	links, err := db.ListSessionPlanLinksByPlan(ctx, pool, plan.ID)
	if err != nil {
		t.Fatalf("ListSessionPlanLinksByPlan() error = %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("len(links) = %d, want 1 (links=%+v)", len(links), links)
	}
	if links[0].AgentSessionID != agentSession.ID {
		t.Fatalf("links[0].AgentSessionID = %d, want %d", links[0].AgentSessionID, agentSession.ID)
	}
	if links[0].Method != db.SessionPlanLinkMethodSystemDerived {
		t.Fatalf("links[0].Method = %q, want %q", links[0].Method, db.SessionPlanLinkMethodSystemDerived)
	}
	if links[0].PlanStepID != nil {
		t.Fatalf("links[0].PlanStepID = %v, want nil", links[0].PlanStepID)
	}
}

// TestIngestCerberusTelemetryWith_MalformedStewardSessionNameIgnored_Postgres
// verifies that session names that merely resemble the Steward
// convention but have a non-numeric or missing plan id segment are
// defensively ignored: no session_plan_links row is created and
// processing the event neither errors nor panics.
func TestIngestCerberusTelemetryWith_MalformedStewardSessionNameIgnored_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := &Server{pool: pool}

	cases := []string{
		"foundry-steward-notanumber-abcdef123456-12345",
		"foundry-steward-abcdef123456",
		"foundry-steward-",
	}

	for _, session := range cases {
		t.Run(session, func(t *testing.T) {
			raw := sessionStartEventJSON(session, "src-"+session)
			evt := compactCerberusEvent{Type: "session_start", Session: session}

			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("ingestCerberusTelemetryWith panicked: %v", r)
					}
				}()
				s.ingestCerberusTelemetryWith(ctx, raw, evt, telemetry.Ingest)
			}()

			if _, err := db.GetAgentSessionBySession(ctx, pool, session); err != nil {
				t.Fatalf("GetAgentSessionBySession() error = %v", err)
			}

			var n int
			if err := pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM session_plan_links l
				 JOIN agent_sessions a ON a.id = l.agent_session_id
				 WHERE a.session = $1`, session,
			).Scan(&n); err != nil {
				t.Fatalf("count session_plan_links: %v", err)
			}
			if n != 0 {
				t.Fatalf("session_plan_links rows for malformed session %q = %d, want 0", session, n)
			}
		})
	}
}

// TestResolveManagedCerberusAttribution_PhaseLaunchedSessionUnchanged_Postgres
// is a regression test guarding that a phase-launched session's
// attribution (resolved live via the phases table lookup) is unaffected
// by the addition of Steward session-plan linking: a session-start
// event for a session recorded as a phase's cerberus_session must still
// resolve managed attribution to that phase/repository, and must not
// produce any session_plan_links row (phase-launched sessions have no
// traceable plans-table row to link to).
func TestResolveManagedCerberusAttribution_PhaseLaunchedSessionUnchanged_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := &Server{pool: pool}

	remote := "https://example.com/foo/phase-launch.git"
	repo, err := db.CreateRepository(ctx, pool, repository.Repository{
		Name:      "phase-launch-repo",
		RemoteURL: &remote,
	})
	if err != nil {
		t.Fatalf("CreateRepository() error = %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM repositories WHERE id = $1`, repo.ID) })

	spec, err := db.CreateSpec(ctx, pool, repo.ID, "spec-phase-launch", "content", []byte(`[]`))
	if err != nil {
		t.Fatalf("CreateSpec() error = %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM specs WHERE id = $1`, spec.ID) })

	wf, err := db.CreateWorkflow(ctx, pool, spec.ID, "default", nil)
	if err != nil {
		t.Fatalf("CreateWorkflow() error = %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workflows WHERE id = $1`, wf.ID) })

	ph, err := db.CreatePhase(ctx, pool, wf.ID, 0, "phase-1", "goal", 60)
	if err != nil {
		t.Fatalf("CreatePhase() error = %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM phases WHERE id = $1`, ph.ID) })

	session := fmt.Sprintf("phase-session-%d", time.Now().UnixNano())
	cs := session
	if _, err := db.UpdatePhase(ctx, pool, ph.ID, db.UpdatePhaseParams{CerberusSession: &cs}); err != nil {
		t.Fatalf("UpdatePhase() error = %v", err)
	}

	attribution, ok := s.resolveManagedCerberusAttribution(ctx, session)
	if !ok {
		t.Fatal("resolveManagedCerberusAttribution() ok = false, want true")
	}
	if attribution.PhaseID == nil || *attribution.PhaseID != ph.ID {
		t.Fatalf("attribution.PhaseID = %v, want %d", attribution.PhaseID, ph.ID)
	}
	if attribution.RepositoryID == nil || *attribution.RepositoryID != repo.ID {
		t.Fatalf("attribution.RepositoryID = %v, want %d", attribution.RepositoryID, repo.ID)
	}

	raw := sessionStartEventJSON(session, "src-"+session)
	evt := compactCerberusEvent{Type: "session_start", Session: session}
	s.ingestCerberusTelemetryWith(ctx, raw, evt, telemetry.Ingest)

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM session_plan_links l
		 JOIN agent_sessions a ON a.id = l.agent_session_id
		 WHERE a.session = $1`, session,
	).Scan(&n); err != nil {
		t.Fatalf("count session_plan_links: %v", err)
	}
	if n != 0 {
		t.Fatalf("session_plan_links rows for phase-launched session = %d, want 0", n)
	}
}
