package webui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

// TestMergeSessionOverviews_OptionalAttribution covers the three merge
// outcomes: a known (lifecycle-attributed) session with matching
// telemetry, a known session with no telemetry recorded yet, and a
// telemetry-only agent session with no lifecycle attribution at all.
func TestMergeSessionOverviews_OptionalAttribution(t *testing.T) {
	known := []cerberusSessionView{
		{
			KnownCerberusSession: db.KnownCerberusSession{
				Session:       "sess-attributed-with-telemetry",
				Type:          "workflow_phase",
				FoundryStatus: "done",
				PhaseName:     "implement",
				LastUpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				SafeToClean:   true,
			},
			CerberusStatus: "idle",
		},
		{
			KnownCerberusSession: db.KnownCerberusSession{
				Session:       "sess-attributed-no-telemetry",
				Type:          "spec_draft",
				FoundryStatus: "active",
				DraftTitle:    "draft one",
				LastUpdatedAt: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
			},
		},
	}
	agents := []db.AgentSession{
		{ID: 1, Session: "sess-attributed-with-telemetry", Origin: "cli", Kind: "primary"},
		{ID: 2, Session: "sess-telemetry-only", Origin: "cli", Kind: "coding", StartedAt: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)},
	}

	got := mergeSessionOverviews(known, agents)
	if len(got) != 3 {
		t.Fatalf("mergeSessionOverviews() returned %d entries, want 3: %+v", len(got), got)
	}

	byName := make(map[string]sessionOverviewView, len(got))
	for _, v := range got {
		byName[v.Session] = v
	}

	withTelemetry, ok := byName["sess-attributed-with-telemetry"]
	if !ok {
		t.Fatalf("missing attributed+telemetry session in %+v", got)
	}
	if !withTelemetry.HasAttribution || !withTelemetry.HasTelemetry {
		t.Fatalf("sess-attributed-with-telemetry = %+v, want HasAttribution=true HasTelemetry=true", withTelemetry)
	}
	if withTelemetry.AgentSession.ID != 1 {
		t.Fatalf("sess-attributed-with-telemetry.AgentSession.ID = %d, want 1", withTelemetry.AgentSession.ID)
	}

	noTelemetry, ok := byName["sess-attributed-no-telemetry"]
	if !ok {
		t.Fatalf("missing attributed, no-telemetry session in %+v", got)
	}
	if !noTelemetry.HasAttribution || noTelemetry.HasTelemetry {
		t.Fatalf("sess-attributed-no-telemetry = %+v, want HasAttribution=true HasTelemetry=false", noTelemetry)
	}

	telemetryOnly, ok := byName["sess-telemetry-only"]
	if !ok {
		t.Fatalf("missing telemetry-only session in %+v", got)
	}
	if telemetryOnly.HasAttribution {
		t.Fatalf("sess-telemetry-only = %+v, want HasAttribution=false", telemetryOnly)
	}
	if !telemetryOnly.HasTelemetry || telemetryOnly.AgentSession.ID != 2 {
		t.Fatalf("sess-telemetry-only telemetry not attached correctly: %+v", telemetryOnly)
	}
	if telemetryOnly.PhaseName != "" || telemetryOnly.SpecTitle != "" || telemetryOnly.DraftTitle != "" {
		t.Fatalf("sess-telemetry-only = %+v, want no phase/spec/draft attribution fields", telemetryOnly)
	}
	if telemetryOnly.Type != "telemetry" {
		t.Fatalf("sess-telemetry-only.Type = %q, want %q", telemetryOnly.Type, "telemetry")
	}
	if telemetryOnly.UnsafeReason == "" {
		t.Fatalf("sess-telemetry-only.UnsafeReason = empty, want an explanation for why it isn't attributed")
	}

	// Most recent LastUpdatedAt first.
	if got[0].Session != "sess-telemetry-only" {
		t.Fatalf("got[0].Session = %q, want the most recently updated session first: %+v", got[0].Session, got)
	}
}

// TestMergeSessionOverviews_DuplicateIdentity covers that a session
// identity appearing more than once among the known (lifecycle-attributed)
// sources is deduplicated, keeping the first occurrence, rather than
// producing duplicate rows.
func TestMergeSessionOverviews_DuplicateIdentity(t *testing.T) {
	known := []cerberusSessionView{
		{KnownCerberusSession: db.KnownCerberusSession{Session: "sess-dup", Type: "workflow_phase", PhaseName: "first-seen"}},
		{KnownCerberusSession: db.KnownCerberusSession{Session: "sess-dup", Type: "external", PhaseName: "should-be-dropped"}},
	}

	got := mergeSessionOverviews(known, nil)
	if len(got) != 1 {
		t.Fatalf("mergeSessionOverviews() returned %d entries, want 1 (deduplicated): %+v", len(got), got)
	}
	if got[0].PhaseName != "first-seen" {
		t.Fatalf("got[0].PhaseName = %q, want %q (first occurrence wins)", got[0].PhaseName, "first-seen")
	}
}

