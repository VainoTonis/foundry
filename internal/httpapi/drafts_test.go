package httpapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/tonis2/foundry/internal/repository"
)

// TestDraftErrorStatusClassifiesRemoteOnlyRepositoryAsConflict covers the
// case this slice must handle: selecting a remote-only repository (one
// with a RemoteURL but no LocalPath) for draft authoring is a valid,
// well-formed request against an existing resource, but authoring
// requires a local worktree mount. That must surface as 409 Conflict,
// not as a validation error (400/422) or a missing-resource error (404),
// so API consumers can distinguish "fix your request" from "this
// repository needs a local path before it can be used here".
func TestDraftErrorStatusClassifiesRemoteOnlyRepositoryAsConflict(t *testing.T) {
	repo := repository.Repository{ID: 1, Name: "remote-only-repo"}
	_, err := repo.RequireLocalPath()
	if err == nil {
		t.Fatal("RequireLocalPath() error = nil, want error for remote-only repository")
	}

	got := draftErrorStatus(err)
	if got != http.StatusConflict {
		t.Fatalf("draftErrorStatus(remote-only err) = %d, want %d", got, http.StatusConflict)
	}
}

// TestDraftErrorStatusClassifiesWrappedRemoteOnlyErrorAsConflict ensures
// the classification survives an extra layer of wrapping (as happens
// when the authoring service or db layer propagates the error up through
// fmt.Errorf("...: %w", err)), since errors.Is must still find
// repository.ErrNoLocalPath beneath any number of wraps.
func TestDraftErrorStatusClassifiesWrappedRemoteOnlyErrorAsConflict(t *testing.T) {
	repo := repository.Repository{ID: 2, Name: "remote-only-repo-2"}
	_, baseErr := repo.RequireLocalPath()
	wrapped := fmt.Errorf("get repository: %w", baseErr)

	if got := draftErrorStatus(wrapped); got != http.StatusConflict {
		t.Fatalf("draftErrorStatus(wrapped remote-only err) = %d, want %d", got, http.StatusConflict)
	}
}

// TestDraftErrorStatusClassifiesOtherErrors covers the remaining
// classification branches so a local (non-remote-only) repository's
// authoring errors keep their existing status codes: a validation
// failure like a missing repository_id stays 422, and a lookup failure
// like "repository not found" stays 404.
func TestDraftErrorStatusClassifiesOtherErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"required field missing", fmt.Errorf("repository_id is required"), http.StatusUnprocessableEntity},
		{"not found", fmt.Errorf("get repository: repository not found"), http.StatusNotFound},
		{"unrelated failure", fmt.Errorf("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := draftErrorStatus(tc.err); got != tc.want {
				t.Fatalf("draftErrorStatus(%q) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
