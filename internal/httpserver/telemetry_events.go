package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
	SourceSessionID string  `json:"source_session_id,omitempty"`
	Origin          string  `json:"origin,omitempty"`
	Kind            string  `json:"kind,omitempty"`
	RepositoryID    *int64  `json:"repository_id,omitempty"`
	PhaseID         *int64  `json:"phase_id,omitempty"`
	RepoPath        *string `json:"repo_path,omitempty"`
	Model           *string `json:"model,omitempty"`
	ParentSession   *string `json:"parent_session,omitempty"`

	// tool fields (tool_use / tool_result)
	ToolCallID *string `json:"tool_call_id,omitempty"`
	ToolName   string  `json:"tool_name,omitempty"`
	ToolInput  string  `json:"tool_input,omitempty"`
	ToolResult string  `json:"tool_result,omitempty"`
	IsError    *bool   `json:"is_error,omitempty"`
	DurationMs *int64  `json:"duration_ms,omitempty"`

	// final-message fields
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`

	// usage fields (message_end)
	Usage *telemetryUsageDTO `json:"usage,omitempty"`

	Timestamp string `json:"timestamp,omitempty"`
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
			Session:         dto.Session,
			SourceSessionID: dto.SourceSessionID,
			Origin:          dto.Origin,
			Kind:            telemetry.SessionKind(dto.Kind),
		},
		Attribution: telemetry.Attribution{
			RepositoryID:  dto.RepositoryID,
			PhaseID:       dto.PhaseID,
			RepoPath:      dto.RepoPath,
			Model:         dto.Model,
			ParentSession: dto.ParentSession,
		},
		Tool: telemetry.Tool{
			ToolCallID: dto.ToolCallID,
			ToolName:   dto.ToolName,
			Input:      dto.ToolInput,
			Result:     dto.ToolResult,
			IsError:    dto.IsError,
			DurationMs: dto.DurationMs,
		},
		Message: telemetry.Message{
			Role:    dto.Role,
			Content: dto.Content,
		},
		Usage:     usage,
		Timestamp: ts,
	}, nil
}

// handleTelemetryEvents accepts producer-neutral telemetry events and routes
// all persistence policy through telemetry.Ingest.
func (s *Server) handleTelemetryEvents(w http.ResponseWriter, r *http.Request) {
	s.handleTelemetryEventsWith(w, r, telemetry.Ingest)
}

func (s *Server) handleTelemetryEventsWith(w http.ResponseWriter, r *http.Request, ingest func(context.Context, *pgxpool.Pool, telemetry.Event) error) {
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, telemetry.MaxContentBytes)
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

	var dto telemetryEventDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		jsonErr(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	ev, err := dto.toTelemetryEvent()
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := ingest(r.Context(), s.pool, ev); err != nil {
		code := http.StatusInternalServerError
		if strings.HasPrefix(err.Error(), "telemetry:") {
			code = http.StatusBadRequest
		}
		jsonErr(w, err.Error(), code)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
