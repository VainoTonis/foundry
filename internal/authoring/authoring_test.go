package authoring

import (
	"encoding/json"
	"testing"
)

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

func TestExtractFinalSpecFindsSaveReadySpecInMarkdownFence(t *testing.T) {
	messages := mustDraftMessagesJSON(t, []draftTestMessage{
		{Role: "assistant", Content: "Earlier draft without phases"},
		{Role: "assistant", Content: "Draft #5 is ready to save.\n\n```markdown\n# Draft Studio Robust Save\n\nBuild the save path.\n\n## Phase 1: Extract\n\nFind the spec inside assistant prose.\n```\n\nYou can save this now."},
	})

	got := ExtractFinalSpec(messages)
	want := "# Draft Studio Robust Save\n\nBuild the save path.\n\n## Phase 1: Extract\n\nFind the spec inside assistant prose."
	if got != want {
		t.Fatalf("ExtractFinalSpec() = %q, want %q", got, want)
	}
}

func TestExtractFinalSpecFindsSaveReadySpecAfterProse(t *testing.T) {
	messages := mustDraftMessagesJSON(t, []draftTestMessage{
		{Role: "assistant", Content: "This is the save-ready version:\n\n# Draft Five Style Output\n\nProse before the title should not be saved.\n\n## Phase 1: Save\n\nPersist only the markdown spec."},
	})

	got := ExtractFinalSpec(messages)
	want := "# Draft Five Style Output\n\nProse before the title should not be saved.\n\n## Phase 1: Save\n\nPersist only the markdown spec."
	if got != want {
		t.Fatalf("ExtractFinalSpec() = %q, want %q", got, want)
	}
}
