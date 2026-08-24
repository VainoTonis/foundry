package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsUIVerbosity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("db_url: postgres://example\ntelemetry_allow_unauthenticated: true\n"), 0o600); err != nil {
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

func TestLoadTelemetrySecurity(t *testing.T) {
	t.Run("requires an explicit secure mode", func(t *testing.T) {
		t.Setenv("FOUNDRY_TELEMETRY_BEARER_TOKEN", "")
		t.Setenv("FOUNDRY_TELEMETRY_ALLOW_UNAUTHENTICATED", "false")
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte("db_url: postgres://example\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("Load() error = nil, want missing telemetry security error")
		}
	})

	t.Run("environment token overrides config without leaking", func(t *testing.T) {
		const secret = "environment-secret"
		t.Setenv("FOUNDRY_TELEMETRY_BEARER_TOKEN", secret)
		t.Setenv("FOUNDRY_TELEMETRY_ALLOW_UNAUTHENTICATED", "not-a-boolean")
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte("telemetry_bearer_token: config-secret\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		_, err := Load(path)
		if err == nil {
			t.Fatal("Load() error = nil, want invalid environment error")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("Load() error leaked token: %v", err)
		}

		t.Setenv("FOUNDRY_TELEMETRY_ALLOW_UNAUTHENTICATED", "false")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.TelemetryBearerToken != secret {
			t.Fatal("TelemetryBearerToken did not use environment override")
		}
	})
}

func TestLoadUIVerbosity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("ui_verbosity: verbose\ntelemetry_allow_unauthenticated: true\n"), 0o600); err != nil {
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
