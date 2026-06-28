package chat

import (
	"testing"

	"github.com/tonis2/foundry/internal/db"
)

func TestEffectiveProfileNameFallsBackToRuntime(t *testing.T) {
	if got := effectiveProfileName("session", "runtime"); got != "session" {
		t.Fatalf("effectiveProfileName with session = %q, want session", got)
	}
	if got := effectiveProfileName("", "runtime"); got != "runtime" {
		t.Fatalf("effectiveProfileName fallback = %q, want runtime", got)
	}
	if got := effectiveProfileName("  ", " runtime "); got != "runtime" {
		t.Fatalf("effectiveProfileName trims = %q, want runtime", got)
	}
}

func TestBuildHistoryLinksMessagesForRecovery(t *testing.T) {
	msgs := []db.ChatMessage{
		{ID: 10, Role: "user", Content: "one"},
		{ID: 11, Role: "assistant", Content: "two"},
		{ID: 12, Role: "user", Content: "three"},
	}

	got := buildHistory(msgs)
	if len(got) != 3 {
		t.Fatalf("len(buildHistory) = %d, want 3", len(got))
	}
	if got[0].ParentID != "" || got[1].ParentID != "10" || got[2].ParentID != "11" {
		t.Fatalf("parent chain = %#v", got)
	}
	if got[2].Role != "user" || got[2].Content != "three" {
		t.Fatalf("message content not preserved: %#v", got[2])
	}
}
