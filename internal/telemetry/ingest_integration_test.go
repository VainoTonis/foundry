package telemetry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestIngest_PrivacyAnnotationsRoundTrip_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := "telemetry-ingest-privacy-annotations"
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM agent_sessions WHERE session = $1`, session); err != nil {
			t.Errorf("cleanup delete agent_sessions %q: %v", session, err)
		}
	})

	ingestToolPair := func(id string, input, result string, inputRedacted, inputOmitted, resultRedacted, resultOmitted bool) {
		t.Helper()
		if err := Ingest(ctx, pool, Event{
			Type: EventToolUse, Session: Session{Session: session},
			Tool: Tool{ToolCallID: &id, ToolName: "bash", Input: input, InputRedacted: inputRedacted, InputOmitted: inputOmitted},
		}); err != nil {
			t.Fatalf("Ingest(tool_use %s) error = %v", id, err)
		}
		if err := Ingest(ctx, pool, Event{
			Type: EventToolResult, Session: Session{Session: session},
			Tool: Tool{ToolCallID: &id, ToolName: "bash", Result: result, ResultRedacted: resultRedacted, ResultOmitted: resultOmitted},
		}); err != nil {
			t.Fatalf("Ingest(tool_result %s) error = %v", id, err)
		}
	}

	// Both omitted and naturally empty payloads persist as NULL content; the
	// annotations must preserve the reason for the absence.
	ingestToolPair("omitted", "", "", false, true, false, true)
	ingestToolPair("empty", "", "", false, false, false, false)

	largeInput := strings.Repeat("i", MaxContentBytes+17)
	largeResult := strings.Repeat("r", MaxContentBytes+19)
	ingestToolPair("redacted-truncated", largeInput, largeResult, true, false, true, false)

	if err := Ingest(ctx, pool, Event{
		Type: EventFinalMessage, Session: Session{Session: session},
		Message: Message{Role: "assistant", ContentRedacted: true},
	}); err != nil {
		t.Fatalf("Ingest(redacted final_message) error = %v", err)
	}
	if err := Ingest(ctx, pool, Event{
		Type: EventFinalMessage, Session: Session{Session: session},
		Message: Message{Role: "assistant"},
	}); err != nil {
		t.Fatalf("Ingest(empty final_message) error = %v", err)
	}

	sess, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() error = %v", err)
	}
	calls, err := db.ListAgentToolCallsBySession(ctx, pool, sess.ID)
	if err != nil {
		t.Fatalf("ListAgentToolCallsBySession() error = %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("tool calls = %d, want 3", len(calls))
	}
	if calls[0].ToolInput != nil || calls[0].ToolResult != nil || !calls[0].ToolInputOmitted || !calls[0].ToolResultOmitted {
		t.Fatalf("omitted tool call = %+v, want nil bodies with omission flags", calls[0])
	}
	if calls[1].ToolInput != nil || calls[1].ToolResult != nil || calls[1].ToolInputOmitted || calls[1].ToolResultOmitted {
		t.Fatalf("naturally empty tool call = %+v, want nil bodies without omission flags", calls[1])
	}
	truncated := calls[2]
	if !truncated.ToolInputRedacted || !truncated.ToolResultRedacted {
		t.Fatalf("redacted tool call flags = input:%v result:%v, want true/true", truncated.ToolInputRedacted, truncated.ToolResultRedacted)
	}
	if !truncated.ToolInputTruncated || truncated.ToolInputSHA256 == nil || truncated.ToolInputOriginalBytes == nil || *truncated.ToolInputOriginalBytes != int64(len(largeInput)) {
		t.Fatalf("input truncation metadata = %+v, want retained hash and %d original bytes", truncated, len(largeInput))
	}
	if !truncated.ToolResultTruncated || truncated.ToolResultSHA256 == nil || truncated.ToolResultOriginalBytes == nil || *truncated.ToolResultOriginalBytes != int64(len(largeResult)) {
		t.Fatalf("result truncation metadata = %+v, want retained hash and %d original bytes", truncated, len(largeResult))
	}

	messages, err := db.ListAgentMessagesBySession(ctx, pool, sess.ID)
	if err != nil {
		t.Fatalf("ListAgentMessagesBySession() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	if messages[0].Content != nil || !messages[0].ContentRedacted {
		t.Fatalf("redacted empty message = %+v, want nil content and content_redacted", messages[0])
	}
	if messages[1].Content != nil || messages[1].ContentRedacted {
		t.Fatalf("naturally empty message = %+v, want nil content without content_redacted", messages[1])
	}
}

func TestIngest_DuplicateEventID_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := "telemetry-ingest-dup-event-id"
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM agent_sessions WHERE session = $1`, session); err != nil {
			t.Errorf("cleanup delete agent_sessions %q: %v", session, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM telemetry_receipts WHERE producer_id = $1`, session+"-producer"); err != nil {
			t.Errorf("cleanup delete telemetry_receipts: %v", err)
		}
	})

	if err := Ingest(ctx, pool, Event{
		Type: EventSessionStart,
		Session: Session{
			Session:         session,
			SourceSessionID: session + "-source",
			Origin:          "claude-code",
			Kind:            "coding",
		},
	}); err != nil {
		t.Fatalf("Ingest(session_start) error = %v", err)
	}

	callID := "call-dup"
	dupEvent := Event{
		Type:       EventToolUse,
		Session:    Session{Session: session},
		Tool:       Tool{ToolCallID: &callID, ToolName: "bash", Input: "ls -la"},
		ProducerID: session + "-producer",
		EventID:    "evt-1",
		ClientSeq:  1,
	}

	if err := Ingest(ctx, pool, dupEvent); err != nil {
		t.Fatalf("Ingest(tool_use) first delivery error = %v", err)
	}
	if err := Ingest(ctx, pool, dupEvent); !errors.Is(err, ErrDuplicateEvent) {
		t.Fatalf("Ingest(tool_use) duplicate delivery error = %v, want ErrDuplicateEvent (idempotent skip)", err)
	}

	got, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() error = %v", err)
	}
	if got.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want 1 (duplicate event_id must not be reprocessed)", got.ToolCallCount)
	}

	var receiptCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM telemetry_receipts WHERE producer_id = $1 AND event_id = $2`,
		dupEvent.ProducerID, dupEvent.EventID,
	).Scan(&receiptCount); err != nil {
		t.Fatalf("count telemetry_receipts: %v", err)
	}
	if receiptCount != 1 {
		t.Fatalf("telemetry_receipts row count = %d, want 1", receiptCount)
	}
}

