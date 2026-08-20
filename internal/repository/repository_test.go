package repository

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	local := "/home/user/repo"
	remote := "https://github.com/foo/bar.git"

	cases := []struct {
		name    string
		repo    Repository
		wantErr error
	}{
		{
			name:    "no locators",
			repo:    Repository{Name: "bar"},
			wantErr: ErrNoLocator,
		},
		{
			name:    "empty string locators",
			repo:    Repository{Name: "bar", LocalPath: strPtr(""), RemoteURL: strPtr("")},
			wantErr: ErrNoLocator,
		},
		{
			name: "only local path",
			repo: Repository{Name: "bar", LocalPath: &local},
		},
		{
			name: "only remote url",
			repo: Repository{Name: "bar", RemoteURL: &remote},
		},
		{
			name: "both locators",
			repo: Repository{Name: "bar", LocalPath: &local, RemoteURL: &remote},
		},
		{
			name:    "whitespace-only local path and remote url",
			repo:    Repository{Name: "bar", LocalPath: strPtr("   "), RemoteURL: strPtr("\t\n")},
			wantErr: ErrNoLocator,
		},
		{
			name: "whitespace-only local path with valid remote url",
			repo: Repository{Name: "bar", LocalPath: strPtr("  "), RemoteURL: &remote},
		},
		{
			name: "valid local path with whitespace-only remote url",
			repo: Repository{Name: "bar", LocalPath: &local, RemoteURL: strPtr("  ")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.repo.Validate()
			if tc.wantErr == nil && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if tc.wantErr != nil && err != tc.wantErr {
				t.Fatalf("Validate() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

// TestRequireLocalPath covers RequireLocalPath's behavior on the three
// cases workflow execution and spec authoring care about: a local path
// present, absent, and remote-only (no local path at all). It must be
// possible for callers to classify "no local path" via errors.Is(err,
// ErrNoLocalPath) so remote-only repositories can be reported as a
// conflict rather than a generic error.
func TestRequireLocalPath(t *testing.T) {
	local := "/home/user/repo"
	remote := "https://github.com/foo/bar.git"

	t.Run("local path present", func(t *testing.T) {
		got, err := Repository{Name: "bar", LocalPath: &local}.RequireLocalPath()
		if err != nil {
			t.Fatalf("RequireLocalPath() error = %v, want nil", err)
		}
		if got != local {
			t.Fatalf("RequireLocalPath() = %q, want %q", got, local)
		}
	})

	t.Run("remote-only repository", func(t *testing.T) {
		_, err := Repository{Name: "bar", RemoteURL: &remote}.RequireLocalPath()
		if !errors.Is(err, ErrNoLocalPath) {
			t.Fatalf("RequireLocalPath() error = %v, want wrapped ErrNoLocalPath", err)
		}
	})

	t.Run("whitespace-only local path is treated as absent", func(t *testing.T) {
		_, err := Repository{Name: "bar", LocalPath: strPtr("   "), RemoteURL: &remote}.RequireLocalPath()
		if !errors.Is(err, ErrNoLocalPath) {
			t.Fatalf("RequireLocalPath() error = %v, want wrapped ErrNoLocalPath", err)
		}
	})
}
