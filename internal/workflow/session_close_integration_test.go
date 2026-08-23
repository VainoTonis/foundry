package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tonis2/foundry/internal/db"
)

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

// TestCloseManagedSessionSetsEndedAt_Postgres verifies that
// Runner.closeManagedSession, invoked directly (without any turn_complete
// telemetry event), records ended_at against a real PostgreSQL agent
// session.
func TestCloseManagedSessionSetsEndedAt_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := "workflow-close-managed-session"
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM agent_sessions WHERE session = $1`, session); err != nil {
			t.Errorf("cleanup delete agent_sessions %q: %v", session, err)
		}
	})

	created, err := db.EnsureAgentSession(ctx, pool, db.EnsureAgentSessionParams{
		Session:         session,
		SourceSessionID: session + "-source",
		Origin:          "cerberus",
		Kind:            "coding",
	})
	if err != nil {
		t.Fatalf("EnsureAgentSession() error = %v", err)
	}
	if created.EndedAt != nil {
		t.Fatalf("created.EndedAt = %v, want nil before closure", created.EndedAt)
	}

	r := &Runner{pool: pool}
	r.closeManagedSession(session)

	got, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() error = %v", err)
	}
	if got.EndedAt == nil {
		t.Fatal("EndedAt = nil, want set after closeManagedSession without a turn_complete event")
	}
}

// TestCloseManagedSessionUnknownSessionIsBestEffort_Postgres verifies that
// closing a session name with no matching telemetry row is a no-op rather
// than an error, since phase outcome must never be affected by best-effort
// closure.
func TestCloseManagedSessionUnknownSessionIsBestEffort_Postgres(t *testing.T) {
	pool := testPool(t)

	r := &Runner{pool: pool}
	r.closeManagedSession("workflow-close-managed-session-does-not-exist")
}
