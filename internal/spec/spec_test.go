package spec

import "testing"

func TestBuildPromptAddsGlobalContextBeforeGoal(t *testing.T) {
	got := BuildPrompt("global context", "phase goal", "track overlay")
	want := "global context\n\n---\n\nphase goal\n\n---\n\ntrack overlay"
	if got != want {
		t.Fatalf("BuildPrompt() mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestBuildPromptStartsWithGoalWithoutGlobalContext(t *testing.T) {
	got := BuildPrompt("", "phase goal", "")
	want := "phase goal"
	if got != want {
		t.Fatalf("BuildPrompt() mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}
