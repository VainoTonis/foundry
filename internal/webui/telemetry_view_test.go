package webui

import (
	"bytes"
	"fmt"
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
			AgentSessionID:          1,
			Seq:                     1,
			ToolCallID:              strp("call-123"),
			ToolName:                "bash",
			ToolInput:               strp("<script>rm -rf /</script>"),
			ToolResult:              strp("done"),
			ToolResultTruncated:     true,
			ToolResultSHA256:        strp("deadbeef"),
			ToolResultOriginalBytes: i64p(4096),
			IsError:                 boolp(true),
			DurationMs:              i64p(42),
			CreatedAt:               started.Add(1 * time.Second),
		},
	}
	messages := []db.AgentMessage{
		{AgentSessionID: 1, Seq: 3, Role: "assistant", IsFinal: true, Content: strp("all good"), CreatedAt: started.Add(3 * time.Second)},
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
		"Captured: 2024-01-01 00:00:01",
		"Tool call ID: call-123",
		"Result SHA-256: deadbeef",
		"4096 original bytes",
		"Curated telemetry",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in rendered output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<script>rm -rf /</script>") {
		t.Fatalf("expected tool input to be escaped, got:\n%s", out)
	}
}

// TestBuildTelemetryNarrativeView_FirstRequestAndLatestOutcome covers the
// core narrative projection: the first substantive user message becomes
// FirstUserRequest, the most recent substantive assistant message
// becomes LatestAssistantOutcome even when an earlier assistant message
// also exists, the last event in the timeline (regardless of kind)
// becomes the last completed activity, and model-call/tool/error
// aggregates are carried through unchanged.
func TestBuildTelemetryNarrativeView_FirstRequestAndLatestOutcome(t *testing.T) {
	started := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 1, Session: "sess-narrative", StartedAt: started}

	turns := []db.AgentTurn{
		{AgentSessionID: 1, Seq: 2, InputTokens: 10, OutputTokens: 5, Ts: started.Add(2 * time.Second)},
	}
	toolCalls := []db.AgentToolCall{
		{AgentSessionID: 1, Seq: 3, ToolName: "bash", ToolInput: strp("ls"), ToolResult: strp("a.txt"), CreatedAt: started.Add(3 * time.Second)},
	}
	messages := []db.AgentMessage{
		{AgentSessionID: 1, Seq: 1, Role: "user", InputSource: "interactive", Content: strp("please list files"), CreatedAt: started.Add(1 * time.Second)},
		{AgentSessionID: 1, Seq: 4, Role: "assistant", IsFinal: true, Content: strp("here is an early answer"), CreatedAt: started.Add(4 * time.Second)},
		{AgentSessionID: 1, Seq: 5, Role: "assistant", IsFinal: true, Content: strp("final answer: a.txt"), CreatedAt: started.Add(5 * time.Second)},
	}

	sv := buildTelemetrySessionView(sess, turns, toolCalls, messages)
	nv := sv.Narrative

	if !nv.HasUserRequest || nv.FirstUserRequest != "please list files" {
		t.Fatalf("FirstUserRequest = %q (has=%v), want %q", nv.FirstUserRequest, nv.HasUserRequest, "please list files")
	}
	if !nv.HasAssistantOutcome || nv.LatestAssistantOutcome != "final answer: a.txt" {
		t.Fatalf("LatestAssistantOutcome = %q (has=%v), want the last assistant message, not the earlier one", nv.LatestAssistantOutcome, nv.HasAssistantOutcome)
	}
	if !nv.HasActivity || nv.LastActivityLabel != "Message: assistant" || !nv.LastActivityAt.Equal(started.Add(5*time.Second)) {
		t.Fatalf("last completed activity = %+v, want the final assistant message event", nv)
	}
	if nv.ModelCallCount != 1 || nv.ToolCallCount != 1 || nv.ErrorCount != 0 {
		t.Fatalf("aggregates = %+v, want ModelCallCount=1 ToolCallCount=1 ErrorCount=0", nv)
	}
}

