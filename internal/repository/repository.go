// Package repository defines the canonical Repository domain model: a
// named source-code repository identified by a local filesystem path
// and/or a remote URL.
package repository

import (
	"errors"
	"strings"
	"time"
)

// ErrNoLocator is returned when a Repository has neither a local path nor
// a remote URL set, which is not a valid state.
var ErrNoLocator = errors.New("repository: at least one of local path or remote url is required")

// ErrInvalidLocator is returned when a caller-supplied local path or
// remote URL locator is syntactically malformed, non-absolute, does not
// point at a non-bare git worktree, or uses an unsupported/unparseable
// remote URL scheme. It wraps every error CanonicalLocalPath and
// NormalizeRemoteURL return, so callers (such as the HTTP API) can use
// errors.Is to classify these as client input errors (HTTP 400) as
// distinct from infrastructure/database failures (HTTP 500).
var ErrInvalidLocator = errors.New("repository: invalid locator")

// Repository is the canonical domain representation of a source-code
// repository tracked by the system.
type Repository struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	LocalPath *string   `json:"local_path"`
	RemoteURL *string   `json:"remote_url"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks invariants of the Repository domain model. It currently
// enforces that at least one locator (LocalPath or RemoteURL) is present.
// A locator that is nil, empty, or consists only of whitespace is treated
// as absent.
func (r Repository) Validate() error {
	hasLocal := r.LocalPath != nil && strings.TrimSpace(*r.LocalPath) != ""
	hasRemote := r.RemoteURL != nil && strings.TrimSpace(*r.RemoteURL) != ""
	if !hasLocal && !hasRemote {
		return ErrNoLocator
	}
	return nil
}
