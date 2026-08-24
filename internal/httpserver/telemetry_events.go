package httpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/telemetry"
)

// telemetryUsageDTO is the snake_case wire representation of telemetry.Usage.
type telemetryUsageDTO struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

// telemetryEventDTO is the producer-neutral, snake_case wire representation
// accepted by POST /api/telemetry/events. It covers lifecycle
// (session_start/session_end), tool (tool_use/tool_result), usage
// (message_end), and final-message events.
type telemetryEventDTO struct {
	Type    string `json:"type"`
	Session string `json:"session"`

	// lifecycle fields (session_start / session_end)
	SourceSessionID       string   `json:"source_session_id,omitempty"`
	SchemaVersion         string   `json:"schema_version,omitempty"`
	CloseReason           string   `json:"close_reason,omitempty"`
	ParentSourceSessionID *string  `json:"parent_source_session_id,omitempty"`
	Origin                string   `json:"origin,omitempty"`
	Kind                  string   `json:"kind,omitempty"`
	RepositoryID          *int64   `json:"repository_id,omitempty"`
	PhaseID               *int64   `json:"phase_id,omitempty"`
	RepoPath              *string  `json:"repo_path,omitempty"`
	Model                 *string  `json:"model,omitempty"`
	ParentSession         *string  `json:"parent_session,omitempty"`
	AttributionMethod     string   `json:"attribution_method,omitempty"`
	AttributionConfidence *float64 `json:"attribution_confidence,omitempty"`

	// tool fields (tool_use / tool_result)
	ToolCallID *string `json:"tool_call_id,omitempty"`
	ToolName   string  `json:"tool_name,omitempty"`
	ToolInput  string  `json:"tool_input,omitempty"`
	ToolResult string  `json:"tool_result,omitempty"`
	IsError    *bool   `json:"is_error,omitempty"`
	DurationMs *int64  `json:"duration_ms,omitempty"`

	// turn and final-message provenance fields
	Provider        string  `json:"provider,omitempty"`
	ThinkingLevel   string  `json:"thinking_level,omitempty"`
	StopReason      string  `json:"stop_reason,omitempty"`
	Role            string  `json:"role,omitempty"`
	Content         string  `json:"content,omitempty"`
	SourceMessageID *string `json:"source_message_id,omitempty"`
	TurnIndex       *int64  `json:"turn_index,omitempty"`
	InputSource     string  `json:"input_source,omitempty"`
	IsFinal         bool    `json:"is_final,omitempty"`

	// usage fields (message_end)
	Usage *telemetryUsageDTO `json:"usage,omitempty"`

	// Producer privacy assertions retained across the persistence boundary.
	Redacted           bool `json:"redacted,omitempty"`
	Omitted            bool `json:"omitted,omitempty"`
	ToolInputRedacted  bool `json:"tool_input_redacted,omitempty"`
	ToolInputOmitted   bool `json:"tool_input_omitted,omitempty"`
	ToolResultRedacted bool `json:"tool_result_redacted,omitempty"`
	ToolResultOmitted  bool `json:"tool_result_omitted,omitempty"`
	ContentRedacted    bool `json:"content_redacted,omitempty"`

	Timestamp string `json:"timestamp,omitempty"`

	// Producer event identity and client sequence, used for transactional
	// duplicate detection via the telemetry receipt ledger. Optional: if
	// omitted, the event is processed without duplicate detection.
	ProducerID string `json:"producer_id,omitempty"`
	EventID    string `json:"event_id,omitempty"`
	ClientSeq  int64  `json:"client_seq,omitempty"`
}

const telemetryMetadataMaxBytes = 4 * 1024