// TestBuildTelemetryNarrativeView_HonestFallbackWithoutUserMessages
// covers the historical-session case: a session with turns and tool
// calls but no recorded user message at all must not fabricate a
// FirstUserRequest, and must instead report HasUserRequest=false so
// templates can render an honest fallback. The same session's tool call
// still becomes the last completed activity.
func TestBuildTelemetryNarrativeView_HonestFallbackWithoutUserMessages(t *testing.T) {
	started := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 2, Session: "sess-historical", StartedAt: started}

	turns := []db.AgentTurn{
		{AgentSessionID: 2, Seq: 1, InputTokens: 10, OutputTokens: 5, Ts: started},
	}
	toolCalls := []db.AgentToolCall{
		{AgentSessionID: 2, Seq: 2, ToolName: "bash", ToolResult: strp("ok"), CreatedAt: started.Add(time.Second)},
	}

	sv := buildTelemetrySessionView(sess, turns, toolCalls, nil)
	nv := sv.Narrative

	if nv.HasUserRequest || nv.FirstUserRequest != "" {
		t.Fatalf("FirstUserRequest = %q (has=%v), want the honest fallback (empty, has=false) for a session with no user message", nv.FirstUserRequest, nv.HasUserRequest)
	}
	if nv.HasAssistantOutcome || nv.LatestAssistantOutcome != "" {
		t.Fatalf("LatestAssistantOutcome = %q (has=%v), want the honest fallback for a session with no assistant message", nv.LatestAssistantOutcome, nv.HasAssistantOutcome)
	}
	if !nv.HasActivity || nv.LastActivityLabel != "Tool: bash" {
		t.Fatalf("last completed activity = %+v, want the tool call (the last recorded event)", nv)
	}
}

// TestBuildTelemetryNarrativeView_PlaceholderMessagesAreNotSubstantive
// covers that a message row with no recorded content (nil Content,
// rendered as the "(no content)" placeholder) is not mistaken for a
// substantive user request or assistant outcome.
func TestBuildTelemetryNarrativeView_PlaceholderMessagesAreNotSubstantive(t *testing.T) {
	started := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 3, Session: "sess-placeholder", StartedAt: started}

	messages := []db.AgentMessage{
		{AgentSessionID: 3, Seq: 1, Role: "user", InputSource: "interactive", Content: nil, CreatedAt: started},
		{AgentSessionID: 3, Seq: 2, Role: "assistant", IsFinal: true, Content: strp(""), CreatedAt: started.Add(time.Second)},
	}

	sv := buildTelemetrySessionView(sess, nil, nil, messages)
	nv := sv.Narrative

	if nv.HasUserRequest {
		t.Fatalf("HasUserRequest = true, want false for a placeholder (no content) user message")
	}
	if nv.HasAssistantOutcome {
		t.Fatalf("HasAssistantOutcome = true, want false for a placeholder (empty) assistant message")
	}
	// The placeholder messages are still real events, so the last one
	// recorded is still the last completed activity.
	if !nv.HasActivity || nv.LastActivityLabel != "Message: assistant" {
		t.Fatalf("last completed activity = %+v, want the final message event even though it has no substantive content", nv)
	}
}

// TestBuildTelemetryNarrativeView_Empty covers that a session with no
// turns, tool calls, or messages at all produces an entirely empty,
// honest narrative with no groups and HasActivity=false.
func TestBuildTelemetryNarrativeView_Empty(t *testing.T) {
	sess := db.AgentSession{ID: 4, Session: "sess-empty", StartedAt: time.Now()}
	sv := buildTelemetrySessionView(sess, nil, nil, nil)
	nv := sv.Narrative

	if nv.HasUserRequest || nv.HasAssistantOutcome || nv.HasActivity {
		t.Fatalf("expected all Has* flags false for an empty session, got %+v", nv)
	}
	if len(nv.Groups) != 0 {
		t.Fatalf("expected no conversational groups for an empty session, got %+v", nv.Groups)
	}
}