func TestIngest_ReplayLeavesUsageToolAndMessageTotalsUnchanged_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := "telemetry-ingest-replay-totals"
	producerID := session + "-producer"
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM agent_sessions WHERE session = $1`, session); err != nil {
			t.Errorf("cleanup delete agent_sessions %q: %v", session, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM telemetry_receipts WHERE producer_id = $1`, producerID); err != nil {
			t.Errorf("cleanup delete telemetry_receipts: %v", err)
		}
	})

	callID := "call-replay"
	events := []Event{
		{
			Type: EventToolUse, Session: Session{Session: session},
			Tool:       Tool{ToolCallID: &callID, ToolName: "bash", Input: "true"},
			ProducerID: producerID, EventID: "tool-1", ClientSeq: 1,
		},
		{
			Type: EventMessageEnd, Session: Session{Session: session},
			Usage:      Usage{InputTokens: 11, OutputTokens: 7, CacheReadTokens: 3, CacheWriteTokens: 2, CostUSD: 0.25},
			ProducerID: producerID, EventID: "turn-1", ClientSeq: 2,
		},
		{
			Type: EventFinalMessage, Session: Session{Session: session},
			Message:    Message{Role: "assistant", Content: "done"},
			ProducerID: producerID, EventID: "message-1", ClientSeq: 3,
		},
	}

	// No session_start is sent: the first activity must create the fallback
	// session, and replaying the complete spool must be side-effect free.
	for _, ev := range events {
		if err := Ingest(ctx, pool, ev); err != nil {
			t.Fatalf("Ingest(%s) first delivery error = %v", ev.EventID, err)
		}
	}
	for _, ev := range events {
		if err := Ingest(ctx, pool, ev); !errors.Is(err, ErrDuplicateEvent) {
			t.Fatalf("Ingest(%s) replay error = %v, want ErrDuplicateEvent", ev.EventID, err)
		}
	}

	got, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() error = %v", err)
	}
	if got.InputTokens != 11 || got.OutputTokens != 7 || got.CacheReadTokens != 3 || got.CacheWriteTokens != 2 || got.CostUSD != 0.25 {
		t.Fatalf("usage totals after replay = %+v, want one usage event", got)
	}
	if got.ToolCallCount != 1 || got.TurnCount != 1 {
		t.Fatalf("counts after replay = tools:%d turns:%d, want 1 and 1", got.ToolCallCount, got.TurnCount)
	}

	var tools, turns, messages int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_tool_calls WHERE agent_session_id = $1`, got.ID).Scan(&tools); err != nil {
		t.Fatalf("count agent_tool_calls: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_turns WHERE agent_session_id = $1`, got.ID).Scan(&turns); err != nil {
		t.Fatalf("count agent_turns: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_messages WHERE agent_session_id = $1`, got.ID).Scan(&messages); err != nil {
		t.Fatalf("count agent_messages: %v", err)
	}
	if tools != 1 || turns != 1 || messages != 1 {
		t.Fatalf("rows after replay = tools:%d turns:%d messages:%d, want 1 each", tools, turns, messages)
	}
}

