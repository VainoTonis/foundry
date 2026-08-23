package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/telemetry"
)

func TestBuildCerberusTelemetryEvent_SessionStart(t *testing.T) {
	raw := []byte(`{"type":"session_start","session":"sess-1","ts":"2024-01-02T03:04:05Z","session_id":"src-1"}`)
	fields := extractCerberusFields(raw)

	ev, ok := buildCerberusTelemetryEvent("session_start", "sess-1", fields)
	if !ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = false, want true")
	}
	if ev.Type != telemetry.EventSessionStart {
		t.Fatalf("Type = %v, want %v", ev.Type, telemetry.EventSessionStart)
	}
	if ev.Session.Session != "sess-1" || ev.Session.SourceSessionID != "src-1" {
		t.Fatalf("Session = %+v, want session=sess-1 source_session_id=src-1", ev.Session)
	}
	if ev.Session.Origin != "cerberus" {
		t.Fatalf("Origin = %q, want %q", ev.Session.Origin, "cerberus")
	}
	if ev.Session.Kind != telemetry.SessionKindUnknown {
		t.Fatalf("Kind = %q, want %q", ev.Session.Kind, telemetry.SessionKindUnknown)
	}
	want, _ := time.Parse(time.RFC3339, "2024-01-02T03:04:05Z")
	if !ev.Timestamp.Equal(want) {
		t.Fatalf("Timestamp = %v, want %v", ev.Timestamp, want)
	}
}

func TestBuildCerberusTelemetryEvent_SessionStartMissingSessionID(t *testing.T) {
	raw := []byte(`{"type":"session_start","session":"sess-1","ts":"2024-01-02T03:04:05Z"}`)
	fields := extractCerberusFields(raw)

	if _, ok := buildCerberusTelemetryEvent("session_start", "sess-1", fields); ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = true, want false without session_id")
	}
}

func TestBuildCerberusTelemetryEvent_ToolUseObjectInput(t *testing.T) {
	raw := []byte(`{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":{"command":"ls"},"tool_call_id":"call-1"}`)
	fields := extractCerberusFields(raw)

	ev, ok := buildCerberusTelemetryEvent("tool_use", "sess-1", fields)
	if !ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = false, want true")
	}
	if ev.Type != telemetry.EventToolUse {
		t.Fatalf("Type = %v, want %v", ev.Type, telemetry.EventToolUse)
	}
	if ev.Tool.ToolName != "bash" {
		t.Fatalf("ToolName = %q, want %q", ev.Tool.ToolName, "bash")
	}
	if ev.Tool.ToolCallID == nil || *ev.Tool.ToolCallID != "call-1" {
		t.Fatalf("ToolCallID = %v, want call-1", ev.Tool.ToolCallID)
	}
	if ev.Tool.Input != `{"command":"ls"}` {
		t.Fatalf("Input = %q, want %q", ev.Tool.Input, `{"command":"ls"}`)
	}
}

func TestBuildCerberusTelemetryEvent_ToolUseStringInput(t *testing.T) {
	raw := []byte(`{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"ls -la","tool_call_id":"call-1"}`)
	fields := extractCerberusFields(raw)

	ev, ok := buildCerberusTelemetryEvent("tool_use", "sess-1", fields)
	if !ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = false, want true")
	}
	if ev.Tool.Input != "ls -la" {
		t.Fatalf("Input = %q, want %q", ev.Tool.Input, "ls -la")
	}
}

func TestBuildCerberusTelemetryEvent_ToolUseMissingToolName(t *testing.T) {
	raw := []byte(`{"type":"tool_use","session":"sess-1","tool_input":"ls -la"}`)
	fields := extractCerberusFields(raw)

	if _, ok := buildCerberusTelemetryEvent("tool_use", "sess-1", fields); ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = true, want false without tool_name")
	}
}

func TestBuildCerberusTelemetryEvent_ToolResult(t *testing.T) {
	raw := []byte(`{"type":"tool_result","session":"sess-1","tool_name":"bash","content":"total 0","tool_call_id":"call-1","is_error":false,"duration_ms":42}`)
	fields := extractCerberusFields(raw)

	ev, ok := buildCerberusTelemetryEvent("tool_result", "sess-1", fields)
	if !ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = false, want true")
	}
	if ev.Type != telemetry.EventToolResult {
		t.Fatalf("Type = %v, want %v", ev.Type, telemetry.EventToolResult)
	}
	if ev.Tool.Result != "total 0" {
		t.Fatalf("Result = %q, want %q", ev.Tool.Result, "total 0")
	}
	if ev.Tool.ToolCallID == nil || *ev.Tool.ToolCallID != "call-1" {
		t.Fatalf("ToolCallID = %v, want call-1", ev.Tool.ToolCallID)
	}
	if ev.Tool.IsError == nil || *ev.Tool.IsError {
		t.Fatalf("IsError = %v, want pointer to false", ev.Tool.IsError)
	}
	if ev.Tool.DurationMs == nil || *ev.Tool.DurationMs != 42 {
		t.Fatalf("DurationMs = %v, want pointer to 42", ev.Tool.DurationMs)
	}
}

