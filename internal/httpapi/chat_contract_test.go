package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tonis2/foundry/internal/repository"
)

// TestHandleChatSessionRepositoriesPayloadUsesRepositoryTerminology covers
// that the repositories payload for a chat session is expressed entirely
// in repository terms (repository_id, local_path), and that a request
// missing repository_id is rejected.
func TestHandleChatSessionRepositoriesPayloadUsesRepositoryTerminology(t *testing.T) {
	localPath := "/repo"
	svc := &fakeChatService{repositories: []repository.Repository{{ID: 7, Name: "repo", LocalPath: &localPath}}}
	h := New(nil, Config{ChatService: func() ChatService { return svc }})

	rec := httptest.NewRecorder()
	h.HandleChatSession(rec, httptest.NewRequest(http.MethodGet, "/api/chat/sessions/3/repositories", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"local_path":"/repo"`) {
		t.Fatalf("expected local_path field in payload, got: %s", body)
	}

	rec = httptest.NewRecorder()
	h.HandleChatSession(rec, httptest.NewRequest(http.MethodPost, "/api/chat/sessions/3/repositories", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected body missing repository_id to be rejected, got status %d body %s", rec.Code, rec.Body.String())
	}
}

// TestHandleChatSessionUnknownSuffixNotFound covers that a chat session
// sub-resource path with an unrecognized suffix is not a recognized
// route: it falls through to the generic not-found response rather than
// the repositories handling.
func TestHandleChatSessionUnknownSuffixNotFound(t *testing.T) {
	svc := &fakeChatService{}
	h := New(nil, Config{ChatService: func() ChatService { return svc }})

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/chat/sessions/3/widgets", nil),
		httptest.NewRequest(http.MethodPost, "/api/chat/sessions/3/widgets", strings.NewReader(`{"id":7}`)),
		httptest.NewRequest(http.MethodDelete, "/api/chat/sessions/3/widgets/7", nil),
	} {
		rec := httptest.NewRecorder()
		h.HandleChatSession(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: status = %d, want 404, body = %s", req.Method, req.URL.Path, rec.Code, rec.Body.String())
		}
	}
}
