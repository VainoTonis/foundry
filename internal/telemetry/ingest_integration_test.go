package telemetry

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

func TestIngest_FullSession_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := "telemetry-ingest-full-session"
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM agent_sessions WHERE session = $1`, session); err != nil {
			t.Errorf("cleanup delete agent_sessions %q: %v", session, err)
		}
	})

	start := time.Now().UTC().Truncate(time.Millisecond)

	if err := Ingest(ctx, pool, Event{
		Type: EventSessionStart,
		Session: Session{
			Session:         session,
			SourceSessionID: session + "-source",
			Origin:          "claude-code",
			Kind:            "coding",
		},
		Timestamp: start,
	}); err != nil {
		t.Fatalf("Ingest(session_start) error = %v", err)
	}

	got, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() error = %v", err)
	}
	if !got.StartedAt.Equal(start) {
		t.Fatalf("StartedAt = %v, want producer timestamp %v", got.StartedAt, start)
	}

	callID := "call-1"
	if err := Ingest(ctx, pool, Event{
		Type:    EventToolUse,
		Session: Session{Session: session},
		Tool:    Tool{ToolCallID: &callID, ToolName: "bash", Input: "ls -la"},
	}); err != nil {
		t.Fatalf("Ingest(tool_use) error = %v", err)
	}

	if err := Ingest(ctx, pool, Event{
		Type:    EventToolResult,
		Session: Session{Session: session},
		Tool:    Tool{ToolCallID: &callID, ToolName: "bash", Result: "total 0"},
	}); err != nil {
		t.Fatalf("Ingest(tool_result) error = %v", err)
	}

	if err := Ingest(ctx, pool, Event{
		Type:    EventMessageEnd,
		Session: Session{Session: session},
		Usage:   Usage{},
	}); err != nil {
		t.Fatalf("Ingest(zero-usage message_end) error = %v", err)
	}

	if err := Ingest(ctx, pool, Event{
		Type:    EventMessageEnd,
		Session: Session{Session: session},
		Usage:   Usage{InputTokens: 10, OutputTokens: 5, CostUSD: 0.02},
	}); err != nil {
		t.Fatalf("Ingest(nonzero-usage message_end) error = %v", err)
	}

	if err := Ingest(ctx, pool, Event{
		Type:    EventFinalMessage,
		Session: Session{Session: session},
		Message: Message{Role: "assistant", Content: "done"},
	}); err != nil {
		t.Fatalf("Ingest(final_message) error = %v", err)
	}

	if err := Ingest(ctx, pool, Event{
		Type:    EventSessionEnd,
		Session: Session{Session: session},
	}); err != nil {
		t.Fatalf("Ingest(session_end) error = %v", err)
	}

	final, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() after full ingest error = %v", err)
	}
	if final.EndedAt == nil {
		t.Fatal("EndedAt = nil, want set after session_end")
	}
	if final.InputTokens != 10 || final.OutputTokens != 5 {
		t.Fatalf("usage = (%d, %d), want (10, 5)", final.InputTokens, final.OutputTokens)
	}
	if final.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want 1", final.ToolCallCount)
	}
	if final.TurnCount != 1 {
		t.Fatalf("TurnCount = %d, want 1 (zero-usage message_end must not add a turn)", final.TurnCount)
	}
	if final.NextSeq != 4 {
		t.Fatalf("NextSeq = %d, want 4 (tool_use, tool_result, one message_end, final_message)", final.NextSeq)
	}
}

func TestIngest_UnknownSession_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	err := Ingest(ctx, pool, Event{
		Type:    EventToolUse,
		Session: Session{Session: "telemetry-ingest-does-not-exist"},
		Tool:    Tool{ToolName: "bash"},
	})
	if err == nil {
		t.Fatal("Ingest(tool_use) for unknown session error = nil, want error")
	}
}
