package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/telemetry"
)

func TestTelemetryEventDTO_ToTelemetryEvent_SessionStart(t *testing.T) {
	dto := telemetryEventDTO{
		Type:            "session_start",
		Session:         "sess-1",
		SourceSessionID: "src-1",
		Origin:          "claude-code",
		Kind:            "coder",
		Timestamp:       "2024-01-02T03:04:05Z",
	}

	ev, err := dto.toTelemetryEvent()
	if err != nil {
		t.Fatalf("toTelemetryEvent() error = %v", err)
	}
	if ev.Type != telemetry.EventSessionStart {
		t.Fatalf("Type = %v, want %v", ev.Type, telemetry.EventSessionStart)
	}
	if ev.Session.Session != "sess-1" || ev.Session.SourceSessionID != "src-1" || ev.Session.Origin != "claude-code" {
		t.Fatalf("Session = %+v, unexpected", ev.Session)
	}
	if ev.Session.Kind != telemetry.SessionKind("coder") {
		t.Fatalf("Kind = %v, want coder", ev.Session.Kind)
	}
	if ev.Timestamp.IsZero() {
		t.Fatal("Timestamp = zero, want parsed value")
	}
}

func TestTelemetryEventDTO_ToTelemetryEvent_ToolUse(t *testing.T) {
	callID := "call-1"
	dto := telemetryEventDTO{
		Type:       "tool_use",
		Session:    "sess-1",
		ToolCallID: &callID,
		ToolName:   "bash",
		ToolInput:  "ls -la",
	}

	ev, err := dto.toTelemetryEvent()
	if err != nil {
		t.Fatalf("toTelemetryEvent() error = %v", err)
	}
	if ev.Type != telemetry.EventToolUse {
		t.Fatalf("Type = %v, want %v", ev.Type, telemetry.EventToolUse)
	}
	if ev.Tool.ToolName != "bash" || ev.Tool.Input != "ls -la" {
		t.Fatalf("Tool = %+v, unexpected", ev.Tool)
	}
	if ev.Tool.ToolCallID == nil || *ev.Tool.ToolCallID != "call-1" {
		t.Fatalf("ToolCallID = %v, want call-1", ev.Tool.ToolCallID)
	}
}

func TestTelemetryEventDTO_ToTelemetryEvent_ToolResult(t *testing.T) {
	isError := true
	durationMs := int64(42)
	dto := telemetryEventDTO{
		Type:       "tool_result",
		Session:    "sess-1",
		ToolName:   "bash",
		ToolResult: "boom",
		IsError:    &isError,
		DurationMs: &durationMs,
	}

	ev, err := dto.toTelemetryEvent()
	if err != nil {
		t.Fatalf("toTelemetryEvent() error = %v", err)
	}
	if ev.Type != telemetry.EventToolResult {
		t.Fatalf("Type = %v, want %v", ev.Type, telemetry.EventToolResult)
	}
	if ev.Tool.Result != "boom" {
		t.Fatalf("Result = %q, want boom", ev.Tool.Result)
	}
	if ev.Tool.IsError == nil || !*ev.Tool.IsError {
		t.Fatalf("IsError = %v, want pointer to true", ev.Tool.IsError)
	}
	if ev.Tool.DurationMs == nil || *ev.Tool.DurationMs != 42 {
		t.Fatalf("DurationMs = %v, want pointer to 42", ev.Tool.DurationMs)
	}
}

func TestTelemetryEventDTO_ToTelemetryEvent_FinalMessage(t *testing.T) {
	dto := telemetryEventDTO{
		Type:    "final_message",
		Session: "sess-1",
		Role:    "assistant",
		Content: "hello",
	}

	ev, err := dto.toTelemetryEvent()
	if err != nil {
		t.Fatalf("toTelemetryEvent() error = %v", err)
	}
	if ev.Type != telemetry.EventFinalMessage {
		t.Fatalf("Type = %v, want %v", ev.Type, telemetry.EventFinalMessage)
	}
	if ev.Message.Role != "assistant" || ev.Message.Content != "hello" {
		t.Fatalf("Message = %+v, unexpected", ev.Message)
	}
}

func TestTelemetryEventDTO_ToTelemetryEvent_Usage(t *testing.T) {
	dto := telemetryEventDTO{
		Type:    "message_end",
		Session: "sess-1",
		Usage: &telemetryUsageDTO{
			InputTokens:      10,
			OutputTokens:     5,
			CacheReadTokens:  1,
			CacheWriteTokens: 2,
			CostUSD:          0.03,
		},
	}

	ev, err := dto.toTelemetryEvent()
	if err != nil {
		t.Fatalf("toTelemetryEvent() error = %v", err)
	}
	want := telemetry.Usage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 1, CacheWriteTokens: 2, CostUSD: 0.03}
	if ev.Usage != want {
		t.Fatalf("Usage = %+v, want %+v", ev.Usage, want)
	}
}