// TestIngest_UnknownSession_Postgres verifies that an activity event
// (e.g. tool_use) for a session that was never created via session_start
// still persists successfully: Ingest ensures a minimal agent_sessions
// row from whatever metadata the activity event itself carries, so a
// lost or delayed session_start does not drop the activity.
func TestIngest_UnknownSession_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := "telemetry-ingest-does-not-exist"
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM agent_sessions WHERE session = $1`, session); err != nil {
			t.Errorf("cleanup delete agent_sessions %q: %v", session, err)
		}
	})

	err := Ingest(ctx, pool, Event{
		Type:    EventToolUse,
		Session: Session{Session: session},
		Tool:    Tool{ToolName: "bash"},
	})
	if err != nil {
		t.Fatalf("Ingest(tool_use) for unknown session error = %v, want nil", err)
	}

	got, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() error = %v", err)
	}
	if got.SourceSessionID != session {
		t.Fatalf("SourceSessionID = %q, want fallback to session %q", got.SourceSessionID, session)
	}
	if got.Origin != "unknown" {
		t.Fatalf("Origin = %q, want fallback %q", got.Origin, "unknown")
	}
	if got.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want 1", got.ToolCallCount)
	}
}

// TestIngest_ActivityThenSessionStart_UpgradesFallbackAttribution_Postgres
// verifies that when an activity event (tool_use) arrives before its
// session_start and creates a fallback agent_sessions row with placeholder
// attribution (source_session_id == session, origin == "unknown"), a
// later, legitimate session_start event upgrades that placeholder
// attribution instead of leaving it stuck forever.
func TestIngest_ActivityThenSessionStart_UpgradesFallbackAttribution_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := "telemetry-ingest-activity-then-start"
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM agent_sessions WHERE session = $1`, session); err != nil {
			t.Errorf("cleanup delete agent_sessions %q: %v", session, err)
		}
	})

	// Activity arrives first (session_start lost or delayed): a fallback
	// row is created with placeholder attribution.
	if err := Ingest(ctx, pool, Event{
		Type:    EventToolUse,
		Session: Session{Session: session},
		Tool:    Tool{ToolName: "bash"},
	}); err != nil {
		t.Fatalf("Ingest(tool_use) error = %v", err)
	}

	fallback, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() error = %v", err)
	}
	if fallback.SourceSessionID != session {
		t.Fatalf("SourceSessionID = %q, want placeholder fallback %q", fallback.SourceSessionID, session)
	}
	if fallback.Origin != "unknown" {
		t.Fatalf("Origin = %q, want placeholder %q", fallback.Origin, "unknown")
	}

	// The authoritative session_start now arrives with real attribution.
	if err := Ingest(ctx, pool, Event{
		Type: EventSessionStart,
		Session: Session{
			Session:         session,
			SourceSessionID: session + "-source",
			Origin:          "claude-code",
			Kind:            "coding",
		},
	}); err != nil {
		t.Fatalf("Ingest(session_start) error = %v", err)
	}

	upgraded, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() after session_start error = %v", err)
	}
	if upgraded.ID != fallback.ID {
		t.Fatalf("session_start created a new row: ID = %d, want unchanged %d", upgraded.ID, fallback.ID)
	}
	if upgraded.SourceSessionID != session+"-source" {
		t.Fatalf("SourceSessionID = %q, want upgraded to %q", upgraded.SourceSessionID, session+"-source")
	}
	if upgraded.Origin != "claude-code" {
		t.Fatalf("Origin = %q, want upgraded to %q", upgraded.Origin, "claude-code")
	}

	// A subsequent activity event with no attribution of its own must not
	// regress the now-real attribution back to a placeholder.
	if err := Ingest(ctx, pool, Event{
		Type:    EventToolUse,
		Session: Session{Session: session},
		Tool:    Tool{ToolName: "grep"},
	}); err != nil {
		t.Fatalf("Ingest(tool_use) after upgrade error = %v", err)
	}

	final, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() after second activity error = %v", err)
	}
	if final.SourceSessionID != session+"-source" {
		t.Fatalf("SourceSessionID = %q, want unchanged %q", final.SourceSessionID, session+"-source")
	}
	if final.Origin != "claude-code" {
		t.Fatalf("Origin = %q, want unchanged %q", final.Origin, "claude-code")
	}
}

