package webui

import (
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

// TestChatDetailTemplateUsesRepositoryTerminology covers that the chat
// detail view was migrated off legacy "project" naming: attached/available
// context is rendered from Repository slices, using repository-labeled
// markup and data attributes, with no leftover "project" wording or
// legacy /projects route reference anywhere in the rendered fragment.
func TestChatDetailTemplateUsesRepositoryTerminology(t *testing.T) {
	localPath := "/repos/foo"
	sess := db.ChatSession{ID: 5, Title: "Demo", Status: "idle", ProfileName: "dev"}

	var buf strings.Builder
	err := templates.ExecuteTemplate(&buf, "chat.detail", struct {
		Session               db.ChatSession
		Messages              []db.ChatMessage
		Profiles              []db.Profile
		Sessions              []db.ChatSession
		ActiveProfileName     string
		RuntimeProfile        string
		AttachedRepositories  []repository.Repository
		AvailableRepositories []repository.Repository
	}{
		Session:           sess,
		Sessions:          []db.ChatSession{sess},
		ActiveProfileName: "dev",
		AttachedRepositories: []repository.Repository{
			{ID: 1, Name: "attached-repo", LocalPath: &localPath, CreatedAt: time.Now()},
		},
		AvailableRepositories: []repository.Repository{
			{ID: 2, Name: "available-repo", CreatedAt: time.Now()},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate(chat.detail) error = %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Attached repositories",
		"data-detach-repository",
		"data-repository-id",
		"chat-add-repository-select",
		"attached-repo",
		"available-repo",
		"/api/chat/sessions/5/message",
		"/api/chat/sessions/5/stream",
		"/api/chat/sessions/5/suspend",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in rendered chat.detail output, got:\n%s", want, out)
		}
	}

	for _, unwanted := range []string{"project", "Project", "/projects"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("expected %q not present in rendered chat.detail output, got:\n%s", unwanted, out)
		}
	}
}

// TestChatRoutesRegisteredWithoutLegacyProjectPaths covers that chat UI
// routes are registered under /chat and that a legacy sibling path
// suggesting a project sub-resource on a chat session is not a distinct
// registered route.
func TestChatRoutesRegisteredWithoutLegacyProjectPaths(t *testing.T) {
	mux, _ := newTestMux(t)

	for _, path := range []string{"/chat", "/chat/fragment", "/chat/1", "/chat/1/fragment"} {
		if pattern := registeredPattern(mux, "GET", path); pattern == "" {
			t.Fatalf("expected a route registered for %q, got none", path)
		}
	}

	if pattern := registeredPattern(mux, "GET", "/chat/1/projects"); pattern != "/chat/" {
		t.Fatalf("expected /chat/1/projects to only fall through the /chat/ catch-all, got pattern %q", pattern)
	}
}