func (dto *telemetryEventDTO) validate() error {
	typeName := telemetry.EventType(dto.Type)
	switch typeName {
	case telemetry.EventSessionStart, telemetry.EventSessionEnd, telemetry.EventToolUse,
		telemetry.EventToolResult, telemetry.EventMessageEnd, telemetry.EventFinalMessage:
	default:
		return fmt.Errorf("unknown telemetry event type %q", dto.Type)
	}

	metadata := []struct {
		name, value string
	}{
		{"type", dto.Type}, {"session", dto.Session}, {"source_session_id", dto.SourceSessionID},
		{"schema_version", dto.SchemaVersion}, {"close_reason", dto.CloseReason},
		{"origin", dto.Origin}, {"kind", dto.Kind}, {"tool_name", dto.ToolName},
		{"provider", dto.Provider}, {"thinking_level", dto.ThinkingLevel}, {"stop_reason", dto.StopReason},
		{"input_source", dto.InputSource}, {"attribution_method", dto.AttributionMethod},
		{"role", dto.Role}, {"timestamp", dto.Timestamp}, {"producer_id", dto.ProducerID}, {"event_id", dto.EventID},
	}
	for _, field := range metadata {
		if len(field.value) > telemetryMetadataMaxBytes {
			return fmt.Errorf("%s exceeds maximum of %d bytes", field.name, telemetryMetadataMaxBytes)
		}
	}
	for name, value := range map[string]*string{
		"repo_path": dto.RepoPath, "model": dto.Model, "parent_session": dto.ParentSession,
		"parent_source_session_id": dto.ParentSourceSessionID, "source_message_id": dto.SourceMessageID,
		"tool_call_id": dto.ToolCallID,
	} {
		if value != nil && len(*value) > telemetryMetadataMaxBytes {
			return fmt.Errorf("%s exceeds maximum of %d bytes", name, telemetryMetadataMaxBytes)
		}
	}
	if dto.RepositoryID != nil && *dto.RepositoryID <= 0 || dto.PhaseID != nil && *dto.PhaseID <= 0 {
		return fmt.Errorf("repository_id and phase_id must be positive when provided")
	}
	if dto.DurationMs != nil && *dto.DurationMs < 0 || dto.ClientSeq < 0 || dto.TurnIndex != nil && *dto.TurnIndex < 0 {
		return fmt.Errorf("duration_ms, client_seq, and turn_index must be non-negative")
	}
	if dto.AttributionConfidence != nil && (*dto.AttributionConfidence < 0 || *dto.AttributionConfidence > 1 || math.IsNaN(*dto.AttributionConfidence)) {
		return fmt.Errorf("attribution_confidence must be between 0 and 1")
	}
	if dto.SchemaVersion != "" && dto.SchemaVersion != telemetry.SemanticSchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", dto.SchemaVersion)
	}

	if typeName == telemetry.EventMessageEnd {
		if dto.Usage == nil {
			return fmt.Errorf("usage is required for message_end")
		}
		u := dto.Usage
		if u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheReadTokens < 0 || u.CacheWriteTokens < 0 || u.CostUSD < 0 || math.IsNaN(u.CostUSD) || math.IsInf(u.CostUSD, 0) {
			return fmt.Errorf("usage values must be finite and non-negative")
		}
	} else if dto.Usage != nil {
		return fmt.Errorf("usage is only allowed for message_end")
	}

	hasLifecycle := dto.SourceSessionID != "" || dto.Origin != "" || dto.Kind != ""
	hasToolIdentity := dto.ToolCallID != nil || dto.ToolName != ""
	hasToolOutcome := dto.IsError != nil || dto.DurationMs != nil
	hasAnyTool := hasToolIdentity || hasToolOutcome || dto.ToolInput != "" || dto.ToolResult != "" ||
		dto.ToolInputRedacted || dto.ToolInputOmitted || dto.ToolResultRedacted || dto.ToolResultOmitted
	hasMessage := dto.Role != "" || dto.Content != "" || dto.ContentRedacted || dto.InputSource != "" || dto.IsFinal
	hasCorrelation := dto.SourceMessageID != nil || dto.TurnIndex != nil
	hasTurn := dto.Provider != "" || dto.ThinkingLevel != "" || dto.StopReason != ""

	switch typeName {
	case telemetry.EventSessionStart:
		if dto.SourceSessionID == "" || dto.Origin == "" {
			return fmt.Errorf("source_session_id and origin are required for session_start")
		}
		if hasAnyTool || hasMessage || hasCorrelation || hasTurn || dto.CloseReason != "" {
			return fmt.Errorf("fields do not match session_start event shape")
		}
	case telemetry.EventSessionEnd:
		if hasLifecycle || hasAnyTool || hasMessage || hasCorrelation || hasTurn {
			return fmt.Errorf("fields do not match session_end event shape")
		}
	case telemetry.EventToolUse:
		if dto.ToolName == "" {
			return fmt.Errorf("tool_name is required for tool_use")
		}
		if hasLifecycle || hasToolOutcome || dto.ToolResult != "" || dto.ToolResultRedacted || dto.ToolResultOmitted || hasMessage || hasCorrelation || hasTurn || dto.CloseReason != "" {
			return fmt.Errorf("fields do not match tool_use event shape")
		}
		if dto.ToolInputOmitted && dto.ToolInput != "" {
			return fmt.Errorf("tool_input must be absent when tool_input_omitted is true")
		}
		dto.ToolInput = truncateTelemetryText(dto.ToolInput)
	case telemetry.EventToolResult:
		if dto.ToolName == "" {
			return fmt.Errorf("tool_name is required for tool_result")
		}
		if hasLifecycle || dto.ToolInput != "" || dto.ToolInputRedacted || dto.ToolInputOmitted || hasMessage || hasCorrelation || hasTurn || dto.CloseReason != "" {
			return fmt.Errorf("fields do not match tool_result event shape")
		}
		if dto.ToolResultOmitted && dto.ToolResult != "" {
			return fmt.Errorf("tool_result must be absent when tool_result_omitted is true")
		}
		dto.ToolResult = truncateTelemetryText(dto.ToolResult)
	case telemetry.EventMessageEnd:
		if hasLifecycle || hasAnyTool || hasMessage || dto.CloseReason != "" {
			return fmt.Errorf("fields do not match message_end event shape")
		}
	case telemetry.EventFinalMessage:
		if dto.Role == "" {
			return fmt.Errorf("role is required for final_message")
		}
		if hasLifecycle || hasAnyTool || hasTurn || dto.CloseReason != "" {
			return fmt.Errorf("fields do not match final_message event shape")
		}
		dto.Content = truncateTelemetryText(dto.Content)
	}
	fieldRedacted := dto.ToolInputRedacted || dto.ToolResultRedacted || dto.ContentRedacted
	fieldOmitted := dto.ToolInputOmitted || dto.ToolResultOmitted
	if fieldRedacted && !dto.Redacted {
		return fmt.Errorf("field redaction requires redacted annotation")
	}
	if fieldOmitted && !dto.Omitted {
		return fmt.Errorf("field omission requires omitted annotation")
	}
	if dto.Omitted && !fieldOmitted {
		return fmt.Errorf("omitted annotation requires an omitted field")
	}
	if (dto.ProducerID == "") != (dto.EventID == "") {
		return fmt.Errorf("producer_id and event_id must both be set together")
	}
	return nil
}