// TestBuildTelemetryConversationGroups covers that a user message starts
// a new conversational group, that all following events (turns, tool
// calls, assistant messages) attach to that group up to the next user
// message, and that groups are labelled and ordered deterministically by
// sequence.
func TestBuildTelemetryConversationGroups(t *testing.T) {
	started := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 5, Session: "sess-groups", StartedAt: started}

	messages := []db.AgentMessage{
		{AgentSessionID: 5, Seq: 1, Role: "user", InputSource: "interactive", Content: strp("first request"), CreatedAt: started},
		{AgentSessionID: 5, Seq: 3, Role: "assistant", IsFinal: true, Content: strp("first answer"), CreatedAt: started.Add(3 * time.Second)},
		{AgentSessionID: 5, Seq: 4, Role: "user", InputSource: "interactive", Content: strp("second request"), CreatedAt: started.Add(4 * time.Second)},
		{AgentSessionID: 5, Seq: 5, Role: "assistant", IsFinal: true, Content: strp("second answer"), CreatedAt: started.Add(5 * time.Second)},
	}
	toolCalls := []db.AgentToolCall{
		{AgentSessionID: 5, Seq: 2, ToolName: "bash", ToolResult: strp("ok"), CreatedAt: started.Add(2 * time.Second)},
	}

	sv := buildTelemetrySessionView(sess, nil, toolCalls, messages)
	groups := sv.Narrative.Groups

	if len(groups) != 2 {
		t.Fatalf("expected 2 conversational groups, got %d: %+v", len(groups), groups)
	}
	if groups[0].Label != "Exchange 1" || groups[1].Label != "Exchange 2" {
		t.Fatalf("unexpected group labels: %q, %q", groups[0].Label, groups[1].Label)
	}
	if len(groups[0].Events) != 3 {
		t.Fatalf("expected the first group to hold the user message, the tool call, and the first assistant reply, got %d events: %+v", len(groups[0].Events), groups[0].Events)
	}
	if len(groups[1].Events) != 2 {
		t.Fatalf("expected the second group to hold the second user message and its assistant reply, got %d events: %+v", len(groups[1].Events), groups[1].Events)
	}
	if !groups[0].StartedAt.Equal(started) {
		t.Fatalf("groups[0].StartedAt = %v, want %v (the first user message's timestamp)", groups[0].StartedAt, started)
	}
}

// TestBuildTelemetryConversationGroups_NoUserMessagesCollapseToOneGroup
// covers the historical-session fallback: a session with no user
// messages at all must still surface every event in a single group,
// rather than dropping it or producing zero groups.
func TestBuildTelemetryConversationGroups_NoUserMessagesCollapseToOneGroup(t *testing.T) {
	started := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 6, Session: "sess-no-user", StartedAt: started}

	turns := []db.AgentTurn{
		{AgentSessionID: 6, Seq: 1, Ts: started},
	}
	messages := []db.AgentMessage{
		{AgentSessionID: 6, Seq: 2, Role: "assistant", IsFinal: true, Content: strp("only an assistant note"), CreatedAt: started.Add(time.Second)},
	}

	sv := buildTelemetrySessionView(sess, turns, nil, messages)
	groups := sv.Narrative.Groups

	if len(groups) != 1 {
		t.Fatalf("expected a single fallback group for a session with no user messages, got %d: %+v", len(groups), groups)
	}
	if len(groups[0].Events) != 2 {
		t.Fatalf("expected the fallback group to hold every event, got %d: %+v", len(groups[0].Events), groups[0].Events)
	}
}

// TestHandleUIPhaseTelemetryFragmentRouteRegistered covers that the
// telemetry fragment path resolves through the existing /phases/ route
// without requiring a database connection.
func TestBuildTelemetryNarrativeView_RequiresSemanticProvenance(t *testing.T) {
	at := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	messages := []db.AgentMessage{
		{Seq: 1, Role: "user", InputSource: "harness", Content: strp("Synthetic Cerberus phase prompt"), CreatedAt: at},
		{Seq: 2, Role: "user", InputSource: "extension", Content: strp("Extension-injected context"), CreatedAt: at.Add(time.Second)},
		{Seq: 3, Role: "user", InputSource: "unknown", Content: strp("historical prompt"), CreatedAt: at.Add(2 * time.Second)},
		{Seq: 4, Role: "user", InputSource: "interactive", Content: strp("human goal"), CreatedAt: at.Add(3 * time.Second)},
		// Aborted/error text is evidence but not a delivered outcome.
		{Seq: 7, Role: "assistant", IsFinal: false, Content: strp("partial before abort"), CreatedAt: at.Add(6 * time.Second)},
		{Seq: 9, Role: "assistant", IsFinal: true, Content: strp("terminal result"), CreatedAt: at.Add(8 * time.Second)},
		{Seq: 10, Role: "assistant", Content: strp("later internal note"), CreatedAt: at.Add(9 * time.Second)},
	}
	turns := []db.AgentTurn{
		{Seq: 5, TurnIndex: i64p(11), StopReason: "toolUse", Ts: at.Add(4 * time.Second)},
		{Seq: 6, TurnIndex: i64p(12), StopReason: "aborted", Ts: at.Add(5 * time.Second)},
		{Seq: 8, TurnIndex: i64p(13), StopReason: "error", Ts: at.Add(7 * time.Second)},
	}
	sv := buildTelemetrySessionView(db.AgentSession{StartedAt: at}, turns, nil, messages)
	n := sv.Narrative
	if n.FirstUserRequest != "human goal" || n.LatestAssistantOutcome != "terminal result" {
		t.Fatalf("semantic narrative/finality = %+v", n)
	}
	if !n.GoalProvenanceUnknown || !n.OutcomeProvenanceUnknown {
		t.Fatalf("unknown historical provenance was not exposed: %+v", n)
	}
	if n.ModelCallCount != 3 || len(n.Groups) != 2 || n.Groups[1].UserPreview != "human goal" || n.Groups[1].AssistantPreview != "terminal result" {
		t.Fatalf("semantic correlation/grouping = %+v", n)
	}
	if sv.Events[0].InputSource != "harness" || sv.Events[1].InputSource != "extension" || sv.Events[3].InputSource != "interactive" {
		t.Fatalf("input provenance was not preserved in narrative events: %+v", sv.Events)
	}
}