// TestIngest_FailedSideEffectRollsBackReceipt_Postgres forces the
// event-specific side effect (the tool_use write, which requires an
// existing agent session) to fail after the receipt has already been
// claimed. It asserts the receipt claim is rolled back along with the
// failed write, so a retry of the identical (producer_id, event_id) is
// not treated as an already-seen duplicate and reprocesses the event
// exactly once.
func TestIngest_FailedSideEffectRollsBackReceipt_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := "telemetry-ingest-rollback"
	producerID := session + "-producer"
	eventID := "evt-1"
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM agent_sessions WHERE session = $1`, session); err != nil {
			t.Errorf("cleanup delete agent_sessions %q: %v", session, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM telemetry_receipts WHERE producer_id = $1`, producerID); err != nil {
			t.Errorf("cleanup delete telemetry_receipts: %v", err)
		}
	})

	callID := "call-rollback"
	// tool_use now ensures a fallback session, so it no longer fails for
	// an unknown session. Force the side effect to fail with tool_result,
	// which requires a matching in-flight agent_tool_calls row: with no
	// prior tool_use, AttachAgentToolResult finds nothing to update.
	ev := Event{
		Type:       EventToolResult,
		Session:    Session{Session: session},
		Tool:       Tool{ToolCallID: &callID, ToolName: "bash", Result: "ok"},
		ProducerID: producerID,
		EventID:    eventID,
		ClientSeq:  1,
	}

	if err := Ingest(ctx, pool, ev); err == nil {
		t.Fatal("Ingest(tool_result) with no matching tool_use error = nil, want error")
	}

	var receiptCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM telemetry_receipts WHERE producer_id = $1 AND event_id = $2`,
		producerID, eventID,
	).Scan(&receiptCount); err != nil {
		t.Fatalf("count telemetry_receipts: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("telemetry_receipts row count after failed delivery = %d, want 0 (receipt must roll back with the failed side effect)", receiptCount)
	}

	if _, err := db.GetAgentSessionBySession(ctx, pool, session); err != db.ErrNotFound {
		t.Fatalf("GetAgentSessionBySession() after failed delivery error = %v, want ErrNotFound (fallback session creation must also roll back)", err)
	}

	// Record the tool_use the result is attached to, then retry the
	// identical tool_result event delivery.
	if err := Ingest(ctx, pool, Event{
		Type:    EventToolUse,
		Session: Session{Session: session},
		Tool:    Tool{ToolCallID: &callID, ToolName: "bash", Input: "ls -la"},
	}); err != nil {
		t.Fatalf("Ingest(tool_use) error = %v", err)
	}

	if err := Ingest(ctx, pool, ev); err != nil {
		t.Fatalf("Ingest(tool_result) retry after tool_use exists error = %v, want nil", err)
	}

	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM telemetry_receipts WHERE producer_id = $1 AND event_id = $2`,
		producerID, eventID,
	).Scan(&receiptCount); err != nil {
		t.Fatalf("count telemetry_receipts: %v", err)
	}
	if receiptCount != 1 {
		t.Fatalf("telemetry_receipts row count after successful retry = %d, want 1", receiptCount)
	}

	got, err := db.GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() error = %v", err)
	}
	if got.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want exactly 1 rollup from the successful tool_use", got.ToolCallCount)
	}

	var toolCallRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM agent_tool_calls WHERE agent_session_id = $1`,
		got.ID,
	).Scan(&toolCallRows); err != nil {
		t.Fatalf("count agent_tool_calls: %v", err)
	}
	if toolCallRows != 1 {
		t.Fatalf("agent_tool_calls row count = %d, want exactly 1", toolCallRows)
	}
}
