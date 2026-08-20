package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tonis2/foundry/internal/repository"
)

// testDBURLEnv names the environment variable that opts this suite into
// running against a real PostgreSQL instance. It is intentionally
// distinct from the application's own config key (db_url in
// config.yaml) so a developer's or CI's app configuration is never
// accidentally pointed at by a test run. The suite is skipped, not
// failed, when the variable is unset, so `go test ./...` remains usable
// without PostgreSQL available.
const testDBURLEnv = "FOUNDRY_TEST_DATABASE_URL"

// testPool connects to the PostgreSQL instance named by
// FOUNDRY_TEST_DATABASE_URL, applies any pending migrations from the
// repository's migrations directory, and returns a pool whose lifetime
// is tied to t via t.Cleanup. Tests call this first and t.Skip happens
// automatically when the environment variable is absent.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv(testDBURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test", testDBURLEnv)
	}

	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations path: %v", err)
	}

	m, err := migrate.New("file:///"+migrationsPath, url)
	if err != nil {
		t.Fatalf("migrate init: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
		t.Fatalf("migrate close: src=%v db=%v", srcErr, dbErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool.Ping: %v", err)
	}
	return pool
}

// newLocalGitRepo creates a fresh temporary, non-bare Git repository
// (using the requireGit/initRepo fixtures declared in repositories_test.go)
// and returns its canonical worktree root: the same absolute path
// CreateRepository's canonicalization (repository.CanonicalLocalPath, via
// "git rev-parse --show-toplevel") resolves any path inside the repository
// to. Local-path CRUD cases use this instead of a fixed, nonexistent
// filesystem path, since production code rejects any LocalPath that is not
// a real non-bare git worktree -- a fixed path would either fail
// canonicalization outright or, worse, never actually exercise it.
func newLocalGitRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)

	dir := t.TempDir()
	initRepo(t, dir)

	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	return canonical
}

// deleteRepositoryQuietly removes the row for id, ignoring ErrNotFound
// (the test may already have deleted it, e.g. as part of the not-found
// mapping cases). It exists so t.Cleanup call sites stay one-liners.
func deleteRepositoryQuietly(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	ctx := context.Background()
	if err := DeleteRepository(ctx, pool, id); err != nil && !errors.Is(err, ErrNotFound) {
		t.Errorf("cleanup DeleteRepository(%d): %v", id, err)
	}
}

// createTestRepository is a small helper around CreateRepository that
// registers cleanup and fails the test immediately on error, keeping the
// individual test bodies focused on the behavior under test.
func createTestRepository(t *testing.T, pool *pgxpool.Pool, r repository.Repository) repository.Repository {
	t.Helper()
	ctx := context.Background()
	created, err := CreateRepository(ctx, pool, r)
	if err != nil {
		t.Fatalf("CreateRepository(%+v): %v", r, err)
	}
	t.Cleanup(func() { deleteRepositoryQuietly(t, pool, created.ID) })
	return created
}