func optionalTelemetryString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func truncateTelemetryText(s string) string {
	if len(s) <= telemetry.MaxContentBytes {
		return s
	}
	cut := telemetry.MaxContentBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func parseTelemetryTimestamp(ts string) (time.Time, error) {
	if ts == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp %q: %w", ts, err)
	}
	return t, nil
}

// toTelemetryEvent maps the wire DTO into a telemetry.Event. It performs only
// the structural validation needed to build the event; policy validation
// (required fields per event type, delta rejection, etc.) is owned by
// telemetry.Ingest so persistence policy stays in one place.
func (dto telemetryEventDTO) toTelemetryEvent() (telemetry.Event, error) {
	if dto.Session == "" {
		return telemetry.Event{}, fmt.Errorf("session is required")
	}
	if dto.Type == "" {
		return telemetry.Event{}, fmt.Errorf("type is required")
	}
	if err := dto.validate(); err != nil {
		return telemetry.Event{}, err
	}
	ts, err := parseTelemetryTimestamp(dto.Timestamp)
	if err != nil {
		return telemetry.Event{}, err
	}

	usage := telemetry.Usage{}
	if dto.Usage != nil {
		usage = telemetry.Usage{
			InputTokens:      dto.Usage.InputTokens,
			OutputTokens:     dto.Usage.OutputTokens,
			CacheReadTokens:  dto.Usage.CacheReadTokens,
			CacheWriteTokens: dto.Usage.CacheWriteTokens,
			CostUSD:          dto.Usage.CostUSD,
		}
	}

	return telemetry.Event{
		Type: telemetry.EventType(dto.Type),
		Session: telemetry.Session{
			Session:               dto.Session,
			SourceSessionID:       dto.SourceSessionID,
			Origin:                dto.Origin,
			Kind:                  telemetry.SessionKind(dto.Kind),
			SchemaVersion:         dto.SchemaVersion,
			CloseReason:           dto.CloseReason,
			ParentSourceSessionID: dto.ParentSourceSessionID,
		},
		Attribution: telemetry.Attribution{
			RepositoryID:          dto.RepositoryID,
			PhaseID:               dto.PhaseID,
			RepoPath:              dto.RepoPath,
			Model:                 dto.Model,
			ParentSession:         dto.ParentSession,
			AttributionMethod:     dto.AttributionMethod,
			AttributionConfidence: dto.AttributionConfidence,
		},
		Tool: telemetry.Tool{
			ToolCallID:     dto.ToolCallID,
			ToolName:       dto.ToolName,
			Input:          dto.ToolInput,
			Result:         dto.ToolResult,
			IsError:        dto.IsError,
			DurationMs:     dto.DurationMs,
			InputRedacted:  dto.ToolInputRedacted,
			InputOmitted:   dto.ToolInputOmitted,
			ResultRedacted: dto.ToolResultRedacted,
			ResultOmitted:  dto.ToolResultOmitted,
		},
		Message: telemetry.Message{
			Role:            dto.Role,
			Content:         dto.Content,
			ContentRedacted: dto.ContentRedacted,
			SourceMessageID: dto.SourceMessageID,
			TurnIndex:       dto.TurnIndex,
			InputSource:     dto.InputSource,
			IsFinal:         dto.IsFinal,
		},
		Usage: usage,
		Turn: telemetry.Turn{
			TurnIndex: dto.TurnIndex, SourceMessageID: dto.SourceMessageID,
			Model: optionalTelemetryString(dto.Model), Provider: dto.Provider,
			ThinkingLevel: dto.ThinkingLevel, StopReason: dto.StopReason,
		},
		Privacy: telemetry.Privacy{
			Redacted: dto.Redacted,
			Omitted:  dto.Omitted,
		},
		Timestamp:  ts,
		ProducerID: dto.ProducerID,
		EventID:    dto.EventID,
		ClientSeq:  dto.ClientSeq,
	}, nil
}