// TestMergeSessionOverviews_Empty covers that merging with no known
// sessions and no agent sessions produces an empty, non-nil slice.
func TestMergeSessionOverviews_Empty(t *testing.T) {
	got := mergeSessionOverviews(nil, nil)
	if got == nil {
		t.Fatal("mergeSessionOverviews(nil, nil) = nil, want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("mergeSessionOverviews(nil, nil) = %+v, want empty", got)
	}
}

// TestSessionsDetailTemplate_RendersEscapedTelemetryEvidence covers that
// the session detail page renders the same collapsed turn/tool/message
// evidence blocks used by the phase telemetry panel (via the shared
// "telemetry.events" template), escaping payload content and rendering
// sensibly when a session has no recorded telemetry at all.
func TestSessionsDetailTemplate_RendersEscapedTelemetryEvidence(t *testing.T) {
	sess := sessionOverviewView{
		KnownCerberusSession: db.KnownCerberusSession{
			Session:       "sess-detail-escape",
			Type:          "workflow_phase",
			FoundryStatus: "running",
			LastUpdatedAt: time.Now(),
		},
		HasAttribution: true,
		HasTelemetry:   true,
	}

	toolCalls := []db.AgentToolCall{
		{
			AgentSessionID: 1,
			Seq:            1,
			ToolName:       "bash",
			ToolInput:      strp("<img src=x onerror=alert(1)>"),
			ToolResult:     strp("done"),
			CreatedAt:      time.Now(),
		},
	}
	tv := buildTelemetrySessionView(db.AgentSession{ID: 1, Session: sess.Session, StartedAt: time.Now()}, nil, toolCalls, nil)

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "sessions.detail", struct {
		Session   sessionOverviewView
		Telemetry *telemetrySessionView
	}{sess, &tv}); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()

	if strings.Contains(out, "<img src=x onerror=alert(1)>") {
		t.Fatalf("expected tool input to be escaped, got:\n%s", out)
	}
	if !strings.Contains(out, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Fatalf("expected escaped tool input in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Tool: bash") {
		t.Fatalf("expected tool call evidence title, got:\n%s", out)
	}

	// Empty-metadata case: no telemetry at all must render the empty
	// state, not panic or emit a nil-dereference.
	var emptyBuf bytes.Buffer
	if err := templates.ExecuteTemplate(&emptyBuf, "sessions.detail", struct {
		Session   sessionOverviewView
		Telemetry *telemetrySessionView
	}{sess, nil}); err != nil {
		t.Fatalf("ExecuteTemplate() with nil Telemetry error = %v", err)
	}
	if !strings.Contains(emptyBuf.String(), "No agent telemetry recorded for this session.") {
		t.Fatalf("expected empty-telemetry message, got:\n%s", emptyBuf.String())
	}
}

// TestSessionsDetailTemplate_RendersNarrative covers that the session
// detail page surfaces the narrative projection (first user request,
// latest assistant outcome, last completed activity, aggregates, and
// conversational groups) derived from the session's curated telemetry
// rows, and that a session with no user message renders the honest
// fallback rather than a fabricated request.
func TestSessionsDetailTemplate_RendersNarrative(t *testing.T) {
	started := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	sess := sessionOverviewView{
		KnownCerberusSession: db.KnownCerberusSession{
			Session:       "sess-narrative-detail",
			Type:          "telemetry",
			FoundryStatus: "done",
			LastUpdatedAt: started,
		},
		HasTelemetry: true,
	}
	messages := []db.AgentMessage{
		{AgentSessionID: 1, Seq: 1, Role: "user", Content: strp("please summarize the repo"), CreatedAt: started},
		{AgentSessionID: 1, Seq: 2, Role: "assistant", Content: strp("the repo has three packages"), CreatedAt: started.Add(time.Second)},
	}
	tv := buildTelemetrySessionView(db.AgentSession{ID: 1, Session: sess.Session, StartedAt: started}, nil, nil, messages)

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "sessions.detail", struct {
		Session   sessionOverviewView
		Telemetry *telemetrySessionView
	}{sess, &tv}); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"please summarize the repo",
		"the repo has three packages",
		"Exchange 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in rendered narrative, got:\n%s", want, out)
		}
	}

	// A session with no user message must render the honest fallback,
	// not a fabricated request.
	noUserTV := buildTelemetrySessionView(db.AgentSession{ID: 2, Session: "sess-no-user", StartedAt: started}, nil, nil, []db.AgentMessage{
		{AgentSessionID: 2, Seq: 1, Role: "assistant", Content: strp("an assistant-only note"), CreatedAt: started},
	})
	var fallbackBuf bytes.Buffer
	if err := templates.ExecuteTemplate(&fallbackBuf, "sessions.detail", struct {
		Session   sessionOverviewView
		Telemetry *telemetrySessionView
	}{sess, &noUserTV}); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	if !strings.Contains(fallbackBuf.String(), "(no user request recorded for this session)") {
		t.Fatalf("expected honest fallback for a session with no user message, got:\n%s", fallbackBuf.String())
	}
}

