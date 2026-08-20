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

// Repository is the canonical domain representation of a source-code
// repository tracked by the system.
type Repository struct {
	ID        int64
	Name      string
	LocalPath *string
	RemoteURL *string
	CreatedAt time.Time
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
