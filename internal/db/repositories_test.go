package db

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tonis2/foundry/internal/repository"
)

// -- git fixtures, mirroring internal/repository/local_test.go helpers --

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

func strPtr(s string) *string { return &s }

// TestCanonicalizeLocators covers the transformations that
// canonicalizeLocators must apply before a Repository is written:
// resolving local paths to their git worktree top-level, normalizing
// remote URLs, and treating absent/whitespace-only locators as NULL
// rather than passing raw caller input through.
func TestCanonicalizeLocators(t *testing.T) {
	t.Run("nil locators pass through unchanged", func(t *testing.T) {
		in := repository.Repository{Name: "bar"}
		out, err := canonicalizeLocators(in)
		if err != nil {
			t.Fatalf("canonicalizeLocators() error = %v", err)
		}
		if out.LocalPath != nil || out.RemoteURL != nil {
			t.Fatalf("canonicalizeLocators() = %+v, want nil locators", out)
		}
	})

	t.Run("whitespace-only locators are nilled out", func(t *testing.T) {
		in := repository.Repository{
			Name:      "bar",
			LocalPath: strPtr("   "),
			RemoteURL: strPtr("\t\n"),
		}
		out, err := canonicalizeLocators(in)
		if err != nil {
			t.Fatalf("canonicalizeLocators() error = %v", err)
		}
		if out.LocalPath != nil {
			t.Fatalf("LocalPath = %q, want nil", *out.LocalPath)
		}
		if out.RemoteURL != nil {
			t.Fatalf("RemoteURL = %q, want nil", *out.RemoteURL)
		}
	})

	t.Run("local path is resolved to git worktree top-level", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		initRepo(t, root)

		nested := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		in := repository.Repository{Name: "bar", LocalPath: strPtr(nested)}
		out, err := canonicalizeLocators(in)
		if err != nil {
			t.Fatalf("canonicalizeLocators() error = %v", err)
		}
		if out.LocalPath == nil {
			t.Fatal("LocalPath = nil, want canonical top-level path")
		}

		want, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		got, err := filepath.EvalSymlinks(*out.LocalPath)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		if got != want {
			t.Fatalf("LocalPath = %q, want %q", got, want)
		}
	})

	t.Run("non-canonicalizable local path is rejected", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir() // not a git repository

		in := repository.Repository{Name: "bar", LocalPath: strPtr(root)}
		out, err := canonicalizeLocators(in)
		if err == nil {
			t.Fatal("canonicalizeLocators() error = nil, want error for non-git directory")
		}
		if (out != repository.Repository{}) {
			t.Fatalf("canonicalizeLocators() = %+v, want zero value on error", out)
		}
	})

	t.Run("remote url is normalized", func(t *testing.T) {
		in := repository.Repository{Name: "bar", RemoteURL: strPtr("HTTPS://GitHub.com/foo/bar.git/")}
		out, err := canonicalizeLocators(in)
		if err != nil {
			t.Fatalf("canonicalizeLocators() error = %v", err)
		}
		if out.RemoteURL == nil {
			t.Fatal("RemoteURL = nil, want normalized value")
		}
		want := "https://github.com/foo/bar.git"
		if *out.RemoteURL != want {
			t.Fatalf("RemoteURL = %q, want %q", *out.RemoteURL, want)
		}
	})

	t.Run("malformed remote url is rejected", func(t *testing.T) {
		in := repository.Repository{Name: "bar", RemoteURL: strPtr("ftp://example.com/repo")}
		out, err := canonicalizeLocators(in)
		if err == nil {
			t.Fatal("canonicalizeLocators() error = nil, want error for unsupported scheme")
		}
		if (out != repository.Repository{}) {
			t.Fatalf("canonicalizeLocators() = %+v, want zero value on error", out)
		}
	})
}

