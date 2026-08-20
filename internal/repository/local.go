package repository

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// CanonicalLocalPath resolves an absolute local filesystem path to the
// top-level directory of the non-bare git worktree it belongs to. It uses
// the local git binary and performs no network access.
func CanonicalLocalPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: local path is empty", ErrInvalidLocator)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: local path %q must be absolute", ErrInvalidLocator, path)
	}

	top, err := runGit(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%w: resolve git top-level for %q: %w", ErrInvalidLocator, path, err)
	}
	top = filepath.Clean(top)

	isBare, err := runGit(top, "rev-parse", "--is-bare-repository")
	if err != nil {
		return "", fmt.Errorf("%w: check bare repository for %q: %w", ErrInvalidLocator, top, err)
	}
	if isBare == "true" {
		return "", fmt.Errorf("%w: %q is a bare repository, non-bare worktree required", ErrInvalidLocator, top)
	}

	return top, nil
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
