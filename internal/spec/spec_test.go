package spec

import "testing"

func TestBuildPromptAddsIntentReferencesBetweenGlobalContextAndGoal(t *testing.T) {
	got := BuildPrompt("global context", "phase goal", "track overlay")
	want := "global context\n\n---\n\n" + DefaultIntentReferences + "\n\n---\n\nphase goal\n\n---\n\ntrack overlay"
	if got != want {
		t.Fatalf("BuildPrompt() mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestBuildPromptStartsWithIntentReferencesWithoutGlobalContext(t *testing.T) {
	got := BuildPrompt("", "phase goal", "")
	want := DefaultIntentReferences + "\n\n---\n\nphase goal"
	if got != want {
		t.Fatalf("BuildPrompt() mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}
