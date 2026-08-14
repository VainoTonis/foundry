package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tonis2/foundry/internal/db"
)

func TestBuildProfilePayloadWithoutVaultOmitsExtraMounts(t *testing.T) {
	p := &db.Profile{DefaultModel: "gpt-5"}
	payload, mounted := buildProfilePayload(p, "")
	if mounted {
		t.Fatalf("expected vault not mounted")
	}
	if _, ok := payload["extra_mounts"]; ok {
		t.Fatalf("payload should not contain extra_mounts: %#v", payload)
	}
	if payload["default_model"] != "gpt-5" {
		t.Fatalf("payload missing default_model: %#v", payload)
	}
}

func TestBuildProfilePayloadWithVaultAddsReadOnlyMount(t *testing.T) {
	payload, mounted := buildProfilePayload(nil, "/host/vault")
	if !mounted {
		t.Fatalf("expected vault mounted")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var got struct {
		ExtraMounts []struct {
			Host      string `json:"host"`
			Container string `json:"container"`
			ReadOnly  bool   `json:"read_only"`
		} `json:"extra_mounts"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(got.ExtraMounts) != 1 {
		t.Fatalf("extra_mounts = %#v, want 1 entry", got.ExtraMounts)
	}
	m := got.ExtraMounts[0]
	if m.Host != "/host/vault" || m.Container != "/vault" || !m.ReadOnly {
		t.Fatalf("mount = %#v, want host=/host/vault container=/vault read_only=true", m)
	}
}

func TestBuildProfilePayloadEmptyWhenNoProfileAndNoVault(t *testing.T) {
	payload, mounted := buildProfilePayload(nil, "")
	if mounted {
		t.Fatalf("expected vault not mounted")
	}
	if len(payload) != 0 {
		t.Fatalf("payload = %#v, want empty", payload)
	}
}

func TestVaultInstructionsMentionsMountAndCommands(t *testing.T) {
	instr := vaultInstructions()
	if !strings.Contains(instr, "/vault") {
		t.Fatalf("instructions missing mount path: %q", instr)
	}
	if !strings.Contains(instr, "frontmatter-radar query") {
		t.Fatalf("instructions missing query command: %q", instr)
	}
	if !strings.Contains(instr, "frontmatter-radar read") {
		t.Fatalf("instructions missing read command: %q", instr)
	}
}
