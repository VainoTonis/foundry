package httpserver

import (
	"context"
	"encoding/json"
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
	turnIndex := int64(4)
	sourceMessageID := "assistant-entry-4"
	dto := telemetryEventDTO{
		Type:            "final_message",
		Session:         "sess-1",
		Role:            "assistant",
		Content:         "hello",
		TurnIndex:       &turnIndex,
		SourceMessageID: &sourceMessageID,
		IsFinal:         true,
	}

	ev, err := dto.toTelemetryEvent()
	if err != nil {
		t.Fatalf("toTelemetryEvent() error = %v", err)
	}
	if ev.Type != telemetry.EventFinalMessage {
		t.Fatalf("Type = %v, want %v", ev.Type, telemetry.EventFinalMessage)
	}
	if ev.Message.Role != "assistant" || ev.Message.Content != "hello" || !ev.Message.IsFinal ||
		ev.Message.TurnIndex == nil || *ev.Message.TurnIndex != turnIndex || ev.Message.SourceMessageID == nil || *ev.Message.SourceMessageID != sourceMessageID {
		t.Fatalf("Message = %+v, unexpected", ev.Message)
	}
}

func TestTelemetryEventDTO_ToTelemetryEvent_Usage(t *testing.T) {
	turnIndex := int64(4)
	sourceMessageID := "assistant-entry-4"
	dto := telemetryEventDTO{
		Type:            "message_end",
		Session:         "sess-1",
		TurnIndex:       &turnIndex,
		SourceMessageID: &sourceMessageID,
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
	if ev.Turn.TurnIndex == nil || *ev.Turn.TurnIndex != turnIndex || ev.Turn.SourceMessageID == nil || *ev.Turn.SourceMessageID != sourceMessageID {
		t.Fatalf("Turn correlation = %+v, want turn %d/message %q", ev.Turn, turnIndex, sourceMessageID)
	}
}

func TestTelemetryEventDTO_CorrelationOnlyAllowedOnMessageEvents(t *testing.T) {
	for _, body := range []string{
		`{"type":"tool_use","session":"sess-1","tool_name":"bash","turn_index":1}`,
		`{"type":"session_end","session":"sess-1","source_message_id":"message-1"}`,
	} {
		rec := postTelemetryEvent(t, &Server{}, func(context.Context, *pgxpool.Pool, telemetry.Event) error {
			t.Fatal("ingest called for invalid correlation shape")
			return nil
		}, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want %d", body, rec.Code, http.StatusBadRequest)
		}
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

func TestTelemetryEventsBearerAuthentication(t *testing.T) {
	const token = "super-secret-token"
	s := &Server{telemetryToken: token}
	called := false
	handler := s.requireTelemetryAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong-secret", want: http.StatusUnauthorized},
		{name: "correct", header: "Bearer " + token, want: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodPost, "/api/telemetry/events", nil)
			req.Header.Set("Authorization", tc.header)
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if strings.Contains(rec.Body.String(), token) || strings.Contains(rec.Body.String(), "wrong-secret") {
				t.Fatalf("response leaked a bearer token: %q", rec.Body.String())
			}
			if called != (tc.want == http.StatusNoContent) {
				t.Fatalf("handler called = %v", called)
			}
		})
	}
}

func TestTelemetryEventsUnauthenticatedModeIsLoopbackOnly(t *testing.T) {
	s := &Server{telemetryAllowUnauthenticated: true}
	handler := s.requireTelemetryAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	for _, tc := range []struct {
		remote string
		want   int
	}{
		{remote: "127.0.0.1:1234", want: http.StatusNoContent},
		{remote: "[::1]:1234", want: http.StatusNoContent},
		{remote: "192.0.2.1:1234", want: http.StatusUnauthorized},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/telemetry/events", nil)
		req.RemoteAddr = tc.remote
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != tc.want {
			t.Errorf("remote %q: status = %d, want %d", tc.remote, rec.Code, tc.want)
		}
	}
}

