package chat

import "testing"

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
