package webui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

	// Message provenance. InputSource and IsFinal are producer assertions;
	// unknown values are deliberately not promoted to narrative facts.
	Role        string
	InputSource string
	IsFinal     bool
	Redacted    bool
	Omitted     bool

	// Original-byte/hash truncation metadata, populated when available.
	// Tool calls carry independent input/result provenance; messages
	// only ever populate the "Result" pair (their single content body).
	InputSHA256         string
	InputOriginalBytes  int64
	ResultSHA256        string
	ResultOriginalBytes int64

	// ToolSummary is a bounded, tool-specific compact summary of a
	// Kind == "tool_call" event (e.g. the path read/written, the bash
	// command run, or a generic fallback for unrecognised tool
	// schemas). It never includes the full input/result payload, so it
	// is safe for a reader-first overview even when Payload carries a
	// 100+ KiB body; the full payload remains available only in the
	// Raw evidence disclosure. Empty for non-tool_call events.
	ToolSummary string

	// ToolInputBytes/ToolResultBytes are the byte sizes of the tool's
	// input/result content, independent of whatever compact summary is
	// shown alongside them: the recorded original size when the
	// payload was truncated, or the length of the stored content
	// otherwise. Zero for non-tool_call events or when no content was
	// recorded.
	ToolInputBytes  int64
	ToolResultBytes int64

	// ToolResultPreview is a bounded, generic (not tool-schema-aware)
	// preview of the tool's result content — its first line, clipped to
	// toolSummaryMaxLen runes — so a compact tools listing can show a
	// hint of what came back even for tool schemas summarizeToolCall
	// has no dedicated case for. Like ToolSummary, it never grows with
	// the size of the underlying payload. Empty for non-tool_call
	// events or when no result was recorded.
	ToolResultPreview string
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

	// Narrative is a derived, human-readable summary of this session's
	// curated event timeline (first user request, latest assistant
	// outcome, last completed activity, and conversational groupings).
	// It is projected entirely from Events/turns/tool calls/messages
	// already loaded for this session; it introduces no new payload
	// storage and no new database reads.
	Narrative telemetryNarrativeView
}

// telemetryConversationGroup is a contiguous run of timeline events that
// belong to the same conversational exchange: a user message (if any)
// followed by the turns, tool calls, and assistant messages that
// resulted from it, up to (but not including) the next user message.
// Sessions with no user messages at all (e.g. historical sessions
// captured before message attribution existed) collapse to a single
// group containing every event, so the timeline is never dropped.
type telemetryConversationGroup struct {
	Label     string
	StartedAt time.Time
	Events    []telemetryEventView

	// UserPreview/AssistantPreview are bounded (see
	// narrativePreviewMaxLen) previews of the first substantive user
	// message and the most recent substantive assistant message within
	// this exchange, respectively — the same "first request, latest
	// outcome" semantics as the session-level narrative, scoped to a
	// single conversational group. Has*Preview is false when the group
	// recorded no substantive message of that role; the full message
	// content remains available via Events in the Raw evidence
	// disclosure regardless.
	UserPreview         string
	HasUserPreview      bool
	AssistantPreview    string
	HasAssistantPreview bool
}

// telemetryNarrativeView is a projection of a session's curated event
// timeline into the handful of facts a session list/detail view wants to
// lead with, rather than the raw sequence-ordered stream: the first user
// request (if the session recorded one), the most recent substantive
// assistant outcome, the last completed activity of any kind, model
// call/tool/error aggregates, and the timeline split into conversational
// groups. All of it is derived from the same turns/tool calls/messages
// already assembled into telemetrySessionView.Events; no additional
// payload is stored or fetched to build it.
type telemetryNarrativeView struct {
	// FirstUserRequest is the earliest (lowest Seq) substantive "user"
	// message content in the session. HasUserRequest is false — and
	// FirstUserRequest is "" — for historical sessions that never
	// recorded a user message; callers must render an honest fallback
	// rather than a synthesized placeholder.
	FirstUserRequest string
	HasUserRequest   bool

	// LatestAssistantOutcome is the most recent (highest Seq)
	// substantive "assistant" message content. HasAssistantOutcome is
	// false when no assistant message ever recorded real content.
	LatestAssistantOutcome string
	HasAssistantOutcome    bool

	// Provenance/partial flags distinguish absent evidence from evidence
	// whose producer metadata is unknown or whose body is incomplete.
	GoalProvenanceUnknown    bool
	OutcomeProvenanceUnknown bool
	GoalPartial              bool
	OutcomePartial           bool

	// LastActivityLabel/LastActivityAt describe the last event in the
	// merged timeline, regardless of kind (turn, tool call, or
	// message) — the most recent thing this session is known to have
	// done. HasActivity is false only for a session with no events at
	// all.
	LastActivityLabel string
	LastActivityAt    time.Time
	HasActivity       bool

	// ModelCallCount, ToolCallCount and ErrorCount mirror the session's
	// turn/tool-call/error aggregates, named for narrative display
	// alongside the fields above.
	ModelCallCount int
	ToolCallCount  int
	ErrorCount     int

	// CostUSD mirrors the owning session's running cost total, grouped
	// alongside ModelCallCount/ErrorCount so a reader-first overview can
	// show "model calls/cost/errors" together.
	CostUSD float64

	// Groups splits Events into conversational exchanges (see
	// telemetryConversationGroup).
	Groups []telemetryConversationGroup
}

