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

const repositorySelectColumns = "id, name, repo_path AS local_path, remote_url, created_at"

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

// applyRepositoryUpdate merges p into current, canonicalizing/normalizing
// only the locators the caller explicitly provided (leaving an omitted
// locator's already-canonical stored value untouched), and validates the
// at-least-one-locator invariant on the merged result, without performing
// any I/O. It is the pure core of UpdateRepository: isolating it lets
// update semantics (which fields change, how omitted/explicit-null values
// behave, how new locators are canonicalized) be tested without a
// database, and guarantees that an invalid or uncanonicalizable result
// never reaches persistence — the caller's current row is left untouched
// whenever this function returns an error.
func applyRepositoryUpdate(current repository.Repository, p UpdateRepositoryParams) (repository.Repository, error) {
	if p.Name != nil {
		current.Name = *p.Name
	}
	if p.LocalPath.IsSet() {
		localPath, err := canonicalizeLocalPath(p.LocalPath.Value())
		if err != nil {
			return repository.Repository{}, err
		}
		current.LocalPath = localPath
	}
	if p.RemoteURL.IsSet() {
		remoteURL, err := canonicalizeRemoteURL(p.RemoteURL.Value())
		if err != nil {
			return repository.Repository{}, err
		}
		current.RemoteURL = remoteURL
	}

	if err := current.Validate(); err != nil {
		return repository.Repository{}, err
	}
	return current, nil
}

// CreateRepository canonicalizes and validates a canonical Repository, then
// inserts it, writing its local path (if any) to the physical repo_path
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
		`INSERT INTO projects (name, repo_path, remote_url) VALUES ($1, $2, $3)
		 RETURNING `+repositorySelectColumns,
		r.Name, r.LocalPath, r.RemoteURL,
	)
	return scanRepository(row)
}

// ListRepositories returns all canonical repositories ordered by id.
func ListRepositories(ctx context.Context, pool *pgxpool.Pool) ([]repository.Repository, error) {
	rows, err := pool.Query(ctx, `SELECT `+repositorySelectColumns+` FROM projects ORDER BY id`)
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
	row := pool.QueryRow(ctx, `SELECT `+repositorySelectColumns+` FROM projects WHERE id = $1`, id)
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
// the merge, canonicalization, or validation fails, no write is performed
// and the stored row is left unchanged.
func UpdateRepository(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdateRepositoryParams) (repository.Repository, error) {
	if p.Name == nil && !p.LocalPath.IsSet() && !p.RemoteURL.IsSet() {
		return GetRepository(ctx, pool, id)
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
		`UPDATE projects SET name = $1, repo_path = $2, remote_url = $3
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

// DeleteRepository deletes a canonical Repository by id, returning
// ErrNotFound if no row matches.
func DeleteRepository(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	tag, err := pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
