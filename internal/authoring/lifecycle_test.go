package authoring

import (
	"strings"
	"testing"

	"github.com/tonis2/foundry/internal/repository"
)

func strPtr(s string) *string { return &s }

// TestRequireLocalPathRejectsRemoteOnlyRepositorySafely covers the case
// this slice must handle without panicking or producing a confusing
// downstream error: a repository selected for draft authoring that has
// no local worktree mounted anywhere, because it is remote-only (a
// RemoteURL locator with no LocalPath). requireLocalPath must return a
// clear, descriptive error identifying the repository by name instead of
// dereferencing a nil LocalPath.
func TestRequireLocalPathRejectsRemoteOnlyRepositorySafely(t *testing.T) {
	repo := repository.Repository{
		ID:        1,
		Name:      "remote-only-repo",
		LocalPath: nil,
		RemoteURL: strPtr("https://example.com/org/repo.git"),
	}

	_, err := requireLocalPath(repo)
	if err == nil {
		t.Fatal("requireLocalPath() error = nil, want error for remote-only repository")
	}
	if !strings.Contains(err.Error(), "remote-only-repo") {
		t.Fatalf("requireLocalPath() error = %v, want it to name the repository", err)
	}
}

// TestRequireLocalPathRejectsWhitespaceOnlyLocalPath covers a
// LocalPath that is present but empty/whitespace-only, which must be
// treated the same as absent rather than passed through to callers that
// assume a usable filesystem path.
func TestRequireLocalPathRejectsWhitespaceOnlyLocalPath(t *testing.T) {
	repo := repository.Repository{ID: 2, Name: "blank-path-repo", LocalPath: strPtr("   ")}

	if _, err := requireLocalPath(repo); err == nil {
		t.Fatal("requireLocalPath() error = nil, want error for whitespace-only local path")
	}
}

// TestRequireLocalPathReturnsTrimmedLocalPath covers the normal case: a
// repository with a usable local path returns that path (trimmed of
// surrounding whitespace) and no error.
func TestRequireLocalPathReturnsTrimmedLocalPath(t *testing.T) {
	repo := repository.Repository{ID: 3, Name: "local-repo", LocalPath: strPtr("  /srv/repo  ")}

	got, err := requireLocalPath(repo)
	if err != nil {
		t.Fatalf("requireLocalPath() error = %v", err)
	}
	if got != "/srv/repo" {
		t.Fatalf("requireLocalPath() = %q, want %q", got, "/srv/repo")
	}
}