// narrativePreviewMaxLen bounds every narrative-level message preview
// (the session's Goal/Latest outcome, and each conversational exchange's
// user/assistant previews) so the reader-first overview never grows
// proportional to a captured message's size, however large the raw
// payload is (messages are only truncated at the DB layer well past
// this bound, if at all). The full message content remains available
// only in the Raw evidence disclosure via Payload.
const narrativePreviewMaxLen = 400

// boundNarrativePreview clips a candidate narrative preview string to
// narrativePreviewMaxLen runes, appending an ellipsis when clipped. It
// is the single choke point every narrative-facing message preview
// (FirstUserRequest, LatestAssistantOutcome, and each conversational
// group's UserPreview/AssistantPreview) funnels through, so none of
// them can leak an unbounded payload into the overview regardless of
// how large the underlying message content is.
func boundNarrativePreview(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > narrativePreviewMaxLen {
		return string(runes[:narrativePreviewMaxLen]) + "…"
	}
	return s
}

// isSubstantiveEventPayload reports whether a rendered event payload
// carries real content, as opposed to one of the "(no content)"/"(empty)"
// placeholders strOrPlaceholder produces for unrecorded/blank payloads.
// Narrative projections (first request, latest outcome) should only ever
// point at substantive content, never a placeholder.
func isSubstantiveEventPayload(payload string) bool {
	return payload != "" && payload != "(no content)" && payload != "(empty)"
}

// buildTelemetryNarrativeView derives the narrative projection described
// on telemetryNarrativeView from an already sequence-ordered event
// timeline and the session's turn/tool-call/error aggregates. Events must
// be sorted ascending by Seq (as buildTelemetrySessionView guarantees)
// for "first" and "latest"/"last" semantics to be deterministic.
func buildTelemetryNarrativeView(events []telemetryEventView, turnCount, toolCallCount, errorCount int, costUSD float64) telemetryNarrativeView {
	nv := telemetryNarrativeView{
		ModelCallCount: turnCount,
		ToolCallCount:  toolCallCount,
		ErrorCount:     errorCount,
		CostUSD:        costUSD,
	}

	for _, ev := range events {
		if ev.Kind != "message" || ev.Role != "user" {
			continue
		}
		if ev.InputSource == "unknown" || ev.InputSource == "" {
			nv.GoalProvenanceUnknown = true
		}
		// Harness and extension prompts are not interactive human goals.
		// Historical rows with no source metadata remain unknown.
		if ev.InputSource == "interactive" && !isSubstantiveEventPayload(ev.Payload) && (ev.Truncated || ev.Redacted || ev.Omitted) {
			nv.GoalPartial = true
		}
		if ev.InputSource == "interactive" && isSubstantiveEventPayload(ev.Payload) {
			nv.FirstUserRequest = boundNarrativePreview(ev.Payload)
			nv.HasUserRequest = true
			nv.GoalPartial = ev.Truncated || ev.Redacted || ev.Omitted || nv.FirstUserRequest != strings.TrimSpace(ev.Payload)
			break
		}
	}

	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Kind != "message" || ev.Role != "assistant" {
			continue
		}
		if !ev.IsFinal {
			nv.OutcomeProvenanceUnknown = true
			continue
		}
		if !isSubstantiveEventPayload(ev.Payload) && (ev.Truncated || ev.Redacted || ev.Omitted) {
			nv.OutcomePartial = true
		}
		if isSubstantiveEventPayload(ev.Payload) {
			nv.LatestAssistantOutcome = boundNarrativePreview(ev.Payload)
			nv.HasAssistantOutcome = true
			nv.OutcomePartial = ev.Truncated || ev.Redacted || ev.Omitted || nv.LatestAssistantOutcome != strings.TrimSpace(ev.Payload)
			break
		}
	}

	// The last completed activity is the event with the latest
	// completion timestamp (At — which for tool calls prefers
	// FinishedAt over the invocation time, see toolCallEventTime), not
	// simply the highest-Seq event: a tool call can be allocated an
	// earlier sequence number than events that started after it, yet
	// still be the last one to actually finish (e.g. a long-running
	// bash command whose result arrives after later, faster events have
	// already completed). Ties prefer the later event in Seq order, so
	// behaviour is unchanged for the common case where every event's
	// timestamp is distinct and monotonic with Seq.
	for _, ev := range events {
		if !nv.HasActivity || !ev.At.Before(nv.LastActivityAt) {
			nv.LastActivityLabel = ev.Title
			nv.LastActivityAt = ev.At
			nv.HasActivity = true
		}
	}

	nv.Groups = buildTelemetryConversationGroups(events)

	return nv
}