// list/detail fragment routes resolve through the mux without requiring
// a database connection.
// TestSessionsDetailTemplate_AttributedSessionShowsLiveStreamAndCleanup
// covers that an attributed (lifecycle-known) Cerberus session still
// renders the live-stream panel and the cleanup control, unchanged from
// before telemetry-only sessions were introduced.
func TestSessionsDetailTemplate_AttributedSessionShowsLiveStreamAndCleanup(t *testing.T) {
	sess := sessionOverviewView{
		KnownCerberusSession: db.KnownCerberusSession{
			Session:       "sess-attributed",
			Type:          "workflow_phase",
			FoundryStatus: "running",
			LastUpdatedAt: time.Now(),
			SafeToClean:   true,
		},
		CerberusStatus: "idle",
		HasAttribution: true,
	}

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "sessions.detail", struct {
		Session   sessionOverviewView
		Telemetry *telemetrySessionView
	}{sess, nil}); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Live activity") || !strings.Contains(out, "data-session-log") {
		t.Fatalf("expected attributed session to render the live-activity stream panel, got:\n%s", out)
	}
	if !strings.Contains(out, "Cerberus session stream") {
		t.Fatalf("expected attributed session to describe the Cerberus live stream, got:\n%s", out)
	}
	if !strings.Contains(out, "Cerberus status") {
		t.Fatalf("expected attributed session to show Cerberus status, got:\n%s", out)
	}
	if !strings.Contains(out, "btn-danger") || !strings.Contains(out, "Clean session") {
		t.Fatalf("expected attributed session to render the cleanup control, got:\n%s", out)
	}
	if strings.Contains(out, "Producer") {
		t.Fatalf("attributed session should not render the telemetry-only Producer fact, got:\n%s", out)
	}
}

// TestSessionsDetailTemplate_TelemetryOnlySessionHidesLiveStreamAndCleanup
// covers that a telemetry-only session (no lifecycle record) hides the
// Cerberus live-stream panel and the cleanup control, accurately
// describes its telemetry producer (e.g. Pi), and shows the
// curated/truncation fidelity notice on its recorded evidence.
func TestSessionsDetailTemplate_TelemetryOnlySessionHidesLiveStreamAndCleanup(t *testing.T) {
	sess := sessionOverviewView{
		KnownCerberusSession: db.KnownCerberusSession{
			Session:       "sess-telemetry-only",
			Type:          "telemetry",
			FoundryStatus: "running",
			LastUpdatedAt: time.Now(),
			UnsafeReason:  "no workflow phase, spec draft, or external record attributes this session",
		},
		HasAttribution: false,
		HasTelemetry:   true,
		AgentSession:   db.AgentSession{ID: 7, Session: "sess-telemetry-only", Origin: "pi", Kind: "coding", StartedAt: time.Now()},
	}

	tv := buildTelemetrySessionView(sess.AgentSession, nil, nil, nil)

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "sessions.detail", struct {
		Session   sessionOverviewView
		Telemetry *telemetrySessionView
	}{sess, &tv}); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	out := buf.String()

	if strings.Contains(out, "data-session-log") {
		t.Fatalf("expected telemetry-only session to hide the live Cerberus stream panel, got:\n%s", out)
	}
	if strings.Contains(out, "btn-danger") || strings.Contains(out, "Clean session") {
		t.Fatalf("expected telemetry-only session to hide the cleanup control, got:\n%s", out)
	}
	if strings.Contains(out, "Cerberus status") {
		t.Fatalf("expected telemetry-only session to hide the Cerberus status fact, got:\n%s", out)
	}
	if !strings.Contains(out, "Producer") || !strings.Contains(out, "pi/coding") {
		t.Fatalf("expected telemetry-only session to accurately describe its producer (pi/coding), got:\n%s", out)
	}
	if !strings.Contains(out, "produced by pi/coding") {
		t.Fatalf("expected recorded telemetry section to attribute the producer, got:\n%s", out)
	}
	if !strings.Contains(out, "telemetry-fidelity-notice") || !strings.Contains(out, "SHA-256") {
		t.Fatalf("expected telemetry-only session to show the curated/truncation fidelity notice, got:\n%s", out)
	}
}

func TestHandleUISessionsFragmentRouteRegistered(t *testing.T) {
	mux, _ := newTestMux(t)
	for _, path := range []string{"/sessions", "/sessions/fragment"} {
		if pattern := registeredPattern(mux, "GET", path); pattern == "" {
			t.Fatalf("expected a route registered for %q, got none", path)
		}
	}
}