func TestBuildTelemetryNarrativeView_PrivacyPartial(t *testing.T) {
	at := time.Now()
	n := buildTelemetrySessionView(db.AgentSession{StartedAt: at}, nil, nil, []db.AgentMessage{
		{Seq: 1, Role: "user", InputSource: "interactive", ContentRedacted: true, CreatedAt: at},
		{Seq: 2, Role: "assistant", IsFinal: true, ContentTruncated: true, CreatedAt: at},
	}).Narrative
	if n.HasUserRequest || n.HasAssistantOutcome || !n.GoalPartial || !n.OutcomePartial {
		t.Fatalf("privacy-filtered narrative = %+v, want unavailable partial facts", n)
	}
}

func TestHandleUIPhaseTelemetryFragmentRouteRegistered(t *testing.T) {
	mux, _ := newTestMux(t)
	if pattern := registeredPattern(mux, "GET", "/phases/1/telemetry/fragment"); pattern == "" {
		t.Fatalf("expected a route registered for telemetry fragment path, got none")
	}
}

// TestSummarizeToolCall_KnownTools covers the compact, tool-specific
// summaries for the read/bash/edit/write schemas: a JSON-object input
// with a path/command field is reduced to a short human-readable line,
// optionally including a bounded result signal (byte/line count).
func TestSummarizeToolCall_KnownTools(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		input  *string
		result *string
		want   []string // all substrings must appear
	}{
		{
			name:   "read",
			tool:   "read",
			input:  strp(`{"path":"/tmp/example.txt"}`),
			result: strp("hello world"),
			want:   []string{"read /tmp/example.txt", "11 byte(s)"},
		},
		{
			name:   "bash",
			tool:   "bash",
			input:  strp(`{"command":"ls -la"}`),
			result: strp("a.txt\nb.txt\n"),
			want:   []string{"$ ls -la", "2 output line(s)"},
		},
		{
			name:  "edit",
			tool:  "edit",
			input: strp(`{"file_path":"main.go","old_string":"foo","new_string":"bar"}`),
			want:  []string{"edit main.go"},
		},
		{
			name:  "write",
			tool:  "write",
			input: strp(`{"path":"out.txt","content":"12345"}`),
			want:  []string{"write out.txt", "5 byte(s)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeToolCall(tc.tool, tc.input, tc.result)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("summarizeToolCall(%q) = %q, want to contain %q", tc.tool, got, want)
				}
			}
		})
	}
}

// TestSummarizeToolCall_UnknownToolSchema covers that a tool name this
// view has no dedicated schema for still produces a bounded, safe
// summary: both when the input is non-JSON plain text and when it is a
// JSON object with none of the recognised field names, summarizeToolCall
// must not panic and must fall back to a generic "name: input" summary.
func TestSummarizeToolCall_UnknownToolSchema(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		input *string
	}{
		{name: "plain text input", tool: "grep", input: strp("pattern across the repo")},
		{name: "json without known fields", tool: "custom_tool", input: strp(`{"foo":"bar","n":5}`)},
		{name: "malformed json", tool: "custom_tool", input: strp(`{not-json`)},
		{name: "nil input", tool: "noop", input: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeToolCall(tc.tool, tc.input, nil)
			if !strings.HasPrefix(got, tc.tool) {
				t.Fatalf("summarizeToolCall(%q, %v) = %q, want a summary starting with the tool name", tc.tool, tc.input, got)
			}
			if len([]rune(got)) > toolSummaryMaxLen+len(tc.tool)+3 {
				t.Fatalf("summarizeToolCall(%q) produced an unbounded summary: %d runes", tc.tool, len([]rune(got)))
			}
		})
	}
}

