package repository

import "testing"

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
