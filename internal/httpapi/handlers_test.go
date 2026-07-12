package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/authoring"
	"github.com/tonis2/foundry/internal/chat"
	"github.com/tonis2/foundry/internal/db"
)

func TestSettingsPatchSeparatesRuntimeKeys(t *testing.T) {
	if !isRuntimeSetting("git_root") || !isRuntimeSetting("cerberus_profile") || !isRuntimeSetting("cerberus_model") || !isRuntimeSetting("default_workflow_budget_usd") {
		t.Fatalf("runtime settings keys not recognized")
	}
	if isRuntimeSetting("server_port") || isRuntimeSetting("db_url") {
		t.Fatalf("db_url and server_port should remain config-backed")
	}
}

func TestMergeYAMLRuntimeSettingsOverridesAndAppendsDBValues(t *testing.T) {
	got := mergeYAMLRuntimeSettings("db_url: old\ngit_root: /old\n", map[string]string{"git_root": "/db/git", "cerberus_profile": "prof"})
	for _, want := range []string{"git_root: \"/db/git\"", "cerberus_profile: \"prof\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("merged settings missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/old") {
		t.Fatalf("old runtime setting leaked into merged yaml:\n%s", got)
	}
}

func TestPhaseStateTransitionHelpers(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)

	approve := approvePhaseUpdate(now)
	if approve.Status == nil || *approve.Status != "done" || approve.ReviewVerdict == nil || *approve.ReviewVerdict != "pass" || approve.FinishedAt == nil || !approve.FinishedAt.Equal(now) {
		t.Fatalf("approvePhaseUpdate = %#v, want done/pass/finished_at", approve)
	}

	reject := rejectPhaseUpdate(now)
	if reject.Status == nil || *reject.Status != "failed" || reject.ReviewVerdict == nil || *reject.ReviewVerdict != "fail" || reject.FinishedAt == nil || !reject.FinishedAt.Equal(now) {
		t.Fatalf("rejectPhaseUpdate = %#v, want failed/fail/finished_at", reject)
	}

	resume := resumeFailedPhaseUpdate()
	if resume.Status == nil || *resume.Status != "pending" || resume.RetryCount == nil || *resume.RetryCount != 0 {
		t.Fatalf("resumeFailedPhaseUpdate = %#v, want pending with retry count reset", resume)
	}
}

func TestExtractFinalSpecFindsSaveReadySpecInMarkdownFence(t *testing.T) {
	messages := mustDraftMessagesJSON(t, []draftTestMessage{
		{Role: "assistant", Content: "Earlier draft without phases"},
		{Role: "assistant", Content: "Draft #5 is ready to save.\n\n```markdown\n# Draft Studio Robust Save\n\nBuild the save path.\n\n## Phase 1: Extract\n\nFind the spec inside assistant prose.\n```\n\nYou can save this now."},
	})

	got := authoring.ExtractFinalSpec(messages)
	want := "# Draft Studio Robust Save\n\nBuild the save path.\n\n## Phase 1: Extract\n\nFind the spec inside assistant prose."
	if got != want {
		t.Fatalf("authoring.ExtractFinalSpec() = %q, want %q", got, want)
	}
}

func TestExtractFinalSpecFindsSaveReadySpecAfterProse(t *testing.T) {
	messages := mustDraftMessagesJSON(t, []draftTestMessage{
		{Role: "assistant", Content: "This is the save-ready version:\n\n# Draft Five Style Output\n\nProse before the title should not be saved.\n\n## Phase 1: Save\n\nPersist only the markdown spec."},
	})

	got := authoring.ExtractFinalSpec(messages)
	want := "# Draft Five Style Output\n\nProse before the title should not be saved.\n\n## Phase 1: Save\n\nPersist only the markdown spec."
	if got != want {
		t.Fatalf("authoring.ExtractFinalSpec() = %q, want %q", got, want)
	}
}

type draftTestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func mustDraftMessagesJSON(t *testing.T, messages []draftTestMessage) []byte {
	t.Helper()
	b, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBuildFollowUpSpecContentInjectsFailureContextBeforePhases(t *testing.T) {
	review := " needs tests "
	summary := "failed on migration"
	rationale := "constraint violated"
	prompt := strings.Repeat("p", 2100)
	sp := db.Spec{Content: "# Original\n\nGlobal context.\n\n## Phase 1: Build\n\nDo it."}
	wf := db.Workflow{ID: 77}
	failed := []db.Phase{{ID: 9, Position: 2, Name: "Verify", Status: "failed", RetryCount: 3, ReviewVerdict: strPtr("fail"), ReviewNotes: &review, DecisionSummary: &summary, DecisionRationale: &rationale, PromptSent: &prompt}}

	content := buildFollowUpSpecContentWithContext(sp, buildFollowUpFailureContext(context.Background(), wf, failed, nil))

	followIdx := strings.Index(content, "## Follow-up run context")
	phaseIdx := strings.Index(content, "## Phase 1: Build")
	if followIdx == -1 || phaseIdx == -1 || followIdx > phaseIdx {
		t.Fatalf("follow-up context was not injected before phases:\n%s", content)
	}
	for _, want := range []string{"failed workflow #77", "### Failed phase 2: Verify", "- Phase ID: 9", "- Retry count: 3", "> needs tests", "> failed on migration", "Prompt sent excerpt:", "... truncated ..."} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
}

func TestBuildFollowUpSpecContentAppendsWhenSpecHasNoPhases(t *testing.T) {
	sp := db.Spec{Content: "# Original\n\nNo executable phases yet."}
	got := buildFollowUpSpecContentWithContext(sp, "## Follow-up run context\n\nDetails")
	if !strings.HasSuffix(got, "## Follow-up run context\n\nDetails") {
		t.Fatalf("context was not appended to phase-less spec:\n%s", got)
	}
}

func TestBuildFollowUpContextIncludesRecentLogs(t *testing.T) {
	ph := db.Phase{ID: 1, Position: 1, Name: "Test", Status: "failed"}
	got := buildFollowUpFailureContext(context.Background(), db.Workflow{ID: 10}, []db.Phase{ph}, func(context.Context, int64, int) ([]db.PhaseLog, error) {
		return []db.PhaseLog{{Line: " last useful log "}, {Line: ""}}, nil
	})
	if !strings.Contains(got, "Recent log summary (tail):") || !strings.Contains(got, "> last useful log") {
		t.Fatalf("recent logs were not included:\n%s", got)
	}
}

func strPtr(s string) *string { return &s }

func TestHandleChatSessionsCreatePassesProfileThroughAPI(t *testing.T) {
	svc := &fakeChatService{createSession: db.ChatSession{ID: 44, ProfileName: "dev"}}
	h := New(nil, Config{ChatService: func() ChatService { return svc }})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/sessions", bytes.NewBufferString(`{"profile_name":"dev"}`))
	rec := httptest.NewRecorder()

	h.HandleChatSessions(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if svc.createdProfile != "dev" {
		t.Fatalf("created profile = %q, want dev", svc.createdProfile)
	}
}

func TestHandleChatSessionMessagePassesProfileThroughAPI(t *testing.T) {
	svc := &fakeChatService{}
	h := New(nil, Config{ChatService: func() ChatService { return svc }})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/sessions/22/message", bytes.NewBufferString(`{"content":"hello","profile_name":"dev"}`))
	rec := httptest.NewRecorder()

	h.HandleChatSession(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if svc.sentSessionID != 22 || svc.sentContent != "hello" || svc.sentProfile == nil || *svc.sentProfile != "dev" {
		t.Fatalf("sent = id %d content %q profile %#v", svc.sentSessionID, svc.sentContent, svc.sentProfile)
	}
}

func TestHandleChatSessionMessageMapsBusyToConflict(t *testing.T) {
	svc := &fakeChatService{sendErr: chat.ErrSessionBusy}
	h := New(nil, Config{ChatService: func() ChatService { return svc }})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/sessions/22/message", bytes.NewBufferString(`{"content":"hello"}`))
	rec := httptest.NewRecorder()

	h.HandleChatSession(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleChatSessionProjectsThroughAPI(t *testing.T) {
	svc := &fakeChatService{projects: []db.Project{{ID: 7, Name: "repo", RepoPath: "/repo"}}}
	h := New(nil, Config{ChatService: func() ChatService { return svc }})

	rec := httptest.NewRecorder()
	h.HandleChatSession(rec, httptest.NewRequest(http.MethodGet, "/api/chat/sessions/3/projects", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"repo_path":"/repo"`) {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleChatSession(rec, httptest.NewRequest(http.MethodPost, "/api/chat/sessions/3/projects", bytes.NewBufferString(`{"project_id":7}`)))
	if rec.Code != http.StatusNoContent || svc.attachedSessionID != 3 || svc.attachedProjectID != 7 {
		t.Fatalf("POST status = %d attach = %d/%d body = %s", rec.Code, svc.attachedSessionID, svc.attachedProjectID, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleChatSession(rec, httptest.NewRequest(http.MethodDelete, "/api/chat/sessions/3/projects/7", nil))
	if rec.Code != http.StatusNoContent || svc.detachedSessionID != 3 || svc.detachedProjectID != 7 {
		t.Fatalf("DELETE status = %d detach = %d/%d body = %s", rec.Code, svc.detachedSessionID, svc.detachedProjectID, rec.Body.String())
	}
}

type fakeChatService struct {
	createSession db.ChatSession
	createErr     error

	createdProfile string
	sentSessionID  int64
	sentContent    string
	sentProfile    *string
	sendErr        error

	projects          []db.Project
	attachedSessionID int64
	attachedProjectID int64
	detachedSessionID int64
	detachedProjectID int64
}

func (f *fakeChatService) CreateSession(_ context.Context, profileName string) (db.ChatSession, error) {
	f.createdProfile = profileName
	return f.createSession, f.createErr
}

func (f *fakeChatService) GetSession(context.Context, int64) (db.ChatSession, error) {
	return db.ChatSession{}, nil
}

func (f *fakeChatService) ListSessions(context.Context) ([]db.ChatSession, error) { return nil, nil }

func (f *fakeChatService) ListMessages(context.Context, int64) ([]db.ChatMessage, error) {
	return nil, nil
}

func (f *fakeChatService) SendMessageWithProfile(_ context.Context, sessionID int64, content string, profileName *string) error {
	f.sentSessionID = sessionID
	f.sentContent = content
	f.sentProfile = profileName
	return f.sendErr
}

func (f *fakeChatService) SuspendSession(context.Context, int64) error { return nil }

func (f *fakeChatService) UpdateSessionProfile(context.Context, int64, string) error { return nil }

func (f *fakeChatService) DeleteSession(context.Context, int64) error { return nil }

func (f *fakeChatService) AttachProject(_ context.Context, sessionID, projectID int64) error {
	f.attachedSessionID = sessionID
	f.attachedProjectID = projectID
	return nil
}

func (f *fakeChatService) DetachProject(_ context.Context, sessionID, projectID int64) error {
	f.detachedSessionID = sessionID
	f.detachedProjectID = projectID
	return nil
}

func (f *fakeChatService) ListSessionProjects(context.Context, int64) ([]db.Project, error) {
	return f.projects, nil
}

var _ ChatService = (*fakeChatService)(nil)