// TestApplyRepositoryUpdate exercises the pure merge/canonicalize/validate
// core of UpdateRepository: which fields change for each state of
// UpdateRepositoryParams, how omitted fields differ from explicit nulls,
// and that an invalid or uncanonicalizable result is rejected before it
// could ever reach persistence (the "rollback" guarantee: current is
// never partially mutated in the caller when an error is returned).
func TestApplyRepositoryUpdate(t *testing.T) {
	local := "/home/user/repo"
	remote := "https://github.com/foo/bar.git"

	t.Run("omitted locator fields leave existing locators unchanged", func(t *testing.T) {
		current := repository.Repository{ID: 1, Name: "bar", LocalPath: &local, RemoteURL: &remote}
		newName := "baz"

		updated, err := applyRepositoryUpdate(current, UpdateRepositoryParams{Name: &newName})
		if err != nil {
			t.Fatalf("applyRepositoryUpdate() error = %v", err)
		}
		if updated.Name != newName {
			t.Fatalf("Name = %q, want %q", updated.Name, newName)
		}
		if updated.LocalPath == nil || *updated.LocalPath != local {
			t.Fatalf("LocalPath = %v, want unchanged %q", updated.LocalPath, local)
		}
		if updated.RemoteURL == nil || *updated.RemoteURL != remote {
			t.Fatalf("RemoteURL = %v, want unchanged %q", updated.RemoteURL, remote)
		}
	})

	t.Run("explicit null clears local path, transitioning to remote-only", func(t *testing.T) {
		current := repository.Repository{ID: 1, Name: "bar", LocalPath: &local, RemoteURL: &remote}

		updated, err := applyRepositoryUpdate(current, UpdateRepositoryParams{LocalPath: SetLocator(nil)})
		if err != nil {
			t.Fatalf("applyRepositoryUpdate() error = %v", err)
		}
		if updated.LocalPath != nil {
			t.Fatalf("LocalPath = %q, want nil", *updated.LocalPath)
		}
		if updated.RemoteURL == nil || *updated.RemoteURL != remote {
			t.Fatalf("RemoteURL = %v, want unchanged %q", updated.RemoteURL, remote)
		}
	})

	t.Run("explicit null clears remote url, transitioning to local-only", func(t *testing.T) {
		current := repository.Repository{ID: 1, Name: "bar", LocalPath: &local, RemoteURL: &remote}

		updated, err := applyRepositoryUpdate(current, UpdateRepositoryParams{RemoteURL: SetLocator(nil)})
		if err != nil {
			t.Fatalf("applyRepositoryUpdate() error = %v", err)
		}
		if updated.RemoteURL != nil {
			t.Fatalf("RemoteURL = %q, want nil", *updated.RemoteURL)
		}
		if updated.LocalPath == nil || *updated.LocalPath != local {
			t.Fatalf("LocalPath = %v, want unchanged %q", updated.LocalPath, local)
		}
	})

	t.Run("clearing the only locator is rejected and current is not mutated by the caller's copy", func(t *testing.T) {
		current := repository.Repository{ID: 1, Name: "bar", LocalPath: &local}
		originalLocal := *current.LocalPath

		_, err := applyRepositoryUpdate(current, UpdateRepositoryParams{LocalPath: SetLocator(nil)})
		if !errors.Is(err, repository.ErrNoLocator) {
			t.Fatalf("applyRepositoryUpdate() error = %v, want %v", err, repository.ErrNoLocator)
		}
		// The caller's `current` value (passed by value, with its own
		// pointer) must be unaffected: no partial write can have
		// occurred, and the pointer's pointee is untouched.
		if current.LocalPath == nil || *current.LocalPath != originalLocal {
			t.Fatalf("caller's current.LocalPath mutated: got %v, want %q", current.LocalPath, originalLocal)
		}
	})

	t.Run("clearing one locator while setting the other to empty is rejected", func(t *testing.T) {
		current := repository.Repository{ID: 1, Name: "bar", LocalPath: &local, RemoteURL: &remote}

		_, err := applyRepositoryUpdate(current, UpdateRepositoryParams{
			LocalPath: SetLocator(nil),
			RemoteURL: SetLocator(strPtr("   ")),
		})
		if !errors.Is(err, repository.ErrNoLocator) {
			t.Fatalf("applyRepositoryUpdate() error = %v, want %v", err, repository.ErrNoLocator)
		}
	})

	t.Run("setting a locator to a fresh value canonicalizes it", func(t *testing.T) {
		current := repository.Repository{ID: 1, Name: "bar", LocalPath: &local}

		updated, err := applyRepositoryUpdate(current, UpdateRepositoryParams{
			RemoteURL: SetLocator(strPtr("HTTPS://GitHub.com/foo/bar.git/")),
		})
		if err != nil {
			t.Fatalf("applyRepositoryUpdate() error = %v", err)
		}
		want := "https://github.com/foo/bar.git"
		if updated.RemoteURL == nil || *updated.RemoteURL != want {
			t.Fatalf("RemoteURL = %v, want %q", updated.RemoteURL, want)
		}
	})

	t.Run("uncanonicalizable local path update is rejected and returns zero value", func(t *testing.T) {
		requireGit(t)
		notARepo := t.TempDir()
		current := repository.Repository{ID: 1, Name: "bar", RemoteURL: &remote}

		updated, err := applyRepositoryUpdate(current, UpdateRepositoryParams{
			LocalPath: SetLocator(&notARepo),
		})
		if err == nil {
			t.Fatal("applyRepositoryUpdate() error = nil, want error for non-git directory")
		}
		if (updated != repository.Repository{}) {
			t.Fatalf("applyRepositoryUpdate() = %+v, want zero value on error", updated)
		}
	})

	t.Run("both locators set together are both canonicalized", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		initRepo(t, root)
		current := repository.Repository{ID: 1, Name: "bar"}

		updated, err := applyRepositoryUpdate(current, UpdateRepositoryParams{
			LocalPath: SetLocator(&root),
			RemoteURL: SetLocator(&remote),
		})
		if err != nil {
			t.Fatalf("applyRepositoryUpdate() error = %v", err)
		}
		if updated.LocalPath == nil {
			t.Fatal("LocalPath = nil, want canonicalized path")
		}
		got, err := filepath.EvalSymlinks(*updated.LocalPath)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		want, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		if got != want {
			t.Fatalf("LocalPath = %q, want %q", got, want)
		}
		if updated.RemoteURL == nil || *updated.RemoteURL != remote {
			t.Fatalf("RemoteURL = %v, want %q", updated.RemoteURL, remote)
		}
	})
}

// TestLocatorFieldZeroValueIsOmitted documents and locks in the tri-state
// contract that UpdateRepositoryParams relies on: the zero value of
// LocatorField must report as not-set (omitted), while SetLocator always
// reports as set, whether given nil or a value.
func TestLocatorFieldZeroValueIsOmitted(t *testing.T) {
	var omitted LocatorField
	if omitted.IsSet() {
		t.Fatal("zero-value LocatorField.IsSet() = true, want false")
	}

	explicitNull := SetLocator(nil)
	if !explicitNull.IsSet() {
		t.Fatal("SetLocator(nil).IsSet() = false, want true")
	}
	if explicitNull.Value() != nil {
		t.Fatalf("SetLocator(nil).Value() = %v, want nil", explicitNull.Value())
	}

	v := "value"
	explicitValue := SetLocator(&v)
	if !explicitValue.IsSet() {
		t.Fatal("SetLocator(&v).IsSet() = false, want true")
	}
	if explicitValue.Value() != &v {
		t.Fatal("SetLocator(&v).Value() did not round-trip the pointer")
	}
}
