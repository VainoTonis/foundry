package repository

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v: %s", err, out)
	}
}

// runGitCmd runs a git subcommand in dir, failing the test on error. Used to
// build fixtures (commits, worktrees) that CanonicalLocalPath is then
// exercised against.
func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, out)
	}
}

// withPATH temporarily replaces the PATH environment variable for the
// duration of the test, restoring the original value on cleanup. It lets
// tests deterministically control whether/how a "git" executable is found.
func withPATH(t *testing.T, path string) {
	t.Helper()
	t.Setenv("PATH", path)
}

// writeFakeGit writes an executable script named "git" into a fresh
// directory that, when invoked, deterministically fails with a fixed
// message on stderr. It returns the directory to prepend to PATH.
func writeFakeGit(t *testing.T, stderrMsg string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho '" + stderrMsg + "' >&2\nexit 1\n"
	path := filepath.Join(dir, "git")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	return dir
}

func TestCanonicalLocalPath(t *testing.T) {
	t.Run("rejects relative path", func(t *testing.T) {
		_, err := CanonicalLocalPath("relative/path")
		if err == nil {
			t.Fatal("expected error for relative path")
		}
		if !errors.Is(err, ErrInvalidLocator) {
			t.Fatalf("CanonicalLocalPath() error = %v, want wrapped ErrInvalidLocator", err)
		}
	})

	t.Run("rejects empty path", func(t *testing.T) {
		_, err := CanonicalLocalPath("")
		if err == nil {
			t.Fatal("expected error for empty path")
		}
		if !errors.Is(err, ErrInvalidLocator) {
			t.Fatalf("CanonicalLocalPath() error = %v, want wrapped ErrInvalidLocator", err)
		}
	})

	t.Run("resolves to worktree top-level", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		initRepo(t, root)

		nested := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		got, err := CanonicalLocalPath(nested)
		if err != nil {
			t.Fatalf("CanonicalLocalPath() error = %v", err)
		}

		want, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		gotResolved, err := filepath.EvalSymlinks(got)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		if gotResolved != want {
			t.Fatalf("CanonicalLocalPath() = %q, want %q", gotResolved, want)
		}
	})

	t.Run("resolves to linked worktree top-level", func(t *testing.T) {
		requireGit(t)
		main := t.TempDir()
		initRepo(t, main)
		runGitCmd(t, main, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "initial")

		worktree := filepath.Join(t.TempDir(), "wt")
		runGitCmd(t, main, "worktree", "add", "-b", "feature", worktree)

		nested := filepath.Join(worktree, "a", "b")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		got, err := CanonicalLocalPath(nested)
		if err != nil {
			t.Fatalf("CanonicalLocalPath() error = %v", err)
		}

		want, err := filepath.EvalSymlinks(worktree)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		gotResolved, err := filepath.EvalSymlinks(got)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		if gotResolved != want {
			t.Fatalf("CanonicalLocalPath() = %q, want %q", gotResolved, want)
		}

		mainResolved, err := filepath.EvalSymlinks(main)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		if gotResolved == mainResolved {
			t.Fatalf("CanonicalLocalPath() = %q, want linked worktree top-level, not main repository %q", gotResolved, mainResolved)
		}
	})

	t.Run("rejects bare repository", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		cmd := exec.Command("git", "init", "--bare")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init --bare failed: %v: %s", err, out)
		}

		_, err := CanonicalLocalPath(root)
		if err == nil {
			t.Fatal("expected error for bare repository")
		}
		if !errors.Is(err, ErrInvalidLocator) {
			t.Fatalf("CanonicalLocalPath() error = %v, want wrapped ErrInvalidLocator", err)
		}
	})

	t.Run("rejects non-git directory", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		_, err := CanonicalLocalPath(root)
		if err == nil {
			t.Fatal("expected error for non-git directory")
		}
		if !errors.Is(err, ErrInvalidLocator) {
			t.Fatalf("CanonicalLocalPath() error = %v, want wrapped ErrInvalidLocator", err)
		}
	})

	t.Run("errors deterministically when git binary is unavailable", func(t *testing.T) {
		// PATH points to an empty directory: no "git" executable can be
		// found regardless of what is installed on the host running the
		// test, so this is deterministic and does not require git.
		withPATH(t, t.TempDir())

		_, err := CanonicalLocalPath(t.TempDir())
		if err == nil {
			t.Fatal("expected error when git binary is unavailable")
		}
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("CanonicalLocalPath() error = %v, want wrapped exec.ErrNotFound", err)
		}
	})

	t.Run("errors deterministically when git command fails", func(t *testing.T) {
		// The fake git executable always exits non-zero with a fixed
		// stderr message, so the failure is deterministic and does not
		// depend on a real git installation.
		const wantMsg = "fatal: deterministic failure injected by test"
		withPATH(t, writeFakeGit(t, wantMsg))

		_, err := CanonicalLocalPath(t.TempDir())
		if err == nil {
			t.Fatal("expected error when git command fails")
		}
		if !strings.Contains(err.Error(), wantMsg) {
			t.Fatalf("CanonicalLocalPath() error = %v, want message containing %q", err, wantMsg)
		}
	})
}