// TestSummarizeToolCall_BoundsLargePayload covers the core reader-first
// requirement: a 100+ KiB tool input/result (well beyond anything a
// reader-first overview should ever render inline) must still produce a
// bounded summary that neither contains nor approaches the size of the
// original payload, for both a known tool schema (read) and an unknown
// one.
func TestSummarizeToolCall_BoundsLargePayload(t *testing.T) {
	huge := strings.Repeat("x", 100*1024+37) // 100+ KiB, single line

	t.Run("known tool, huge plain-text input", func(t *testing.T) {
		got := summarizeToolCall("read", &huge, &huge)
		if len([]rune(got)) > toolSummaryMaxLen+32 {
			t.Fatalf("expected a bounded summary, got %d runes", len([]rune(got)))
		}
		if strings.Contains(got, huge) {
			t.Fatalf("expected the huge payload not to appear verbatim in the summary")
		}
	})

	t.Run("unknown tool, huge JSON input", func(t *testing.T) {
		hugeJSON := `{"blob":"` + huge + `"}`
		got := summarizeToolCall("mystery_tool", &hugeJSON, nil)
		if len([]rune(got)) > toolSummaryMaxLen+32 {
			t.Fatalf("expected a bounded summary, got %d runes", len([]rune(got)))
		}
		if strings.Contains(got, huge) {
			t.Fatalf("expected the huge payload not to appear verbatim in the summary")
		}
	})

	t.Run("multiline huge input is clipped to first line", func(t *testing.T) {
		multiline := strings.Repeat("y", 200*1024) + "\nsecond line should never surface"
		got := summarizeToolCall("bash", &multiline, nil)
		if strings.Contains(got, "second line") {
			t.Fatalf("expected only the first line to feed the bounded summary, got %q", got)
		}
		if len([]rune(got)) > toolSummaryMaxLen+32 {
			t.Fatalf("expected a bounded summary, got %d runes", len([]rune(got)))
		}
	})
}

// TestToolCallEventTime_PrefersFinishedAt covers that a tool_call
// event's display timestamp (At) uses the tool's completion time when
// the producer recorded one, and falls back to the invocation time
// (CreatedAt) when it did not.
func TestToolCallEventTime_PrefersFinishedAt(t *testing.T) {
	created := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	finished := created.Add(5 * time.Minute)

	sess := db.AgentSession{ID: 1, Session: "sess-timing", StartedAt: created}
	toolCalls := []db.AgentToolCall{
		{AgentSessionID: 1, Seq: 1, ToolName: "bash", ToolResult: strp("ok"), CreatedAt: created, FinishedAt: &finished},
		{AgentSessionID: 1, Seq: 2, ToolName: "bash", ToolResult: strp("ok"), CreatedAt: created.Add(time.Second)},
	}

	sv := buildTelemetrySessionView(sess, nil, toolCalls, nil)
	if len(sv.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(sv.Events))
	}
	if !sv.Events[0].At.Equal(finished) {
		t.Fatalf("event[0].At = %v, want the tool's FinishedAt (%v)", sv.Events[0].At, finished)
	}
	wantFallback := created.Add(time.Second)
	if !sv.Events[1].At.Equal(wantFallback) {
		t.Fatalf("event[1].At = %v, want CreatedAt fallback (%v) when FinishedAt is nil", sv.Events[1].At, wantFallback)
	}
}