func TestBuildCerberusTelemetryEvent_MessageEnd(t *testing.T) {
	raw := []byte(`{"type":"message_end","session":"sess-1","usage":{"input_tokens":10,"output_tokens":5,"cache_read_tokens":1,"cache_write_tokens":2,"cost_usd":0.03}}`)
	fields := extractCerberusFields(raw)

	ev, ok := buildCerberusTelemetryEvent("message_end", "sess-1", fields)
	if !ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = false, want true")
	}
	want := telemetry.Usage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 1, CacheWriteTokens: 2, CostUSD: 0.03}
	if ev.Usage != want {
		t.Fatalf("Usage = %+v, want %+v", ev.Usage, want)
	}
}

func TestBuildCerberusTelemetryEvent_MessageEndMissingUsage(t *testing.T) {
	raw := []byte(`{"type":"message_end","session":"sess-1"}`)
	fields := extractCerberusFields(raw)

	ev, ok := buildCerberusTelemetryEvent("message_end", "sess-1", fields)
	if !ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = false, want true")
	}
	if ev.Usage != (telemetry.Usage{}) {
		t.Fatalf("Usage = %+v, want zero value", ev.Usage)
	}
}

func TestBuildCerberusTelemetryEvent_UnhandledType(t *testing.T) {
	raw := []byte(`{"type":"raw","session":"sess-1"}`)
	fields := extractCerberusFields(raw)

	if _, ok := buildCerberusTelemetryEvent("raw", "sess-1", fields); ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = true, want false for unhandled type")
	}
}

func TestExtractCerberusFields_NestedPayloadCompatibility(t *testing.T) {
	raw := []byte(`{"type":"tool_use","session":"sess-1","payload":{"tool_name":"bash","tool_input":"ls","tool_call_id":"call-9"}}`)
	fields := extractCerberusFields(raw)

	if fields.ToolName != "bash" {
		t.Fatalf("ToolName = %q, want %q", fields.ToolName, "bash")
	}
	if fields.ToolCallID != "call-9" {
		t.Fatalf("ToolCallID = %q, want %q", fields.ToolCallID, "call-9")
	}
	if cerberusRawToString(fields.ToolInput) != "ls" {
		t.Fatalf("ToolInput = %q, want %q", cerberusRawToString(fields.ToolInput), "ls")
	}
}

func TestExtractCerberusFields_TopLevelTakesPrecedenceOverNested(t *testing.T) {
	raw := []byte(`{"type":"tool_use","session":"sess-1","tool_name":"top","payload":{"tool_name":"nested"}}`)
	fields := extractCerberusFields(raw)

	if fields.ToolName != "top" {
		t.Fatalf("ToolName = %q, want %q", fields.ToolName, "top")
	}
}

