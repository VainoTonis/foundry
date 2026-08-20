package repository

import (
	"errors"
	"testing"
)

func TestNormalizeRemoteURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{
			name: "https",
			in:   "https://GitHub.com/Foo/bar.git",
			want: "https://github.com/Foo/bar.git",
		},
		{
			name: "https trailing slash",
			in:   "https://github.com/foo/bar.git/",
			want: "https://github.com/foo/bar.git",
		},
		{
			name: "http",
			in:   "http://example.com/foo/bar.git",
			want: "http://example.com/foo/bar.git",
		},
		{
			name: "ssh url form",
			in:   "ssh://git@github.com:2222/foo/bar.git",
			want: "ssh://git@github.com:2222/foo/bar.git",
		},
		{
			name: "git protocol",
			in:   "git://github.com/foo/bar.git",
			want: "git://github.com/foo/bar.git",
		},
		{
			name: "scp style",
			in:   "git@github.com:foo/bar.git",
			want: "ssh://git@github.com/foo/bar.git",
		},
		{
			name: "scp style no user",
			in:   "github.com:foo/bar.git",
			want: "ssh://github.com/foo/bar.git",
		},
		{
			name: "scp style absolute path",
			in:   "git@github.com:/foo/bar.git",
			want: "ssh://git@github.com/foo/bar.git",
		},
		{
			name:    "empty",
			in:      "",
			wantErr: true,
		},
		{
			name:    "unsupported scheme",
			in:      "ftp://example.com/foo/bar.git",
			wantErr: true,
		},
		{
			name:    "missing host",
			in:      "https:///foo/bar.git",
			wantErr: true,
		},
		{
			name:    "unparseable url",
			in:      "https://user:pass word@example.com/foo/bar.git",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeRemoteURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeRemoteURL(%q) = %q, want error", tc.in, got)
				}
				if !errors.Is(err, ErrInvalidLocator) {
					t.Fatalf("NormalizeRemoteURL(%q) error = %v, want wrapped ErrInvalidLocator", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeRemoteURL(%q) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeRemoteURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