// buildTelemetryConversationGroups splits a sequence-ordered event
// timeline into conversational exchanges: each "user" message starts a
// new group, and every following event (turns, tool calls, assistant
// messages) belongs to that group until the next user message. A
// session with no user messages — common for historical sessions
// captured before message attribution existed — still yields a single
// group holding every event, so no activity is silently dropped from the
// narrative. An empty timeline yields no groups.
func buildTelemetryConversationGroups(events []telemetryEventView) []telemetryConversationGroup {
	var groups []telemetryConversationGroup
	for _, ev := range events {
		startsGroup := ev.Kind == "message" && ev.Role == "user" && ev.InputSource == "interactive"
		if len(groups) == 0 || startsGroup {
			groups = append(groups, telemetryConversationGroup{StartedAt: ev.At})
		}
		idx := len(groups) - 1
		groups[idx].Events = append(groups[idx].Events, ev)
	}
	for i := range groups {
		groups[i].Label = fmt.Sprintf("Exchange %d", i+1)

		for _, ev := range groups[i].Events {
			if ev.Kind == "message" && ev.Role == "user" && ev.InputSource == "interactive" && isSubstantiveEventPayload(ev.Payload) {
				groups[i].UserPreview = boundNarrativePreview(ev.Payload)
				groups[i].HasUserPreview = true
				break
			}
		}
		for j := len(groups[i].Events) - 1; j >= 0; j-- {
			ev := groups[i].Events[j]
			if ev.Kind == "message" && ev.Role == "assistant" && ev.IsFinal && isSubstantiveEventPayload(ev.Payload) {
				groups[i].AssistantPreview = boundNarrativePreview(ev.Payload)
				groups[i].HasAssistantPreview = true
				break
			}
		}
	}
	return groups
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
			Title: fmt.Sprintf("Model call — %d in / %d out tokens", t.InputTokens, t.OutputTokens),
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
			Redacted:            c.ToolInputRedacted || c.ToolResultRedacted,
			Omitted:             c.ToolInputOmitted || c.ToolResultOmitted,
			DurationLabel:       formatDurationMs(c.DurationMs),
			At:                  toolCallEventTime(c),
			ToolCallID:          strOrEmpty(c.ToolCallID),
			InputSHA256:         strOrEmpty(c.ToolInputSHA256),
			InputOriginalBytes:  int64PtrOrZero(c.ToolInputOriginalBytes),
			ResultSHA256:        strOrEmpty(c.ToolResultSHA256),
			ResultOriginalBytes: int64PtrOrZero(c.ToolResultOriginalBytes),
			ToolSummary:         summarizeToolCall(c.ToolName, c.ToolInput, c.ToolResult),
			ToolInputBytes:      toolContentBytes(c.ToolInput, c.ToolInputOriginalBytes),
			ToolResultBytes:     toolContentBytes(c.ToolResult, c.ToolResultOriginalBytes),
			ToolResultPreview:   boundSummary(strOrEmptyPtr(c.ToolResult)),
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
			Role:                m.Role,
			InputSource:         m.InputSource,
			IsFinal:             m.IsFinal,
			Redacted:            m.ContentRedacted,
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
	view.Narrative = buildTelemetryNarrativeView(events, view.TurnCount, view.ToolCallCount, view.ErrorCount, sess.CostUSD)
	return view
}