func TestRepositoriesCRUD_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("local-only repository can be created and read back", func(t *testing.T) {
		local := newLocalGitRepo(t)
		created := createTestRepository(t, pool, repository.Repository{
			Name:      "local-only",
			LocalPath: &local,
		})

		if created.ID == 0 {
			t.Fatal("CreateRepository() returned zero ID")
		}
		if created.LocalPath == nil || *created.LocalPath != local {
			t.Fatalf("LocalPath = %v, want %q", created.LocalPath, local)
		}
		if created.RemoteURL != nil {
			t.Fatalf("RemoteURL = %v, want nil", created.RemoteURL)
		}

		got, err := GetRepository(ctx, pool, created.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if got.ID != created.ID || got.Name != created.Name {
			t.Fatalf("GetRepository() = %+v, want id/name matching %+v", got, created)
		}
		if got.LocalPath == nil || *got.LocalPath != local {
			t.Fatalf("LocalPath = %v, want %q", got.LocalPath, local)
		}
		if got.RemoteURL != nil {
			t.Fatalf("RemoteURL = %v, want nil", got.RemoteURL)
		}
	})

	t.Run("remote-only repository can be created and read back", func(t *testing.T) {
		remote := "https://github.com/foo/remote-only.git"
		created := createTestRepository(t, pool, repository.Repository{
			Name:      "remote-only",
			RemoteURL: &remote,
		})

		if created.LocalPath != nil {
			t.Fatalf("LocalPath = %v, want nil", created.LocalPath)
		}
		if created.RemoteURL == nil || *created.RemoteURL != remote {
			t.Fatalf("RemoteURL = %v, want %q", created.RemoteURL, remote)
		}

		got, err := GetRepository(ctx, pool, created.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if got.LocalPath != nil {
			t.Fatalf("LocalPath = %v, want nil", got.LocalPath)
		}
		if got.RemoteURL == nil || *got.RemoteURL != remote {
			t.Fatalf("RemoteURL = %v, want %q", got.RemoteURL, remote)
		}
	})

	t.Run("repository with both locators persists both", func(t *testing.T) {
		local := newLocalGitRepo(t)
		remote := "https://github.com/foo/both.git"
		created := createTestRepository(t, pool, repository.Repository{
			Name:      "both-locators",
			LocalPath: &local,
			RemoteURL: &remote,
		})

		got, err := GetRepository(ctx, pool, created.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if got.LocalPath == nil || *got.LocalPath != local {
			t.Fatalf("LocalPath = %v, want %q", got.LocalPath, local)
		}
		if got.RemoteURL == nil || *got.RemoteURL != remote {
			t.Fatalf("RemoteURL = %v, want %q", got.RemoteURL, remote)
		}
	})

	t.Run("id is stable across create, get, and update", func(t *testing.T) {
		remote := "https://github.com/foo/stable-id.git"
		created := createTestRepository(t, pool, repository.Repository{
			Name:      "stable-id",
			RemoteURL: &remote,
		})

		got, err := GetRepository(ctx, pool, created.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if got.ID != created.ID {
			t.Fatalf("GetRepository() ID = %d, want %d", got.ID, created.ID)
		}

		newName := "stable-id-renamed"
		updated, err := UpdateRepository(ctx, pool, created.ID, UpdateRepositoryParams{Name: &newName})
		if err != nil {
			t.Fatalf("UpdateRepository() error = %v", err)
		}
		if updated.ID != created.ID {
			t.Fatalf("UpdateRepository() ID = %d, want %d", updated.ID, created.ID)
		}
	})

	t.Run("name can be updated in isolation, leaving locators untouched", func(t *testing.T) {
		local := newLocalGitRepo(t)
		remote := "https://github.com/foo/name-update.git"
		created := createTestRepository(t, pool, repository.Repository{
			Name:      "before-rename",
			LocalPath: &local,
			RemoteURL: &remote,
		})

		newName := "after-rename"
		updated, err := UpdateRepository(ctx, pool, created.ID, UpdateRepositoryParams{Name: &newName})
		if err != nil {
			t.Fatalf("UpdateRepository() error = %v", err)
		}
		if updated.Name != newName {
			t.Fatalf("Name = %q, want %q", updated.Name, newName)
		}
		if updated.LocalPath == nil || *updated.LocalPath != local {
			t.Fatalf("LocalPath = %v, want unchanged %q", updated.LocalPath, local)
		}
		if updated.RemoteURL == nil || *updated.RemoteURL != remote {
			t.Fatalf("RemoteURL = %v, want unchanged %q", updated.RemoteURL, remote)
		}

		got, err := GetRepository(ctx, pool, created.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if got.Name != newName {
			t.Fatalf("persisted Name = %q, want %q", got.Name, newName)
		}
	})

	t.Run("explicit null clears a locator, transitioning between states", func(t *testing.T) {
		local := newLocalGitRepo(t)
		remote := "https://github.com/foo/clear-locator.git"
		created := createTestRepository(t, pool, repository.Repository{
			Name:      "clear-locator",
			LocalPath: &local,
			RemoteURL: &remote,
		})

		// Clear RemoteURL, transitioning to local-only.
		updated, err := UpdateRepository(ctx, pool, created.ID, UpdateRepositoryParams{
			RemoteURL: SetLocator(nil),
		})
		if err != nil {
			t.Fatalf("UpdateRepository() error = %v", err)
		}
		if updated.RemoteURL != nil {
			t.Fatalf("RemoteURL = %v, want nil", updated.RemoteURL)
		}
		if updated.LocalPath == nil || *updated.LocalPath != local {
			t.Fatalf("LocalPath = %v, want unchanged %q", updated.LocalPath, local)
		}

		got, err := GetRepository(ctx, pool, created.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if got.RemoteURL != nil {
			t.Fatalf("persisted RemoteURL = %v, want nil", got.RemoteURL)
		}

		// Now clear LocalPath and set RemoteURL back, transitioning to
		// remote-only in a single update.
		updated, err = UpdateRepository(ctx, pool, created.ID, UpdateRepositoryParams{
			LocalPath: SetLocator(nil),
			RemoteURL: SetLocator(&remote),
		})
		if err != nil {
			t.Fatalf("UpdateRepository() error = %v", err)
		}
		if updated.LocalPath != nil {
			t.Fatalf("LocalPath = %v, want nil", updated.LocalPath)
		}
		if updated.RemoteURL == nil || *updated.RemoteURL != remote {
			t.Fatalf("RemoteURL = %v, want %q", updated.RemoteURL, remote)
		}
	})

	t.Run("update that would clear the only locator leaves the row unchanged", func(t *testing.T) {
		local := newLocalGitRepo(t)
		created := createTestRepository(t, pool, repository.Repository{
			Name:      "invalid-update",
			LocalPath: &local,
		})

		_, err := UpdateRepository(ctx, pool, created.ID, UpdateRepositoryParams{
			LocalPath: SetLocator(nil),
		})
		if !errors.Is(err, repository.ErrNoLocator) {
			t.Fatalf("UpdateRepository() error = %v, want %v", err, repository.ErrNoLocator)
		}

		got, err := GetRepository(ctx, pool, created.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if got.Name != created.Name {
			t.Fatalf("Name = %q, want unchanged %q", got.Name, created.Name)
		}
		if got.LocalPath == nil || *got.LocalPath != local {
			t.Fatalf("LocalPath = %v, want unchanged %q", got.LocalPath, local)
		}
		if got.RemoteURL != nil {
			t.Fatalf("RemoteURL = %v, want unchanged nil", got.RemoteURL)
		}
	})

	t.Run("legacy rows with NULL repo_path read back with a nil LocalPath", func(t *testing.T) {
		// Bypasses CreateRepository (which always canonicalizes/validates
		// on write) to reproduce a row shaped like data written before
		// remote_url existed, or a row whose repo_path was cleared and
		// only remote_url remains -- exercising the read path's handling
		// of NULL repo_path directly against the physical column.
		remote := "https://github.com/foo/legacy-null-repo-path.git"
		var id int64
		row := pool.QueryRow(ctx,
			`INSERT INTO projects (name, repo_path, remote_url) VALUES ($1, NULL, $2) RETURNING id`,
			"legacy-null-repo-path", remote,
		)
		if err := row.Scan(&id); err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
		t.Cleanup(func() { deleteRepositoryQuietly(t, pool, id) })

		got, err := GetRepository(ctx, pool, id)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if got.LocalPath != nil {
			t.Fatalf("LocalPath = %v, want nil for legacy NULL repo_path", got.LocalPath)
		}
		if got.RemoteURL == nil || *got.RemoteURL != remote {
			t.Fatalf("RemoteURL = %v, want %q", got.RemoteURL, remote)
		}

		all, err := ListRepositories(ctx, pool)
		if err != nil {
			t.Fatalf("ListRepositories() error = %v", err)
		}
		found := false
		for _, r := range all {
			if r.ID == id {
				found = true
				if r.LocalPath != nil {
					t.Fatalf("ListRepositories() LocalPath = %v, want nil", r.LocalPath)
				}
			}
		}
		if !found {
			t.Fatalf("ListRepositories() did not include legacy row id %d", id)
		}
	})

	t.Run("not-found is mapped to ErrNotFound for get, update, and delete", func(t *testing.T) {
		remote := "https://github.com/foo/not-found.git"
		created := createTestRepository(t, pool, repository.Repository{
			Name:      "will-be-deleted",
			RemoteURL: &remote,
		})

		if err := DeleteRepository(ctx, pool, created.ID); err != nil {
			t.Fatalf("DeleteRepository() error = %v", err)
		}

		if _, err := GetRepository(ctx, pool, created.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetRepository() on deleted id error = %v, want %v", err, ErrNotFound)
		}

		newName := "irrelevant"
		if _, err := UpdateRepository(ctx, pool, created.ID, UpdateRepositoryParams{Name: &newName}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("UpdateRepository() on deleted id error = %v, want %v", err, ErrNotFound)
		}

		if err := DeleteRepository(ctx, pool, created.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("DeleteRepository() on already-deleted id error = %v, want %v", err, ErrNotFound)
		}

		// A never-issued id (far beyond any sequence value used by this
		// test run) must also map to ErrNotFound rather than, say, a
		// scan error.
		const neverIssuedID int64 = 1 << 40
		if _, err := GetRepository(ctx, pool, neverIssuedID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetRepository() on never-issued id error = %v, want %v", err, ErrNotFound)
		}
	})
}