// TestSessionDetailTemplate_OverviewNeverRendersLargePayload covers the
// end-to-end template contract: a session with a 100+ KiB tool result
// must render a bounded compact summary in the Overview section, while
// the full escaped payload only ever appears inside the collapsed "Raw
// evidence" disclosure — never in the reader-first overview above it.
func TestSessionDetailTemplate_OverviewNeverRendersLargePayload(t *testing.T) {
	started := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 1, Session: "sess-large", StartedAt: started}

	hugeResult := strings.Repeat("R", 100*1024+91)
	messages := []db.AgentMessage{
		{AgentSessionID: 1, Seq: 1, Role: "user", InputSource: "interactive", Content: strp("please read the giant file"), CreatedAt: started},
	}
	toolCalls := []db.AgentToolCall{
		{
			AgentSessionID: 1, Seq: 2, ToolName: "read",
			ToolInput:  strp(`{"path":"/tmp/giant.log"}`),
			ToolResult: strp(hugeResult),
			CreatedAt:  started.Add(time.Second),
		},
	}

	sv := buildTelemetrySessionView(sess, nil, toolCalls, messages)

	found := sessionOverviewView{
		KnownCerberusSession: db.KnownCerberusSession{Session: "sess-large", Type: "telemetry", FoundryStatus: "running"},
		HasAttribution:       false,
		HasTelemetry:         true,
		AgentSession:         sess,
	}

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "sessions.detail", struct {
		Session   sessionOverviewView
		Telemetry *telemetrySessionView
	}{found, &sv}); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "read /tmp/giant.log") {
		t.Fatalf("expected the bounded tool summary in the overview, got:\n%s", out)
	}
	if !strings.Contains(out, "Raw evidence") {
		t.Fatalf("expected a Raw evidence disclosure, got:\n%s", out)
	}
	if !strings.Contains(out, hugeResult) {
		t.Fatalf("expected the full payload to still be present in the Raw evidence disclosure, got a truncated document")
	}

	overviewEnd := strings.Index(out, "Raw evidence")
	if overviewEnd < 0 {
		t.Fatalf("could not locate the Raw evidence marker")
	}
	if strings.Contains(out[:overviewEnd], hugeResult) {
		t.Fatalf("expected the huge payload not to appear before the Raw evidence disclosure (i.e. in the overview)")
	}
}

// TestSessionDetailTemplate_ModelCallsLabel covers that the "Turns"
// label was renamed to "Model calls" in both the overview and the Raw
// evidence disclosure of the session detail template.
func TestSessionDetailTemplate_ModelCallsLabel(t *testing.T) {
	started := time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 1, Session: "sess-labels", StartedAt: started}
	turns := []db.AgentTurn{{AgentSessionID: 1, Seq: 1, InputTokens: 1, OutputTokens: 1, Ts: started}}
	sv := buildTelemetrySessionView(sess, turns, nil, nil)

	found := sessionOverviewView{
		KnownCerberusSession: db.KnownCerberusSession{Session: "sess-labels", Type: "telemetry", FoundryStatus: "running"},
		HasTelemetry:         true,
		AgentSession:         sess,
	}

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "sessions.detail", struct {
		Session   sessionOverviewView
		Telemetry *telemetrySessionView
	}{found, &sv}); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Model calls: 1") {
		t.Fatalf("expected a \"Model calls\" label, got:\n%s", out)
	}
	if strings.Contains(out, "Turns:") {
		t.Fatalf("expected the legacy \"Turns:\" label to be gone, got:\n%s", out)
	}
}

// TestBuildTelemetryNarrativeView_BoundsLargeGoalAndOutcome covers that
// a session's Goal (first user request) and Latest outcome (latest
// assistant message) are bounded independently of how large the
// underlying message content is: a 100+ KiB user/assistant message
// must still produce a preview no longer than narrativePreviewMaxLen
// (plus the ellipsis marker), never the raw payload verbatim, while the
// full content remains reachable through the untouched event Payload
// (and therefore the Raw evidence disclosure).
func TestBuildTelemetryNarrativeView_BoundsLargeGoalAndOutcome(t *testing.T) {
	started := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 1, Session: "sess-large-narrative", StartedAt: started}

	hugeUser := strings.Repeat("u", 100*1024+11)
	hugeAssistant := strings.Repeat("a", 100*1024+22)
	messages := []db.AgentMessage{
		{AgentSessionID: 1, Seq: 1, Role: "user", InputSource: "interactive", Content: strp(hugeUser), CreatedAt: started},
		{AgentSessionID: 1, Seq: 2, Role: "assistant", IsFinal: true, Content: strp(hugeAssistant), CreatedAt: started.Add(time.Second)},
	}

	sv := buildTelemetrySessionView(sess, nil, nil, messages)
	nv := sv.Narrative

	if !nv.HasUserRequest {
		t.Fatalf("expected HasUserRequest = true")
	}
	if got := len([]rune(nv.FirstUserRequest)); got > narrativePreviewMaxLen+1 {
		t.Fatalf("FirstUserRequest is %d runes, want a bounded preview (<= %d)", got, narrativePreviewMaxLen+1)
	}
	if nv.FirstUserRequest == hugeUser {
		t.Fatalf("expected FirstUserRequest not to be the raw payload verbatim")
	}

	if !nv.HasAssistantOutcome {
		t.Fatalf("expected HasAssistantOutcome = true")
	}
	if got := len([]rune(nv.LatestAssistantOutcome)); got > narrativePreviewMaxLen+1 {
		t.Fatalf("LatestAssistantOutcome is %d runes, want a bounded preview (<= %d)", got, narrativePreviewMaxLen+1)
	}
	if nv.LatestAssistantOutcome == hugeAssistant {
		t.Fatalf("expected LatestAssistantOutcome not to be the raw payload verbatim")
	}

	// The full content is still reachable through the untouched event
	// timeline, i.e. what the Raw evidence disclosure renders.
	var sawFullUser, sawFullAssistant bool
	for _, ev := range sv.Events {
		if ev.Payload == hugeUser {
			sawFullUser = true
		}
		if ev.Payload == hugeAssistant {
			sawFullAssistant = true
		}
	}
	if !sawFullUser || !sawFullAssistant {
		t.Fatalf("expected the full message content to remain available via Events, sawFullUser=%v sawFullAssistant=%v", sawFullUser, sawFullAssistant)
	}
}

