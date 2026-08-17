package workflow

import (
	"testing"

	"github.com/tonis2/foundry/internal/db"
)

func TestBuildProfilePayloadWithProfile(t *testing.T) {
	p := &db.Profile{DefaultModel: "gpt-5"}
	payload := buildProfilePayload(p)
	if payload["default_model"] != "gpt-5" {
		t.Fatalf("payload missing default_model: %#v", payload)
	}
}

func TestBuildProfilePayloadEmptyWhenNoProfile(t *testing.T) {
	payload := buildProfilePayload(nil)
	if len(payload) != 0 {
		t.Fatalf("payload = %#v, want empty", payload)
	}
}
