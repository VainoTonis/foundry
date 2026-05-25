package cerberus

import (
	"context"
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