func TestTelemetryAuthenticationDoesNotAffectUnrelatedRoutes(t *testing.T) {
	s := &Server{telemetryToken: "secret"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/telemetry/events", s.requireTelemetryAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/other", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/other", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("unrelated route status = %d, want %d", rec.Code, http.StatusTeapot)
	}
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

func TestHandleTelemetryEventsWith_AllowedTextIsTruncatedBeforeIngest(t *testing.T) {
	s := &Server{}
	oversizedInput := strings.Repeat("a", int(telemetry.MaxContentBytes)+1)
	body := `{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"` + oversizedInput + `"}`
	var gotEvent telemetry.Event
	rec := postTelemetryEvent(t, s, func(ctx context.Context, pool *pgxpool.Pool, ev telemetry.Event) error {
		gotEvent = ev
		return nil
	}, body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if len(gotEvent.Tool.Input) != telemetry.MaxContentBytes {
		t.Fatalf("Tool.Input length = %d, want %d", len(gotEvent.Tool.Input), telemetry.MaxContentBytes)
	}
}

func TestHandleTelemetryEventsWith_ValidationBoundary(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"unknown type", `{"type":"made_up","session":"sess-1"}`},
		{"unknown field", `{"type":"session_end","session":"sess-1","surprise":true}`},
		{"malformed usage shape", `{"type":"message_end","session":"sess-1","usage":{"input_tokens":"ten"}}`},
		{"usage on wrong event", `{"type":"session_end","session":"sess-1","usage":{"input_tokens":1}}`},
		{"negative usage", `{"type":"message_end","session":"sess-1","usage":{"input_tokens":-1}}`},
		{"oversized metadata", `{"type":"tool_use","session":"sess-1","tool_name":"` + strings.Repeat("x", telemetryMetadataMaxBytes+1) + `"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			rec := postTelemetryEvent(t, &Server{}, func(context.Context, *pgxpool.Pool, telemetry.Event) error {
				called = true
				return nil
			}, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if called {
				t.Fatal("ingest called for rejected event")
			}
		})
	}
}

func TestHandleTelemetryEventsWith_PrivacyAnnotationsPreserved(t *testing.T) {
	body := `{"type":"tool_use","session":"sess-1","tool_name":"bash","redacted":true,"omitted":true,"tool_input_redacted":true,"tool_input_omitted":true}`
	var got telemetry.Event
	rec := postTelemetryEvent(t, &Server{}, func(_ context.Context, _ *pgxpool.Pool, ev telemetry.Event) error {
		got = ev
		return nil
	}, body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if !got.Privacy.Redacted || !got.Privacy.Omitted || !got.Tool.InputRedacted || !got.Tool.InputOmitted {
		t.Fatalf("privacy annotations were not preserved: %+v %+v", got.Privacy, got.Tool)
	}
}

func TestHandleTelemetryEventsWith_RequestBodyOverCapRejected(t *testing.T) {
	s := &Server{}
	oversized := `{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"` + strings.Repeat("a", int(telemetryRequestMaxBytes)+1) + `"}`
	rec := postTelemetryEvent(t, s, func(context.Context, *pgxpool.Pool, telemetry.Event) error {
		t.Fatal("ingest should not be called")
		return nil
	}, oversized)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleTelemetryEventsWith_BatchMixedOutcomes(t *testing.T) {
	s := &Server{}
	body := `{"events":[` +
		`{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"ls","producer_id":"producer","event_id":"evt-ok"},` +
		`{"type":"tool_use","session":"sess-1","producer_id":"producer","event_id":"evt-bad"},` +
		`{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"ls","producer_id":"producer","event_id":"evt-dup"}` +
		`]}`

	callCount := 0
	rec := postTelemetryEvent(t, s, func(ctx context.Context, pool *pgxpool.Pool, ev telemetry.Event) error {
		callCount++
		if ev.EventID == "evt-bad" {
			return errors.New("telemetry: tool_name is required for tool_use")
		}
		if ev.EventID == "evt-dup" {
			return telemetry.ErrDuplicateEvent
		}
		return nil
	}, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if callCount != 2 {
		t.Fatalf("ingest called %d times, want 2 (only validated events cross the persistence boundary)", callCount)
	}

	var got telemetryBatchResultDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v, body=%s", err, rec.Body.String())
	}
	if len(got.Results) != 3 {
		t.Fatalf("len(Results) = %d, want 3", len(got.Results))
	}
	wantStatus := map[int]string{0: "accepted", 1: "rejected", 2: "duplicate"}
	for i, want := range wantStatus {
		if got.Results[i].Status != want {
			t.Fatalf("Results[%d].Status = %q, want %q (full=%+v)", i, got.Results[i].Status, want, got.Results)
		}
	}
	if got.Results[1].Error == "" {
		t.Fatalf("Results[1].Error = empty, want a rejection reason")
	}
}

func TestHandleTelemetryEventsWith_IdentifiedSingleEventBatch(t *testing.T) {
	s := &Server{}
	body := `{"events":[{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"ls","producer_id":"producer","event_id":"evt-1"}]}`
	rec := postTelemetryEvent(t, s, func(ctx context.Context, pool *pgxpool.Pool, ev telemetry.Event) error {
		return nil
	}, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got telemetryBatchResultDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Status != "accepted" || got.Results[0].EventID != "evt-1" {
		t.Fatalf("Results = %+v, want single accepted evt-1", got.Results)
	}
}

func TestHandleTelemetryEventsWith_BatchExceedsMaxRejected(t *testing.T) {
	s := &Server{}
	event := `{"type":"tool_use","session":"sess-1","tool_name":"bash","tool_input":"ls"}`
	events := make([]string, maxTelemetryBatchEvents+1)
	for i := range events {
		events[i] = event
	}
	body := `{"events":[` + strings.Join(events, ",") + `]}`
	rec := postTelemetryEvent(t, s, func(context.Context, *pgxpool.Pool, telemetry.Event) error {
		t.Fatal("ingest should not be called")
		return nil
	}, body)
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
