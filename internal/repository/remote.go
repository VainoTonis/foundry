package repository

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// scpLikeRegexp matches SCP-style remotes such as "git@host.xz:path/to/repo.git".
// A path starting with "//" indicates a "scheme://" URL, not SCP syntax, and is
// rejected explicitly after matching (Go's RE2 engine has no lookahead).
var scpLikeRegexp = regexp.MustCompile(`^(?:([^@/]+)@)?([^:/]+):(.*)$`)

// NormalizeRemoteURL parses and normalizes a git remote URL without
// performing any network access. It accepts HTTP(S), SSH, git, and
// SCP-style ("user@host:path") remotes, and returns a canonical URL string.
func NormalizeRemoteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: remote url is empty", ErrInvalidLocator)
	}

	if m := scpLikeRegexp.FindStringSubmatch(raw); m != nil && !strings.HasPrefix(m[3], "//") {
		user, host, path := m[1], m[2], m[3]
		if path == "" {
			return "", fmt.Errorf("%w: remote url %q is missing a path", ErrInvalidLocator, raw)
		}
		u := &url.URL{
			Scheme: "ssh",
			Host:   strings.ToLower(host),
			Path:   "/" + strings.TrimPrefix(path, "/"),
		}
		if user != "" {
			u.User = url.User(user)
		}
		return u.String(), nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: parse remote url %q: %w", ErrInvalidLocator, raw, err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "ssh", "git":
	default:
		return "", fmt.Errorf("%w: unsupported remote url scheme %q", ErrInvalidLocator, u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: remote url %q is missing a host", ErrInvalidLocator, raw)
	}
	path := strings.TrimSuffix(u.Path, "/")
	if path == "" {
		return "", fmt.Errorf("%w: remote url %q is missing a path", ErrInvalidLocator, raw)
	}

	u.Scheme = scheme
	u.Host = strings.ToLower(u.Host)
	u.Path = path

	return u.String(), nil
}