func TestTelemetryEventDTO_ToTelemetryEvent_MissingSession(t *testing.T) {
	dto := telemetryEventDTO{Type: "session_start"}
	if _, err := dto.toTelemetryEvent(); err == nil {
		t.Fatal("toTelemetryEvent() error = nil, want error for missing session")
	}
}

func TestTelemetryEventDTO_ToTelemetryEvent_MissingType(t *testing.T) {
	dto := telemetryEventDTO{Session: "sess-1"}
	if _, err := dto.toTelemetryEvent(); err == nil {
		t.Fatal("toTelemetryEvent() error = nil, want error for missing type")
	}
}

func TestTelemetryEventDTO_ToTelemetryEvent_InvalidTimestamp(t *testing.T) {
	dto := telemetryEventDTO{Type: "session_start", Session: "sess-1", Timestamp: "not-a-timestamp"}
	if _, err := dto.toTelemetryEvent(); err == nil {
		t.Fatal("toTelemetryEvent() error = nil, want error for invalid timestamp")
	}
}

func TestParseTelemetryTimestamp(t *testing.T) {
	if got, err := parseTelemetryTimestamp(""); err != nil || !got.IsZero() {
		t.Fatalf("parseTelemetryTimestamp(\"\") = (%v, %v), want (zero, nil)", got, err)
	}
	if _, err := parseTelemetryTimestamp("nope"); err == nil {
		t.Fatal("parseTelemetryTimestamp(invalid) error = nil, want error")
	}
	got, err := parseTelemetryTimestamp("2024-01-02T03:04:05Z")
	if err != nil {
		t.Fatalf("parseTelemetryTimestamp(valid) error = %v", err)
	}
	if got.IsZero() {
		t.Fatal("parseTelemetryTimestamp(valid) = zero, want parsed value")
	}
}

func postTelemetryEvent(t *testing.T, s *Server, ingest func(context.Context, *pgxpool.Pool, telemetry.Event) error, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/telemetry/events", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleTelemetryEventsWith(rec, req, ingest)
	return rec
}

func TestHandleTelemetryEventsWith_Success(t *testing.T) {
	s := &Server{}
	var gotEvent telemetry.Event
	called := false
	rec := postTelemetryEvent(t, s, func(ctx context.Context, pool *pgxpool.Pool, ev telemetry.Event) error {
		called = true
		gotEvent = ev
		return nil
	}, `{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"ls"}`)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if !called {
		t.Fatal("expected ingest to be called")
	}
	if gotEvent.Type != telemetry.EventToolUse || gotEvent.Tool.ToolName != "bash" {
		t.Fatalf("gotEvent = %+v, unexpected", gotEvent)
	}
}

func TestHandleTelemetryEventsWith_MethodNotAllowed(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/telemetry/events", nil)
	rec := httptest.NewRecorder()
	s.handleTelemetryEventsWith(rec, req, func(context.Context, *pgxpool.Pool, telemetry.Event) error {
		t.Fatal("ingest should not be called")
		return nil
	})
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleTelemetryEventsWith_InvalidJSON(t *testing.T) {
	s := &Server{}
	rec := postTelemetryEvent(t, s, func(context.Context, *pgxpool.Pool, telemetry.Event) error {
		t.Fatal("ingest should not be called")
		return nil
	}, `{not-json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleTelemetryEventsWith_MissingRequiredFields(t *testing.T) {
	s := &Server{}
	rec := postTelemetryEvent(t, s, func(context.Context, *pgxpool.Pool, telemetry.Event) error {
		t.Fatal("ingest should not be called")
		return nil
	}, `{"type":"tool_use"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleTelemetryEventsWith_ValidationErrorFromIngestIsBadRequest(t *testing.T) {
	s := &Server{}
	rec := postTelemetryEvent(t, s, func(context.Context, *pgxpool.Pool, telemetry.Event) error {
		return errors.New("telemetry: tool_name is required for tool_use")
	}, `{"type":"tool_use","session":"sess-1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleTelemetryEventsWith_OversizedBodyRejected(t *testing.T) {
	s := &Server{}
	oversized := `{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"` + strings.Repeat("a", int(telemetry.MaxContentBytes)+1) + `"}`
	rec := postTelemetryEvent(t, s, func(context.Context, *pgxpool.Pool, telemetry.Event) error {
		t.Fatal("ingest should not be called")
		return nil
	}, oversized)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleTelemetryEventsWith_DeltaEventRejectedByIngest(t *testing.T) {
	s := &Server{}
	rec := postTelemetryEvent(t, s, telemetry.Ingest, `{"type":"delta","session":"sess-1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleTelemetryEventsWith_InternalErrorFromIngestIs500(t *testing.T) {
	s := &Server{}
	rec := postTelemetryEvent(t, s, func(context.Context, *pgxpool.Pool, telemetry.Event) error {
		return errors.New("db: connection refused")
	}, `{"type":"tool_use","session":"sess-1","tool_name":"bash"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