func TestCerberusRawToString(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"json string", `"hello"`, "hello"},
		{"json object", `{"a":1}`, `{"a":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cerberusRawToString([]byte(tc.raw))
			if got != tc.want {
				t.Fatalf("cerberusRawToString(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestOptionalCerberusString(t *testing.T) {
	if got := optionalCerberusString(""); got != nil {
		t.Fatalf("optionalCerberusString(\"\") = %v, want nil", got)
	}
	got := optionalCerberusString("x")
	if got == nil || *got != "x" {
		t.Fatalf("optionalCerberusString(\"x\") = %v, want pointer to \"x\"", got)
	}
}

func TestParseCerberusTimestamp(t *testing.T) {
	if got := parseCerberusTimestamp(""); !got.IsZero() {
		t.Fatalf("parseCerberusTimestamp(\"\") = %v, want zero time", got)
	}
	if got := parseCerberusTimestamp("not-a-timestamp"); !got.IsZero() {
		t.Fatalf("parseCerberusTimestamp(invalid) = %v, want zero time", got)
	}
	want, _ := time.Parse(time.RFC3339, "2024-01-02T03:04:05Z")
	if got := parseCerberusTimestamp("2024-01-02T03:04:05Z"); !got.Equal(want) {
		t.Fatalf("parseCerberusTimestamp(valid) = %v, want %v", got, want)
	}
}

func TestIngestCerberusTelemetryWith_SwallowsInjectedError(t *testing.T) {
	s := &Server{}
	raw := []byte(`{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"ls"}`)
	evt := compactCerberusEvent{Type: "tool_use", Session: "sess-1"}

	called := false
	s.ingestCerberusTelemetryWith(context.Background(), raw, evt, func(ctx context.Context, pool *pgxpool.Pool, ev telemetry.Event) error {
		called = true
		return errors.New("boom")
	})

	if !called {
		t.Fatal("expected injected ingest function to be called")
	}
}

func TestIngestCerberusTelemetryWith_SkipsUnbuildableEvent(t *testing.T) {
	s := &Server{}
	raw := []byte(`{"type":"raw","session":"sess-1"}`)
	evt := compactCerberusEvent{Type: "raw", Session: "sess-1"}

	called := false
	s.ingestCerberusTelemetryWith(context.Background(), raw, evt, func(ctx context.Context, pool *pgxpool.Pool, ev telemetry.Event) error {
		called = true
		return nil
	})

	if called {
		t.Fatal("expected injected ingest function not to be called for unbuildable event type")
	}
}

func TestCompactToolUsePayload_UpdateSpecFollowsCompactPayload(t *testing.T) {
	raw := []byte(`{"type":"tool_use","session":"sess-1","tool_name":"update_spec","tool_input":{"foo":"bar"}}`)

	payload, ok := compactToolUsePayload(raw)
	if !ok {
		t.Fatal("compactToolUsePayload() ok = false, want true for update_spec")
	}
	var decoded map[string]string
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded["tool_name"] != "update_spec" {
		t.Fatalf("tool_name = %q, want %q", decoded["tool_name"], "update_spec")
	}
	if decoded["tool_input"] != `{"foo":"bar"}` {
		t.Fatalf("tool_input = %q, want %q", decoded["tool_input"], `{"foo":"bar"}`)
	}
}

func TestManagedMessageEndCostDecision_Zero(t *testing.T) {
	phaseID, deltaUSD, ok := managedMessageEndCostDecision(42, nil, 0)
	if ok {
		t.Fatalf("managedMessageEndCostDecision() ok = true, want false for zero cost")
	}
	if phaseID != 0 || deltaUSD != 0 {
		t.Fatalf("managedMessageEndCostDecision() = (%d, %v), want (0, 0)", phaseID, deltaUSD)
	}
}

func TestManagedMessageEndCostDecision_Nonzero(t *testing.T) {
	phaseID, deltaUSD, ok := managedMessageEndCostDecision(42, nil, 0.03)
	if !ok {
		t.Fatal("managedMessageEndCostDecision() ok = false, want true for nonzero cost")
	}
	if phaseID != 42 || deltaUSD != 0.03 {
		t.Fatalf("managedMessageEndCostDecision() = (%d, %v), want (42, 0.03)", phaseID, deltaUSD)
	}
}

func TestManagedMessageEndCostDecision_PhaseError(t *testing.T) {
	phaseID, deltaUSD, ok := managedMessageEndCostDecision(42, errors.New("not found"), 0.03)
	if ok {
		t.Fatal("managedMessageEndCostDecision() ok = true, want false when phase lookup errored")
	}
	if phaseID != 0 || deltaUSD != 0 {
		t.Fatalf("managedMessageEndCostDecision() = (%d, %v), want (0, 0)", phaseID, deltaUSD)
	}
}

func TestCompactToolUsePayload_NonUpdateSpecDoesNotFollowCompactPayload(t *testing.T) {
	raw := []byte(`{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"ls"}`)

	if _, ok := compactToolUsePayload(raw); ok {
		t.Fatal("compactToolUsePayload() ok = true, want false for non-update_spec tool")
	}
}

func TestBuildCerberusTelemetryEvent_MessageEndUsageFilteringDecision(t *testing.T) {
	zeroRaw := []byte(`{"type":"message_end","session":"sess-1","usage":{"input_tokens":0,"output_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0,"cost_usd":0}}`)
	zeroFields := extractCerberusFields(zeroRaw)
	zeroEv, ok := buildCerberusTelemetryEvent("message_end", "sess-1", zeroFields)
	if !ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = false, want true for message_end")
	}
	if zeroEv.Usage != (telemetry.Usage{}) {
		t.Fatalf("Usage = %+v, want zero value so telemetry.Ingest filters the event out", zeroEv.Usage)
	}

	nonzeroRaw := []byte(`{"type":"message_end","session":"sess-1","usage":{"input_tokens":1,"output_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0,"cost_usd":0}}`)
	nonzeroFields := extractCerberusFields(nonzeroRaw)
	nonzeroEv, ok := buildCerberusTelemetryEvent("message_end", "sess-1", nonzeroFields)
	if !ok {
		t.Fatal("buildCerberusTelemetryEvent() ok = false, want true for message_end")
	}
	if nonzeroEv.Usage == (telemetry.Usage{}) {
		t.Fatal("Usage = zero value, want nonzero so telemetry.Ingest stores the event")
	}
}