// TestBuildTelemetryConversationGroups_BoundsLargeExchangePreviews
// covers that each conversational exchange's User/AssistantPreview is
// bounded the same way as the session-level Goal/Latest outcome, for a
// group whose messages are each 100+ KiB.
func TestBuildTelemetryConversationGroups_BoundsLargeExchangePreviews(t *testing.T) {
	started := time.Date(2024, 9, 2, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 1, Session: "sess-large-exchange", StartedAt: started}

	hugeUser := strings.Repeat("q", 100*1024+7)
	hugeAssistant := strings.Repeat("r", 100*1024+9)
	messages := []db.AgentMessage{
		{AgentSessionID: 1, Seq: 1, Role: "user", InputSource: "interactive", Content: strp(hugeUser), CreatedAt: started},
		{AgentSessionID: 1, Seq: 2, Role: "assistant", IsFinal: true, Content: strp(hugeAssistant), CreatedAt: started.Add(time.Second)},
	}

	sv := buildTelemetrySessionView(sess, nil, nil, messages)
	groups := sv.Narrative.Groups
	if len(groups) != 1 {
		t.Fatalf("expected 1 conversational group, got %d", len(groups))
	}
	g := groups[0]

	if !g.HasUserPreview || len([]rune(g.UserPreview)) > narrativePreviewMaxLen+1 || g.UserPreview == hugeUser {
		t.Fatalf("UserPreview = %q (has=%v, len=%d), want a bounded, non-verbatim preview", g.UserPreview, g.HasUserPreview, len([]rune(g.UserPreview)))
	}
	if !g.HasAssistantPreview || len([]rune(g.AssistantPreview)) > narrativePreviewMaxLen+1 || g.AssistantPreview == hugeAssistant {
		t.Fatalf("AssistantPreview = %q (has=%v, len=%d), want a bounded, non-verbatim preview", g.AssistantPreview, g.HasAssistantPreview, len([]rune(g.AssistantPreview)))
	}
}

// TestBuildTelemetryNarrativeView_OutOfOrderToolCompletionWinsLastActivity
// covers that the last completed activity is chosen by latest
// completion timestamp, not highest sequence number: a tool call
// allocated an early sequence number (because it was invoked first) but
// whose result/completion arrives after every other event in the
// timeline must still be reported as the last completed activity, even
// though later-sequenced events completed earlier.
func TestBuildTelemetryNarrativeView_OutOfOrderToolCompletionWinsLastActivity(t *testing.T) {
	started := time.Date(2024, 9, 3, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 1, Session: "sess-out-of-order", StartedAt: started}

	slowFinished := started.Add(10 * time.Minute)
	toolCalls := []db.AgentToolCall{
		// Seq 1: invoked first (low sequence number), but its result
		// doesn't finish until well after every other event below.
		{AgentSessionID: 1, Seq: 1, ToolName: "bash", ToolResult: strp("slow result"), CreatedAt: started, FinishedAt: &slowFinished},
	}
	messages := []db.AgentMessage{
		// Seq 2: a later-sequenced message that nonetheless completes
		// well before the slow tool call above finishes.
		{AgentSessionID: 1, Seq: 2, Role: "assistant", IsFinal: true, Content: strp("quick reply"), CreatedAt: started.Add(time.Second)},
	}

	sv := buildTelemetrySessionView(sess, nil, toolCalls, messages)
	nv := sv.Narrative

	if !nv.HasActivity {
		t.Fatalf("expected HasActivity = true")
	}
	if nv.LastActivityLabel != "Tool: bash" {
		t.Fatalf("LastActivityLabel = %q, want the slow tool call (\"Tool: bash\"), which completed last despite its lower sequence number", nv.LastActivityLabel)
	}
	if !nv.LastActivityAt.Equal(slowFinished) {
		t.Fatalf("LastActivityAt = %v, want %v (the tool's FinishedAt)", nv.LastActivityAt, slowFinished)
	}
}