// telemetryIngestFunc is the shape of the persistence entry point used by
// the handler; tests substitute a fake to observe/short-circuit behavior.
type telemetryIngestFunc func(context.Context, *pgxpool.Pool, telemetry.Event) error

// telemetryRequestMaxBytes bounds the total HTTP request body. It is larger
// than the per-event cap so a bounded batch can be delivered in one request.
const telemetryRequestMaxBytes = 8 * telemetry.MaxContentBytes

// maxTelemetryBatchEvents bounds the number of events accepted in a single
// batch request, keeping worst-case per-request ingestion work bounded.
const maxTelemetryBatchEvents = 500

// telemetryEventMaxBytes bounds each encoded event independently of the
// request and batch limits. The allowance above the one permitted large text
// field is for bounded structural metadata and JSON encoding overhead.
const telemetryEventMaxBytes = telemetry.MaxContentBytes + 64*1024

// telemetryEventResultDTO reports the outcome of ingesting a single event
// within a batch request.
type telemetryEventResultDTO struct {
	Index   int    `json:"index"`
	EventID string `json:"event_id,omitempty"`
	// Status is one of "accepted", "duplicate", or "rejected".
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// telemetryBatchResultDTO is the response body for a batch ingest request.
type telemetryBatchResultDTO struct {
	Results []telemetryEventResultDTO `json:"results"`
}

// telemetryBatchEnvelope detects the identified batch wire format: a JSON
// object carrying an "events" array. Its presence (even for a single
// event) opts the request into per-event accepted/duplicate/rejected
// outcome reporting instead of the legacy single-event 204 response,
// without disturbing existing callers that POST a bare event object.
type telemetryBatchEnvelope struct {
	Events []json.RawMessage `json:"events"`
}

func decodeTelemetryJSON(raw []byte, dst any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func (s *Server) requireTelemetryAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.telemetryRequestAuthorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="telemetry"`)
			jsonErr(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) telemetryRequestAuthorized(r *http.Request) bool {
	if s.telemetryAllowUnauthenticated && isLoopbackRemote(r.RemoteAddr) {
		return true
	}
	if s.telemetryToken == "" {
		return false
	}

	scheme, supplied, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || supplied == "" {
		return false
	}
	// Hashing both values first keeps comparison work independent of token
	// length; subtle.ConstantTimeCompare returns early for unequal lengths.
	expectedHash := sha256.Sum256([]byte(s.telemetryToken))
	suppliedHash := sha256.Sum256([]byte(supplied))
	return subtle.ConstantTimeCompare(expectedHash[:], suppliedHash[:]) == 1
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// handleTelemetryEvents accepts producer-neutral telemetry events and routes
// all persistence policy through telemetry.Ingest.
func (s *Server) handleTelemetryEvents(w http.ResponseWriter, r *http.Request) {
	s.handleTelemetryEventsWith(w, r, telemetry.Ingest)
}

func (s *Server) handleTelemetryEventsWith(w http.ResponseWriter, r *http.Request, ingest telemetryIngestFunc) {
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, telemetryRequestMaxBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			jsonErr(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		jsonErr(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err == nil {
		if _, batch := root["events"]; batch {
			var envelope telemetryBatchEnvelope
			if err := decodeTelemetryJSON(raw, &envelope); err != nil {
				jsonErr(w, "invalid batch: "+err.Error(), http.StatusBadRequest)
				return
			}
			s.handleTelemetryEventBatch(w, r, envelope.Events, ingest)
			return
		}
	}

	if len(raw) > telemetryEventMaxBytes {
		jsonErr(w, "event too large", http.StatusRequestEntityTooLarge)
		return
	}
	var dto telemetryEventDTO
	if err := decodeTelemetryJSON(raw, &dto); err != nil {
		jsonErr(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	ev, err := dto.toTelemetryEvent()
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := ingest(r.Context(), s.pool, ev); err != nil && !errors.Is(err, telemetry.ErrDuplicateEvent) {
		code := http.StatusInternalServerError
		if strings.HasPrefix(err.Error(), "telemetry:") {
			code = http.StatusBadRequest
		}
		jsonErr(w, err.Error(), code)
		return
	}

	// Legacy behavior: a bare event object always resolves to a single
	// 204, whether it was newly accepted or an idempotent duplicate skip.
	w.WriteHeader(http.StatusNoContent)
}

// handleTelemetryEventBatch ingests an identified batch of events
// (including a batch of one), reporting a per-event accepted/duplicate/
// rejected outcome instead of failing the whole request when some events
// are invalid. Valid events in a mixed batch are still ingested.
func (s *Server) handleTelemetryEventBatch(w http.ResponseWriter, r *http.Request, rawEvents []json.RawMessage, ingest telemetryIngestFunc) {
	if len(rawEvents) == 0 {
		jsonErr(w, "events must not be empty", http.StatusBadRequest)
		return
	}
	if len(rawEvents) > maxTelemetryBatchEvents {
		jsonErr(w, fmt.Sprintf("batch exceeds maximum of %d events", maxTelemetryBatchEvents), http.StatusRequestEntityTooLarge)
		return
	}

	results := make([]telemetryEventResultDTO, len(rawEvents))
	for i, raw := range rawEvents {
		result := telemetryEventResultDTO{Index: i}

		if len(raw) > telemetryEventMaxBytes {
			result.Status = "rejected"
			result.Error = "event too large"
			results[i] = result
			continue
		}
		var dto telemetryEventDTO
		if err := decodeTelemetryJSON(raw, &dto); err != nil {
			result.Status = "rejected"
			result.Error = "invalid json: " + err.Error()
			results[i] = result
			continue
		}
		result.EventID = dto.EventID

		ev, err := dto.toTelemetryEvent()
		if err != nil {
			result.Status = "rejected"
			result.Error = err.Error()
			results[i] = result
			continue
		}

		if err := ingest(r.Context(), s.pool, ev); err != nil {
			if errors.Is(err, telemetry.ErrDuplicateEvent) {
				result.Status = "duplicate"
			} else {
				result.Status = "rejected"
				result.Error = err.Error()
			}
			results[i] = result
			continue
		}

		result.Status = "accepted"
		results[i] = result
	}

	jsonOK(w, telemetryBatchResultDTO{Results: results}, http.StatusOK)
}
