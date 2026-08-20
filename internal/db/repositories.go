package db

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tonis2/foundry/internal/repository"
)

// LocatorField represents an optional update to one of a Repository's
// locator fields (LocalPath or RemoteURL). Its zero value means the field
// was omitted by the caller and must be left unchanged. SetLocator marks
// the field as explicitly provided, distinguishing an explicit null (clear
// the locator, transitioning the Repository toward local-only or
// remote-only) from an explicit value (set the locator, canonicalized or
// normalized before persisting) and from an omitted field (no change).
// This distinction is required so callers can transition a Repository
// between local-only, remote-only, and both-locators states one field at a
// time while UpdateRepository still enforces the at-least-one-locator
// invariant on the merged result.
type LocatorField struct {
	set   bool
	value *string
}

// SetLocator returns a LocatorField that is explicitly provided and set to
// value. Passing nil explicitly clears the locator; passing a non-nil
// pointer sets it (subject to canonicalization/normalization).
func SetLocator(value *string) LocatorField {
	return LocatorField{set: true, value: value}
}

// IsSet reports whether the field was explicitly provided by the caller,
// as opposed to omitted (the zero value).
func (f LocatorField) IsSet() bool { return f.set }

// Value returns the explicit value carried by the field. It is only
// meaningful when IsSet() is true.
func (f LocatorField) Value() *string { return f.value }

// UpdateRepositoryParams describes the fields that may be updated on a
// canonical Repository. Name is nil-means-unchanged. LocalPath and
// RemoteURL are LocatorField values: their zero value leaves the
// corresponding locator unchanged, SetLocator(nil) clears it, and
// SetLocator(&v) sets it.
type UpdateRepositoryParams struct {
	Name      *string
	LocalPath LocatorField
	RemoteURL LocatorField
}

const repositorySelectColumns = "id, name, local_path, remote_url, created_at"

func scanRepository(row pgx.Row) (repository.Repository, error) {
	var r repository.Repository
	err := row.Scan(&r.ID, &r.Name, &r.LocalPath, &r.RemoteURL, &r.CreatedAt)
	return r, err
}

// canonicalizeLocalPath resolves a non-nil, non-empty local path to the
// top-level of the git worktree it belongs to. A nil, empty, or
// whitespace-only path is treated as absent and returned as nil, so that
// persisted locators are always either a canonical value or NULL, never
// raw caller input.
func canonicalizeLocalPath(v *string) (*string, error) {
	if v == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil, nil
	}
	canonical, err := repository.CanonicalLocalPath(trimmed)
	if err != nil {
		return nil, err
	}
	return &canonical, nil
}

// canonicalizeRemoteURL normalizes a non-nil, non-empty remote URL to its
// canonical form. A nil, empty, or whitespace-only URL is treated as
// absent and returned as nil, mirroring canonicalizeLocalPath.
func canonicalizeRemoteURL(v *string) (*string, error) {
	if v == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil, nil
	}
	normalized, err := repository.NormalizeRemoteURL(trimmed)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

// canonicalizeLocators canonicalizes/normalizes both locators of r. It
// must be called on every Repository written by CreateRepository, since a
// newly-created Repository's locators are unvalidated caller input and
// have never been canonicalized before, not only checked for presence via
// Validate.
func canonicalizeLocators(r repository.Repository) (repository.Repository, error) {
	localPath, err := canonicalizeLocalPath(r.LocalPath)
	if err != nil {
		return repository.Repository{}, err
	}
	remoteURL, err := canonicalizeRemoteURL(r.RemoteURL)
	if err != nil {
		return repository.Repository{}, err
	}
	r.LocalPath = localPath
	r.RemoteURL = remoteURL
	return r, nil
}

// canonicalizeUpdateParams canonicalizes/normalizes any locator fields the
// caller explicitly provided in p, returning a copy of p with those
// fields replaced by their canonical form. It performs no database
// access, so UpdateRepository can call it before fetching the current
// row: a malformed/unsupported locator is then rejected as a client
// validation error without depending on whether a row for id exists, and
// without ever touching the database. Fields left unset in p (an omitted
// locator) pass through unchanged.
func canonicalizeUpdateParams(p UpdateRepositoryParams) (UpdateRepositoryParams, error) {
	if p.LocalPath.IsSet() {
		localPath, err := canonicalizeLocalPath(p.LocalPath.Value())
		if err != nil {
			return UpdateRepositoryParams{}, err
		}
		p.LocalPath = SetLocator(localPath)
	}
	if p.RemoteURL.IsSet() {
		remoteURL, err := canonicalizeRemoteURL(p.RemoteURL.Value())
		if err != nil {
			return UpdateRepositoryParams{}, err
		}
		p.RemoteURL = SetLocator(remoteURL)
	}
	return p, nil
}

// applyRepositoryUpdate merges p into current and validates the
// at-least-one-locator invariant on the merged result, without
// performing any I/O. It assumes any locators explicitly set in p have
// already been canonicalized/normalized (via canonicalizeUpdateParams),
// and only merges omitted-vs-explicit-null-vs-explicit-value semantics.
// It is the pure core of UpdateRepository: isolating it lets update
// semantics be tested without a database, and guarantees that an invalid
// merged result never reaches persistence — the caller's current row is
// left untouched whenever this function returns an error.
func applyRepositoryUpdate(current repository.Repository, p UpdateRepositoryParams) (repository.Repository, error) {
	if p.Name != nil {
		current.Name = *p.Name
	}
	if p.LocalPath.IsSet() {
		current.LocalPath = p.LocalPath.Value()
	}
	if p.RemoteURL.IsSet() {
		current.RemoteURL = p.RemoteURL.Value()
	}

	if err := current.Validate(); err != nil {
		return repository.Repository{}, err
	}
	return current, nil
}

