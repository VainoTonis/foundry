package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsUIVerbosity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("db_url: postgres://example\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UIVerbosity != "normal" {
		t.Fatalf("UIVerbosity = %q, want normal", cfg.UIVerbosity)
	}
}

func TestLoadUIVerbosity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("ui_verbosity: verbose\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UIVerbosity != "verbose" {
		t.Fatalf("UIVerbosity = %q, want verbose", cfg.UIVerbosity)
	}
}
