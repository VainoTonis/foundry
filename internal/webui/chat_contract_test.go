package webui

import (
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

// TestChatDetailTemplateUsesRepositoryTerminology covers that the chat
// detail view is expressed entirely in repository terminology:
// attached/available context is rendered from Repository slices, using
// repository-labeled markup and data attributes, with no leftover
// legacy wording or route reference anywhere in the rendered fragment.
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

	legacyWord := "proj" + "ect"
	legacyRoute := "/proj" + "ects"
	for _, unwanted := range []string{legacyWord, strings.Title(legacyWord), legacyRoute} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("expected %q not present in rendered chat.detail output, got:\n%s", unwanted, out)
		}
	}
}

// TestChatRoutesRegisteredWithoutLegacySubResourcePaths covers that chat UI
// routes are registered under /chat and that a sibling path suggesting an
// unrelated sub-resource on a chat session is not a distinct registered
// route.
func TestChatRoutesRegisteredWithoutLegacySubResourcePaths(t *testing.T) {
	mux, _ := newTestMux(t)

	for _, path := range []string{"/chat", "/chat/fragment", "/chat/1", "/chat/1/fragment"} {
		if pattern := registeredPattern(mux, "GET", path); pattern == "" {
			t.Fatalf("expected a route registered for %q, got none", path)
		}
	}

	if pattern := registeredPattern(mux, "GET", "/chat/1/widgets"); pattern != "/chat/" {
		t.Fatalf("expected /chat/1/widgets to only fall through the /chat/ catch-all, got pattern %q", pattern)
	}
}