// toolCallEventTime picks the display timestamp for a tool_call event,
// preferring the tool's completion time (FinishedAt) when the producer
// recorded one — e.g. a long-running bash command's result arrives well
// after its invocation was captured — and falling back to CreatedAt
// (the invocation time) otherwise.
func toolCallEventTime(c db.AgentToolCall) time.Time {
	if c.FinishedAt != nil {
		return *c.FinishedAt
	}
	return c.CreatedAt
}

// toolContentBytes reports the byte size of a tool's input/result
// content: the recorded original size when the producer truncated it
// before storage, or the length of the stored content otherwise. It
// returns 0 when no content was recorded at all, so a compact tools
// listing can show an honest size even for content this view never
// renders in full outside the Raw evidence disclosure.
func toolContentBytes(content *string, originalBytes *int64) int64 {
	if originalBytes != nil {
		return *originalBytes
	}
	if content != nil {
		return int64(len(*content))
	}
	return 0
}

// toolSummaryMaxLen bounds every tool-specific compact summary so an
// overview never grows proportional to a captured payload's size (which
// may be well over 100 KiB): summaries are for orientation, not replay.
const toolSummaryMaxLen = 200

// boundSummary collapses a candidate summary string to its first line
// and clips it to toolSummaryMaxLen runes, appending an ellipsis when
// clipped. It is the single choke point every summarizeToolCall branch
// funnels through, so no branch can accidentally leak a large payload
// into the bounded overview.
func boundSummary(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	runes := []rune(s)
	if len(runes) > toolSummaryMaxLen {
		return string(runes[:toolSummaryMaxLen]) + "…"
	}
	return s
}

// jsonStringField extracts a top-level string field from a JSON object
// payload, trying each candidate key in order. It returns "" whenever
// payload is not a JSON object, decoding fails, or none of the
// candidate keys hold a string value — callers treat that as "no
// structured field available" and fall back to the raw payload, rather
// than treating it as an error. This keeps summarization safe for
// arbitrary/unknown tool input schemas (plain text, non-object JSON,
// or malformed payloads) as well as the well-known ones.
func jsonStringField(payload string, candidates ...string) string {
	if payload == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return ""
	}
	for _, key := range candidates {
		if v, ok := obj[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// summarizeToolCall renders a bounded, tool-specific one-line summary of
// a tool call's input/result for the reader-first overview: the file
// path for read/edit/write, the command (and output line count) for
// bash, or a generic "name: first line of input" fallback for any tool
// name this view has no dedicated schema for (including malformed or
// non-JSON input). It never returns more than toolSummaryMaxLen runes
// and never includes the full input/result payload, regardless of how
// large either is — the full payload is only ever rendered in the Raw
// evidence disclosure via Payload.
func summarizeToolCall(name string, input, result *string) string {
	in := strOrEmptyPtr(input)
	res := strOrEmptyPtr(result)

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read":
		path := jsonStringField(in, "path", "file_path", "filePath")
		if path == "" {
			path = in
		}
		summary := fmt.Sprintf("read %s", path)
		if res != "" {
			summary += fmt.Sprintf(" → %d byte(s)", len(res))
		}
		return boundSummary(summary)
	case "bash":
		cmd := jsonStringField(in, "command", "cmd")
		if cmd == "" {
			cmd = in
		}
		summary := "$ " + boundSummary(cmd)
		if res != "" {
			summary += fmt.Sprintf(" (%d output line(s))", strings.Count(strings.TrimRight(res, "\n"), "\n")+1)
		}
		return boundSummary(summary)
	case "edit":
		path := jsonStringField(in, "path", "file_path", "filePath")
		if path == "" {
			return boundSummary("edit: " + in)
		}
		return boundSummary(fmt.Sprintf("edit %s", path))
	case "write":
		path := jsonStringField(in, "path", "file_path", "filePath")
		content := jsonStringField(in, "content")
		if path == "" {
			return boundSummary("write: " + in)
		}
		if content != "" {
			return boundSummary(fmt.Sprintf("write %s (%d byte(s))", path, len(content)))
		}
		return boundSummary(fmt.Sprintf("write %s", path))
	default:
		if in == "" {
			return boundSummary(name)
		}
		return boundSummary(fmt.Sprintf("%s: %s", name, in))
	}
}

// strOrEmptyPtr returns the dereferenced string, or "" when the pointer
// is nil. Distinct from strOrPlaceholder (used for display payloads)
// because summarizeToolCall needs to distinguish "no input recorded"
// from a recorded-but-empty input without ever rendering a placeholder
// string like "(no content)" into a summary.
func strOrEmptyPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