// TestSummarizeToolCall (compact tool overview fields) covers that a
// tool_call event's compact overview data — (success/error, duration,
// input/result byte sizes, bounded result preview) are populated and
// bounded regardless of payload size, complementing the tool-specific
// summary text already covered by TestSummarizeToolCall_KnownTools.
func TestBuildTelemetrySessionView_CompactToolFields(t *testing.T) {
	started := time.Date(2024, 9, 4, 0, 0, 0, 0, time.UTC)
	sess := db.AgentSession{ID: 1, Session: "sess-compact-tools", StartedAt: started}

	hugeResult := strings.Repeat("z", 100*1024+13)
	toolCalls := []db.AgentToolCall{
		{
			AgentSessionID: 1, Seq: 1, ToolName: "bash",
			ToolInput:  strp(`{"command":"ls"}`),
			ToolResult: strp(hugeResult),
			IsError:    boolp(true),
			DurationMs: i64p(7),
			CreatedAt:  started,
		},
	}

	sv := buildTelemetrySessionView(sess, nil, toolCalls, nil)
	if len(sv.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sv.Events))
	}
	ev := sv.Events[0]

	if !ev.IsError {
		t.Fatalf("expected IsError = true")
	}
	if ev.DurationLabel != "7ms" {
		t.Fatalf("DurationLabel = %q, want %q", ev.DurationLabel, "7ms")
	}
	if ev.ToolInputBytes != int64(len(`{"command":"ls"}`)) {
		t.Fatalf("ToolInputBytes = %d, want %d", ev.ToolInputBytes, len(`{"command":"ls"}`))
	}
	if ev.ToolResultBytes != int64(len(hugeResult)) {
		t.Fatalf("ToolResultBytes = %d, want %d", ev.ToolResultBytes, len(hugeResult))
	}
	if got := len([]rune(ev.ToolResultPreview)); got > toolSummaryMaxLen+1 {
		t.Fatalf("ToolResultPreview is %d runes, want a bounded preview", got)
	}
	if ev.ToolResultPreview == hugeResult {
		t.Fatalf("expected ToolResultPreview not to be the raw payload verbatim")
	}

	// The reader-first overview (rendered via telemetry.toolSummaries,
	// as the session detail Overview does) must show the bounded
	// success/error status, duration, byte sizes, and result preview,
	// but never the raw huge payload.
	found := sessionOverviewView{
		KnownCerberusSession: db.KnownCerberusSession{Session: sess.Session, Type: "telemetry", FoundryStatus: "running"},
		HasTelemetry:         true,
		AgentSession:         sess,
	}
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "sessions.detail", struct {
		Session   sessionOverviewView
		Telemetry *telemetrySessionView
	}{found, &sv}); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()

	overviewEnd := strings.Index(out, "Raw evidence")
	if overviewEnd < 0 {
		t.Fatalf("could not locate the Raw evidence marker")
	}
	overview := out[:overviewEnd]

	if !strings.Contains(overview, "error") {
		t.Fatalf("expected error status rendered in the overview, got:\n%s", overview)
	}
	if !strings.Contains(overview, "7ms") {
		t.Fatalf("expected the tool duration rendered in the overview, got:\n%s", overview)
	}
	if !strings.Contains(overview, fmt.Sprintf("%d", len(hugeResult))) {
		t.Fatalf("expected the result byte size rendered in the overview, got:\n%s", overview)
	}
	if strings.Contains(overview, hugeResult) {
		t.Fatalf("expected the bounded result preview, not the raw huge payload, in the overview")
	}
	if !strings.Contains(out, hugeResult) {
		t.Fatalf("expected the full payload to still be reachable in the Raw evidence disclosure")
	}
}
