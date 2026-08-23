package webui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }
func i64p(n int64) *int64   { return &n }

// TestBuildPhaseTelemetryViewEmpty covers that a phase with no attached
// agent sessions produces a view model with zeroed totals and no
// sessions, and that the template renders the empty state without
// panicking.
func TestBuildPhaseTelemetryViewEmpty(t *testing.T) {
	ph := db.Phase{ID: 7, Name: "implement"}
	view := buildPhaseTelemetryView(ph, nil, nil, nil, nil)

	if view.TotalSessions != 0 {
		t.Fatalf("TotalSessions = %d, want 0", view.TotalSessions)
	}
	if len(view.Sessions) != 0 {
		t.Fatalf("Sessions = %v, want empty", view.Sessions)
	}
	if view.TotalTurns != 0 || view.TotalToolCalls != 0 || view.TotalMessages != 0 {
		t.Fatalf("expected zeroed counts, got %+v", view)
	}

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "phases.telemetry", view); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "No agent telemetry recorded for this phase yet.") {
		t.Fatalf("expected empty-state message, got:\n%s", out)
	}
	if !strings.Contains(out, "Phase #7 implement") {
		t.Fatalf("expected phase header, got:\n%s", out)
	}
}

// TestBuildPhaseTelemetryViewPopulated covers that turns, tool calls and
// messages for a session are merged into a single sequence-ordered
// timeline, that session and phase totals are aggregated correctly, and
// that the rendered template surfaces truncation/error/duration metadata
// while escaping payload content.
func TestBuildPhaseTelemetryViewPopulated(t *testing.T) {
	started := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ended := started.Add(90 * time.Second)
	sess := db.AgentSession{
		ID:               1,
		Session:          "sess-abc",
		Origin:           "cerberus",
		Kind:             "primary",
		Model:            strp("claude-x"),
		StartedAt:        started,
		EndedAt:          &ended,
		InputTokens:      100,
		OutputTokens:     50,
		CacheReadTokens:  10,
		CacheWriteTokens: 5,
		CostUSD:          0.1234,
	}

	turns := []db.AgentTurn{
		{AgentSessionID: 1, Seq: 2, InputTokens: 100, OutputTokens: 50, CostUSD: 0.1234, Ts: started.Add(2 * time.Second)},
	}
	toolCalls := []db.AgentToolCall{
		{
			AgentSessionID:      1,
			Seq:                 1,
			ToolName:            "bash",
			ToolInput:           strp("<script>rm -rf /</script>"),
			ToolResult:          strp("done"),
			ToolResultTruncated: true,
			IsError:             boolp(true),
			DurationMs:          i64p(42),
			CreatedAt:           started.Add(1 * time.Second),
		},
	}
	messages := []db.AgentMessage{
		{AgentSessionID: 1, Seq: 3, Role: "assistant", Content: strp("all good"), CreatedAt: started.Add(3 * time.Second)},
	}

	ph := db.Phase{ID: 9, Name: "review"}
	view := buildPhaseTelemetryView(
		ph,
		[]db.AgentSession{sess},
		map[int64][]db.AgentTurn{1: turns},
		map[int64][]db.AgentToolCall{1: toolCalls},
		map[int64][]db.AgentMessage{1: messages},
	)

	if view.TotalSessions != 1 {
		t.Fatalf("TotalSessions = %d, want 1", view.TotalSessions)
	}
	if view.TotalTurns != 1 || view.TotalToolCalls != 1 || view.TotalMessages != 1 {
		t.Fatalf("expected one of each event kind, got %+v", view)
	}
	if view.TotalErrors != 1 {
		t.Fatalf("TotalErrors = %d, want 1", view.TotalErrors)
	}
	if view.TotalTruncated != 1 {
		t.Fatalf("TotalTruncated = %d, want 1", view.TotalTruncated)
	}
	if view.TotalInputTokens != 100 || view.TotalOutputTokens != 50 {
		t.Fatalf("unexpected token totals: %+v", view)
	}

	if len(view.Sessions) != 1 {
		t.Fatalf("expected 1 session view, got %d", len(view.Sessions))
	}
	sv := view.Sessions[0]
	if len(sv.Events) != 3 {
		t.Fatalf("expected 3 merged events, got %d", len(sv.Events))
	}
	// Sequence order must be tool_call(1), turn(2), message(3) regardless
	// of insertion order.
	wantKinds := []string{"tool_call", "turn", "message"}
	for i, ev := range sv.Events {
		if ev.Kind != wantKinds[i] {
			t.Fatalf("event[%d].Kind = %q, want %q (order: %+v)", i, ev.Kind, wantKinds[i], sv.Events)
		}
	}
	if sv.Duration == "" || sv.Duration == "running" {
		t.Fatalf("expected a finite duration for an ended session, got %q", sv.Duration)
	}

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "phases.telemetry", view); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"sess-abc",
		"claude-x",
		"· truncated",
		"· error",
		"· 42ms",
		"&lt;script&gt;",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in rendered output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<script>rm -rf /</script>") {
		t.Fatalf("expected tool input to be escaped, got:\n%s", out)
	}
}

// TestHandleUIPhaseTelemetryFragmentRouteRegistered covers that the
// telemetry fragment path resolves through the existing /phases/ route
// without requiring a database connection.
func TestHandleUIPhaseTelemetryFragmentRouteRegistered(t *testing.T) {
	mux, _ := newTestMux(t)
	if pattern := registeredPattern(mux, "GET", "/phases/1/telemetry/fragment"); pattern == "" {
		t.Fatalf("expected a route registered for telemetry fragment path, got none")
	}
}
