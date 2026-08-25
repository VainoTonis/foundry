package cerberus

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGenerateRunsChatAndReturnsStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "cerberus-fake")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD|$1|$2|$3|$4|$5\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	c := New(bin, "", "", "")
	c.SetRepoPath(repo)
	out, err := c.Generate(context.Background(), "session-1", "make markdown")
	if err != nil {
		t.Fatal(err)
	}
	want := repo + "|chat|--name|session-1|--prompt|make markdown"
	if strings.TrimSpace(out) != want {
		t.Fatalf("Generate output = %q, want %q", strings.TrimSpace(out), want)
	}
}

func TestPerRepoViewGenerateUsesSpecificRepo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "cerberus-fake")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD|$1|$2|$3|$4|$5\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo1 := filepath.Join(dir, "repo1")
	repo2 := filepath.Join(dir, "repo2")
	if err := os.Mkdir(repo1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(repo2, 0o755); err != nil {
		t.Fatal(err)
	}
	c := New(bin, "", "", "")
	c.SetRepoPath(repo1)

	// Use PerRepoView for repo2
	view := c.WithRepo(repo2)
	out, err := view.Generate(context.Background(), "session-1", "make markdown")
	if err != nil {
		t.Fatal(err)
	}
	want := repo2 + "|chat|--name|session-1|--prompt|make markdown"
	if strings.TrimSpace(out) != want {
		t.Fatalf("PerRepoView.Generate output = %q, want %q", strings.TrimSpace(out), want)
	}

	// Verify Client repoPath was not mutated
	if c.repoPath != repo1 {
		t.Fatalf("Client.repoPath was mutated: got %q, want %q", c.repoPath, repo1)
	}
}

func TestPerRepoViewWithImageAndModel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "cerberus-fake")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	c := New(bin, "test-image", "test-model", "")
	view := c.WithRepo(repo)
	out, err := view.Generate(context.Background(), "session-1", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	outStr := strings.TrimSpace(out)
	if !strings.Contains(outStr, "--image") || !strings.Contains(outStr, "test-image") {
		t.Fatalf("Generate output missing image: %q", outStr)
	}
	if !strings.Contains(outStr, "--model") || !strings.Contains(outStr, "test-model") {
		t.Fatalf("Generate output missing model: %q", outStr)
	}
}

func TestPerRepoViewStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "cerberus-fake")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s' \"$PWD|$1|$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	c := New(bin, "", "", "")
	view := c.WithRepo(repo)
	out, err := view.Status(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	want := repo + "|status|--name"
	if !strings.Contains(out, want) {
		t.Fatalf("Status output = %q, want to contain %q", out, want)
	}
}

func TestTurnOutputDecodesLegacyMessageField(t *testing.T) {
	raw := `{"status":"ok","uuid":"u1","message":"hello there"}`
	var out TurnOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Message != "hello there" {
		t.Fatalf("Message = %q, want %q", out.Message, "hello there")
	}
	if out.Status != "ok" || out.UUID != "u1" {
		t.Fatalf("unexpected fields: %+v", out)
	}
}

func TestTurnOutputDecodesAssistantMessageString(t *testing.T) {
	raw := `{"status":"ok","uuid":"u1","assistant_message":"hi from assistant"}`
	var out TurnOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Message != "hi from assistant" {
		t.Fatalf("Message = %q, want %q", out.Message, "hi from assistant")
	}
}

func TestTurnOutputDecodesAssistantMessageObjectWithStringContent(t *testing.T) {
	raw := `{"status":"ok","uuid":"u1","assistant_message":{"content":"object content"}}`
	var out TurnOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Message != "object content" {
		t.Fatalf("Message = %q, want %q", out.Message, "object content")
	}
}

func TestTurnOutputDecodesAssistantMessageContentBlocks(t *testing.T) {
	raw := `{"status":"ok","uuid":"u1","assistant_message":{"content":[{"type":"text","text":"part one "},{"type":"text","text":"part two"}]}}`
	var out TurnOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Message != "part one part two" {
		t.Fatalf("Message = %q, want %q", out.Message, "part one part two")
	}
}

func TestTurnOutputLegacyMessageTakesPrecedenceOverAssistantMessage(t *testing.T) {
	raw := `{"status":"ok","uuid":"u1","message":"legacy wins","assistant_message":"should be ignored"}`
	var out TurnOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Message != "legacy wins" {
		t.Fatalf("Message = %q, want %q", out.Message, "legacy wins")
	}
}

func TestTurnOutputWithoutMessageOrAssistantMessage(t *testing.T) {
	raw := `{"status":"error","uuid":"u1","error":"session not found"}`
	var out TurnOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Message != "" {
		t.Fatalf("Message = %q, want empty", out.Message)
	}
	if out.Error != "session not found" {
		t.Fatalf("Error = %q, want %q", out.Error, "session not found")
	}
}

func TestPerRepoViewClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "cerberus-fake")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s' \"$PWD|$1|$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	c := New(bin, "", "", "")
	view := c.WithRepo(repo)
	err := view.Close(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
}
