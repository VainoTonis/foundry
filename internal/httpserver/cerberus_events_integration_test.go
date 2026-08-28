package httpserver

import (
	"context"
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
	"github.com/tonis2/foundry/internal/telemetry"
)

const cerberusTestDBURLEnv = "FOUNDRY_TEST_DATABASE_URL"

func cerberusTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv(cerberusTestDBURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test", cerberusTestDBURLEnv)
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

func cleanupAgentSessions(t *testing.T, pool *pgxpool.Pool, sessions ...string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM agent_sessions WHERE session = ANY($1::text[])`, sessions); err != nil {
			t.Errorf("cleanup delete agent_sessions %v: %v", sessions, err)
		}
	})
}

// TestIngestCerberusTelemetryWith_SessionStartResolvesParentSourceSessionID
// verifies that a session_start event whose parent_session has the
// "pi:<source_session_id>" shape resolves parent_source_session_id when a
// matching agent_sessions row already exists, and leaves it NULL (without
// error) both when no matching row exists yet and when parent_session does
// not have the "pi:" shape at all.
func TestIngestCerberusTelemetryWith_SessionStartResolvesParentSourceSessionID(t *testing.T) {
	pool := cerberusTestPool(t)
	ctx := context.Background()
	s := &Server{pool: pool}

	parentSession := "cerb-parent-session"
	parentSourceSessionID := "cerb-parent-source-id"

	if _, err := db.EnsureAgentSession(ctx, pool, db.EnsureAgentSessionParams{
		Session:         parentSession,
		SourceSessionID: parentSourceSessionID,
		Origin:          "cerberus",
		Kind:            "coding",
		LifecycleState:  "active",
		StartEventSeen:  true,
	}); err != nil {
		t.Fatalf("EnsureAgentSession(parent): %v", err)
	}

	t.Run("resolves when parent already recorded", func(t *testing.T) {
		childSession := "cerb-child-known-parent"
		cleanupAgentSessions(t, pool, parentSession, childSession)

		raw := []byte(fmt.Sprintf(
			`{"type":"session_start","session":%q,"session_id":"cerb-child-known-parent-src","parent_session":"pi:%s"}`,
			childSession, parentSourceSessionID))
		evt := compactCerberusEvent{Type: "session_start", Session: childSession}

		s.ingestCerberusTelemetryWith(ctx, raw, evt, telemetry.Ingest)

		got, err := db.GetAgentSessionBySession(ctx, pool, childSession)
		if err != nil {
			t.Fatalf("GetAgentSessionBySession: %v", err)
		}
		if got.ParentSourceSessionID == nil || *got.ParentSourceSessionID != parentSourceSessionID {
			t.Fatalf("ParentSourceSessionID = %v, want %q", got.ParentSourceSessionID, parentSourceSessionID)
		}
	})

	t.Run("leaves NULL when parent not yet recorded", func(t *testing.T) {
		childSession := "cerb-child-unknown-parent"
		cleanupAgentSessions(t, pool, childSession)

		raw := []byte(fmt.Sprintf(
			`{"type":"session_start","session":%q,"session_id":"cerb-child-unknown-parent-src","parent_session":"pi:no-such-source-session-id"}`,
			childSession))
		evt := compactCerberusEvent{Type: "session_start", Session: childSession}

		s.ingestCerberusTelemetryWith(ctx, raw, evt, telemetry.Ingest)

		got, err := db.GetAgentSessionBySession(ctx, pool, childSession)
		if err != nil {
			t.Fatalf("GetAgentSessionBySession: %v", err)
		}
		if got.ParentSourceSessionID != nil {
			t.Fatalf("ParentSourceSessionID = %v, want nil", *got.ParentSourceSessionID)
		}
	})

	t.Run("leaves NULL when parent_session lacks pi: prefix", func(t *testing.T) {
		childSession := "cerb-child-non-pi-parent"
		cleanupAgentSessions(t, pool, childSession)

		raw := []byte(fmt.Sprintf(
			`{"type":"session_start","session":%q,"session_id":"cerb-child-non-pi-parent-src","parent_session":"/some/raw/path.jsonl"}`,
			childSession))
		evt := compactCerberusEvent{Type: "session_start", Session: childSession}

		s.ingestCerberusTelemetryWith(ctx, raw, evt, telemetry.Ingest)

		got, err := db.GetAgentSessionBySession(ctx, pool, childSession)
		if err != nil {
			t.Fatalf("GetAgentSessionBySession: %v", err)
		}
		if got.ParentSourceSessionID != nil {
			t.Fatalf("ParentSourceSessionID = %v, want nil", *got.ParentSourceSessionID)
		}
	})

	t.Run("leaves NULL when parent_session is empty", func(t *testing.T) {
		childSession := "cerb-child-empty-parent"
		cleanupAgentSessions(t, pool, childSession)

		raw := []byte(fmt.Sprintf(
			`{"type":"session_start","session":%q,"session_id":"cerb-child-empty-parent-src"}`,
			childSession))
		evt := compactCerberusEvent{Type: "session_start", Session: childSession}

		s.ingestCerberusTelemetryWith(ctx, raw, evt, telemetry.Ingest)

		got, err := db.GetAgentSessionBySession(ctx, pool, childSession)
		if err != nil {
			t.Fatalf("GetAgentSessionBySession: %v", err)
		}
		if got.ParentSourceSessionID != nil {
			t.Fatalf("ParentSourceSessionID = %v, want nil", *got.ParentSourceSessionID)
		}
	})
}

// TestIngestCerberusTelemetryWith_SessionStartAttributionRegression is a
// regression check that ingesting a session_start event through the real
// telemetry.Ingest path still records the base session fields as before
// this change (session/source_session_id/origin/kind), independent of
// parent resolution.
func TestIngestCerberusTelemetryWith_SessionStartAttributionRegression(t *testing.T) {
	pool := cerberusTestPool(t)
	ctx := context.Background()
	s := &Server{pool: pool}

	childSession := "cerb-child-attribution-regression"
	cleanupAgentSessions(t, pool, childSession)

	raw := []byte(fmt.Sprintf(
		`{"type":"session_start","session":%q,"session_id":"cerb-child-attribution-regression-src","kind":"coding","model":"anthropic/claude"}`,
		childSession))
	evt := compactCerberusEvent{Type: "session_start", Session: childSession}

	s.ingestCerberusTelemetryWith(ctx, raw, evt, telemetry.Ingest)

	got, err := db.GetAgentSessionBySession(ctx, pool, childSession)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession: %v", err)
	}
	if got.Session != childSession {
		t.Fatalf("Session = %q, want %q", got.Session, childSession)
	}
	if got.SourceSessionID != "cerb-child-attribution-regression-src" {
		t.Fatalf("SourceSessionID = %q, want %q", got.SourceSessionID, "cerb-child-attribution-regression-src")
	}
	if got.Origin != "cerberus" {
		t.Fatalf("Origin = %q, want %q", got.Origin, "cerberus")
	}
	if got.Kind != "coding" {
		t.Fatalf("Kind = %q, want %q", got.Kind, "coding")
	}
	if got.ParentSourceSessionID != nil {
		t.Fatalf("ParentSourceSessionID = %v, want nil", *got.ParentSourceSessionID)
	}
}