// CreateRepository canonicalizes and validates a canonical Repository, then
// inserts it, writing its local path (if any) to the physical local_path
// column.
func CreateRepository(ctx context.Context, pool *pgxpool.Pool, r repository.Repository) (repository.Repository, error) {
	r, err := canonicalizeLocators(r)
	if err != nil {
		return repository.Repository{}, err
	}
	if err := r.Validate(); err != nil {
		return repository.Repository{}, err
	}
	row := pool.QueryRow(ctx,
		`INSERT INTO repositories (name, local_path, remote_url) VALUES ($1, $2, $3)
		 RETURNING `+repositorySelectColumns,
		r.Name, r.LocalPath, r.RemoteURL,
	)
	return scanRepository(row)
}

// ListRepositories returns all canonical repositories ordered by id.
func ListRepositories(ctx context.Context, pool *pgxpool.Pool) ([]repository.Repository, error) {
	rows, err := pool.Query(ctx, `SELECT `+repositorySelectColumns+` FROM repositories ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repository.Repository
	for rows.Next() {
		var r repository.Repository
		if err := rows.Scan(&r.ID, &r.Name, &r.LocalPath, &r.RemoteURL, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRepository fetches a canonical Repository by id, returning ErrNotFound
// if no row matches.
func GetRepository(ctx context.Context, pool *pgxpool.Pool, id int64) (repository.Repository, error) {
	row := pool.QueryRow(ctx, `SELECT `+repositorySelectColumns+` FROM repositories WHERE id = $1`, id)
	r, err := scanRepository(row)
	if err == pgx.ErrNoRows {
		return r, ErrNotFound
	}
	return r, err
}

// UpdateRepository applies the given field changes to a canonical
// Repository — canonicalizing/normalizing any new locators and
// re-validating the at-least-one-locator invariant on the merged result —
// before persisting it. It returns ErrNotFound if no row matches id. If
// the canonicalization, merge, or validation fails, no write is performed
// and the stored row is left unchanged. New locators are
// canonicalized/normalized before the current row is fetched, so a
// malformed/unsupported locator is rejected as a client validation error
// without any database access.
func UpdateRepository(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdateRepositoryParams) (repository.Repository, error) {
	if p.Name == nil && !p.LocalPath.IsSet() && !p.RemoteURL.IsSet() {
		return GetRepository(ctx, pool, id)
	}

	p, err := canonicalizeUpdateParams(p)
	if err != nil {
		return repository.Repository{}, err
	}

	current, err := GetRepository(ctx, pool, id)
	if err != nil {
		return repository.Repository{}, err
	}

	updated, err := applyRepositoryUpdate(current, p)
	if err != nil {
		return repository.Repository{}, err
	}

	row := pool.QueryRow(ctx,
		`UPDATE repositories SET name = $1, local_path = $2, remote_url = $3
		 WHERE id = $4
		 RETURNING `+repositorySelectColumns,
		updated.Name, updated.LocalPath, updated.RemoteURL, id,
	)
	r, err := scanRepository(row)
	if err == pgx.ErrNoRows {
		return r, ErrNotFound
	}
	return r, err
}

// RefreshResult reports the outcome of attempting to refresh a single
// repository's normalized origin remote URL, as returned by
// RefreshRepositories.
type RefreshResult struct {
	ID      int64
	Name    string
	Updated bool
	Err     error
}

// RefreshRepositories fills in the normalized "origin" remote URL for
// every repository that has a LocalPath but no RemoteURL yet. This is
// missing-only: a repository whose RemoteURL is already set -- whether
// populated by a previous refresh or configured explicitly by a caller --
// is left untouched, so a deliberately configured remote is never
// overwritten by whatever "origin" happens to be configured locally.
//
// Each eligible repository is refreshed independently by reading its
// local worktree's "origin" remote (repository.LocalOriginURL, no network
// access) and normalizing it (repository.NormalizeRemoteURL). A failure
// for one row -- no origin configured, a malformed/unsupported origin
// URL, a missing/unreadable local path, or a database error updating that
// row -- is recorded in its RefreshResult.Err rather than aborting the
// remaining rows. RefreshRepositories itself only returns a non-nil error
// for an infrastructure failure that prevents it from even listing
// repositories.
func RefreshRepositories(ctx context.Context, pool *pgxpool.Pool) ([]RefreshResult, error) {
	repos, err := ListRepositories(ctx, pool)
	if err != nil {
		return nil, err
	}

	var results []RefreshResult
	for _, r := range repos {
		hasLocal := r.LocalPath != nil && strings.TrimSpace(*r.LocalPath) != ""
		hasRemote := r.RemoteURL != nil && strings.TrimSpace(*r.RemoteURL) != ""
		if !hasLocal || hasRemote {
			continue
		}

		origin, err := repository.LocalOriginURL(*r.LocalPath)
		if err != nil {
			results = append(results, RefreshResult{ID: r.ID, Name: r.Name, Err: err})
			continue
		}

		normalized, err := repository.NormalizeRemoteURL(origin)
		if err != nil {
			results = append(results, RefreshResult{ID: r.ID, Name: r.Name, Err: err})
			continue
		}

		if _, err := UpdateRepository(ctx, pool, r.ID, UpdateRepositoryParams{RemoteURL: SetLocator(&normalized)}); err != nil {
			results = append(results, RefreshResult{ID: r.ID, Name: r.Name, Err: err})
			continue
		}

		results = append(results, RefreshResult{ID: r.ID, Name: r.Name, Updated: true})
	}
	return results, nil
}

// DeleteRepository deletes a canonical Repository by id, returning
// ErrNotFound if no row matches.
func DeleteRepository(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	tag, err := pool.Exec(ctx, `DELETE FROM repositories WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
