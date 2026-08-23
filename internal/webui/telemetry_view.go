package webui

import (
	"fmt"
	"sort"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

// Phase telemetry display types and helpers.
//
// A phase can have one or more agent sessions attached to it (e.g. the
// primary Cerberus session plus any sub-agent sessions). Each session
// accumulates turns (LLM calls with token usage), tool calls (with
// input/result payloads) and messages (final assistant/user text). This
// file assembles a server-rendered view model that merges those three
// event streams into a single sequence-ordered timeline per session, and
// rolls up totals across all sessions for the phase.

// telemetryEventView is a single sequence-ordered event within a session's
// timeline: a turn, a tool call (with its result, if attached), or a
// message. Payload is rendered by the template inside a plain text node,
// so html/template's default auto-escaping keeps it safe even though the
// raw content may contain HTML-like text (e.g. tool output).
type telemetryEventView struct {
	Seq           int64
	Kind          string // "turn", "tool_call", "message"
	Title         string
	Payload       string
	Truncated     bool
	IsError       bool
	DurationLabel string
	At            time.Time

	// ToolCallID identifies the originating tool_call event when the
	// upstream agent surfaced one (e.g. Cerberus tool invocations).
	ToolCallID string

	// Original-byte/hash truncation metadata, populated when available.
	// Tool calls carry independent input/result provenance; messages
	// only ever populate the "Result" pair (their single content body).
	InputSHA256         string
	InputOriginalBytes  int64
	ResultSHA256        string
	ResultOriginalBytes int64
}

// formatCapturedAt renders an event's capture timestamp for display, or a
// placeholder when it was never recorded (zero time).
func formatCapturedAt(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04:05")
}

// telemetrySessionView is the per-session view: identifying metadata,
// aggregate counters (mirroring the session's running totals), and the
// merged, sequence-ordered event timeline.
type telemetrySessionView struct {
	Session        db.AgentSession
	Duration       string
	Events         []telemetryEventView
	TurnCount      int
	ToolCallCount  int
	MessageCount   int
	ErrorCount     int
	TruncatedCount int
}

// phaseTelemetryView is the top-level view model rendered by the
// "phases.telemetry" template: the phase itself, one entry per attached
// agent session, and totals aggregated across all of those sessions.
type phaseTelemetryView struct {
	Phase                 db.Phase
	Sessions              []telemetrySessionView
	TotalSessions         int
	TotalTurns            int64
	TotalToolCalls        int64
	TotalMessages         int64
	TotalErrors           int
	TotalTruncated        int
	TotalInputTokens      int64
	TotalOutputTokens     int64
	TotalCacheReadTokens  int64
	TotalCacheWriteTokens int64
	TotalCostUSD          float64
}

// strOrPlaceholder returns the dereferenced string, or a placeholder when
// the pointer is nil (e.g. a message/tool payload that was never recorded).
func strOrPlaceholder(s *string) string {
	if s == nil {
		return "(no content)"
	}
	if *s == "" {
		return "(empty)"
	}
	return *s
}

// strOrEmpty returns the dereferenced string, or "" when the pointer is
// nil. Used for metadata fields (tool call ID, hashes) that should be
// hidden entirely by the template rather than rendered as a placeholder.
func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// int64PtrOrZero dereferences an optional int64, returning 0 when nil
// (e.g. original-byte counts that were never recorded).
func int64PtrOrZero(n *int64) int64 {
	if n == nil {
		return 0
	}
	return *n
}

// formatDurationMs renders an optional millisecond duration for display,
// or "" when not recorded.
func formatDurationMs(ms *int64) string {
	if ms == nil {
		return ""
	}
	return fmt.Sprintf("%dms", *ms)
}

// formatSessionDuration renders a session's wall-clock duration, or a
// sentinel when the session has not ended yet.
func formatSessionDuration(sess db.AgentSession) string {
	if sess.EndedAt == nil {
		return "running"
	}
	return sess.EndedAt.Sub(sess.StartedAt).Round(time.Second).String()
}

// buildTelemetrySessionView merges a session's turns, tool calls and
// messages into a single sequence-ordered timeline and computes the
// session's display counters.
func buildTelemetrySessionView(sess db.AgentSession, turns []db.AgentTurn, toolCalls []db.AgentToolCall, messages []db.AgentMessage) telemetrySessionView {
	events := make([]telemetryEventView, 0, len(turns)+len(toolCalls)+len(messages))

	for _, t := range turns {
		events = append(events, telemetryEventView{
			Seq:   t.Seq,
			Kind:  "turn",
			Title: fmt.Sprintf("Turn — %d in / %d out tokens", t.InputTokens, t.OutputTokens),
			Payload: fmt.Sprintf(
				"cache_read_tokens=%d\ncache_write_tokens=%d\ncost_usd=%.4f",
				t.CacheReadTokens, t.CacheWriteTokens, t.CostUSD,
			),
			At: t.Ts,
		})
	}

	view := telemetrySessionView{Session: sess, Duration: formatSessionDuration(sess)}

	for _, c := range toolCalls {
		isError := c.IsError != nil && *c.IsError
		truncated := c.ToolInputTruncated || c.ToolResultTruncated
		events = append(events, telemetryEventView{
			Seq:   c.Seq,
			Kind:  "tool_call",
			Title: fmt.Sprintf("Tool: %s", c.ToolName),
			Payload: fmt.Sprintf(
				"input: %s\nresult: %s",
				strOrPlaceholder(c.ToolInput), strOrPlaceholder(c.ToolResult),
			),
			Truncated:           truncated,
			IsError:             isError,
			DurationLabel:       formatDurationMs(c.DurationMs),
			At:                  c.CreatedAt,
			ToolCallID:          strOrEmpty(c.ToolCallID),
			InputSHA256:         strOrEmpty(c.ToolInputSHA256),
			InputOriginalBytes:  int64PtrOrZero(c.ToolInputOriginalBytes),
			ResultSHA256:        strOrEmpty(c.ToolResultSHA256),
			ResultOriginalBytes: int64PtrOrZero(c.ToolResultOriginalBytes),
		})
		if isError {
			view.ErrorCount++
		}
		if truncated {
			view.TruncatedCount++
		}
	}

	for _, m := range messages {
		events = append(events, telemetryEventView{
			Seq:                 m.Seq,
			Kind:                "message",
			Title:               fmt.Sprintf("Message: %s", m.Role),
			Payload:             strOrPlaceholder(m.Content),
			Truncated:           m.ContentTruncated,
			At:                  m.CreatedAt,
			ResultSHA256:        strOrEmpty(m.ContentSHA256),
			ResultOriginalBytes: int64PtrOrZero(m.ContentOriginalBytes),
		})
		if m.ContentTruncated {
			view.TruncatedCount++
		}
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })

	view.Events = events
	view.TurnCount = len(turns)
	view.ToolCallCount = len(toolCalls)
	view.MessageCount = len(messages)
	return view
}

// buildPhaseTelemetryView assembles the full phase telemetry view model
// from a phase and its attached agent sessions' turns, tool calls and
// messages (keyed by agent session ID). Sessions are rendered in the
// order given (callers should list them deterministically, e.g. by
// started_at then id).
func buildPhaseTelemetryView(
	ph db.Phase,
	sessions []db.AgentSession,
	turnsBySession map[int64][]db.AgentTurn,
	toolCallsBySession map[int64][]db.AgentToolCall,
	messagesBySession map[int64][]db.AgentMessage,
) phaseTelemetryView {
	view := phaseTelemetryView{Phase: ph, TotalSessions: len(sessions)}
	view.Sessions = make([]telemetrySessionView, 0, len(sessions))

	for _, sess := range sessions {
		sv := buildTelemetrySessionView(sess, turnsBySession[sess.ID], toolCallsBySession[sess.ID], messagesBySession[sess.ID])
		view.Sessions = append(view.Sessions, sv)

		view.TotalTurns += int64(sv.TurnCount)
		view.TotalToolCalls += int64(sv.ToolCallCount)
		view.TotalMessages += int64(sv.MessageCount)
		view.TotalErrors += sv.ErrorCount
		view.TotalTruncated += sv.TruncatedCount

		view.TotalInputTokens += sess.InputTokens
		view.TotalOutputTokens += sess.OutputTokens
		view.TotalCacheReadTokens += sess.CacheReadTokens
		view.TotalCacheWriteTokens += sess.CacheWriteTokens
		view.TotalCostUSD += sess.CostUSD
	}

	return view
}
