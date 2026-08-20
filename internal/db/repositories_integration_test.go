package db

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tonis2/foundry/internal/repository"
)

// runGitCmd runs a git subcommand in dir, failing the test on error. It
// builds refresh fixtures (an "origin" remote, or a deliberately
// malformed one) that RefreshRepositories is then exercised against.
func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, out)
	}
}

// findRefreshResult returns the RefreshResult for id, failing the test if
// RefreshRepositories did not report one -- every repository eligible for
// refresh (has a LocalPath) must appear exactly once in the results,
// whether it succeeded or failed.
func findRefreshResult(t *testing.T, results []RefreshResult, id int64) RefreshResult {
	t.Helper()
	for _, r := range results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("RefreshRepositories() did not report a result for id %d", id)
	return RefreshResult{}
}

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

	t.Run("refresh fills in the missing origin remote from the local worktree", func(t *testing.T) {
		requireGit(t)
		local := newLocalGitRepo(t)
		runGitCmd(t, local, "remote", "add", "origin", "https://example.com/foo/refresh-missing.git")

		created := createTestRepository(t, pool, repository.Repository{
			Name:      "refresh-missing-origin",
			LocalPath: &local,
		})
		if created.RemoteURL != nil {
			t.Fatalf("RemoteURL = %v, want nil before refresh", created.RemoteURL)
		}

		results, err := RefreshRepositories(ctx, pool)
		if err != nil {
			t.Fatalf("RefreshRepositories() error = %v", err)
		}
		result := findRefreshResult(t, results, created.ID)
		if result.Err != nil {
			t.Fatalf("RefreshResult.Err = %v, want nil", result.Err)
		}
		if !result.Updated {
			t.Fatal("RefreshResult.Updated = false, want true")
		}

		got, err := GetRepository(ctx, pool, created.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		want := "https://example.com/foo/refresh-missing.git"
		if got.RemoteURL == nil || *got.RemoteURL != want {
			t.Fatalf("RemoteURL = %v, want %q", got.RemoteURL, want)
		}

		// Refreshing again is idempotent: RemoteURL is now set, so the row
		// is skipped entirely (not merely re-set to the same value), and a
		// second refresh reports it as not updated.
		results, err = RefreshRepositories(ctx, pool)
		if err != nil {
			t.Fatalf("RefreshRepositories() second call error = %v", err)
		}
		for _, r := range results {
			if r.ID == created.ID {
				t.Fatalf("RefreshRepositories() second call unexpectedly touched id %d: %+v", created.ID, r)
			}
		}

		gotAgain, err := GetRepository(ctx, pool, created.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if gotAgain.RemoteURL == nil || *gotAgain.RemoteURL != want {
			t.Fatalf("RemoteURL after second refresh = %v, want unchanged %q", gotAgain.RemoteURL, want)
		}
	})

	t.Run("refresh never overwrites an already-configured remote", func(t *testing.T) {
		requireGit(t)
		local := newLocalGitRepo(t)
		// The local worktree's origin deliberately differs from the
		// configured remote, so a bug that refreshed unconditionally would
		// be caught by the assertion below.
		runGitCmd(t, local, "remote", "add", "origin", "https://example.com/foo/local-origin-should-be-ignored.git")

		configured := "https://example.com/foo/configured-remote.git"
		created := createTestRepository(t, pool, repository.Repository{
			Name:      "refresh-no-overwrite",
			LocalPath: &local,
			RemoteURL: &configured,
		})

		results, err := RefreshRepositories(ctx, pool)
		if err != nil {
			t.Fatalf("RefreshRepositories() error = %v", err)
		}
		for _, r := range results {
			if r.ID == created.ID {
				t.Fatalf("RefreshRepositories() touched id %d which already had a configured remote: %+v", created.ID, r)
			}
		}

		got, err := GetRepository(ctx, pool, created.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if got.RemoteURL == nil || *got.RemoteURL != configured {
			t.Fatalf("RemoteURL = %v, want unchanged configured value %q", got.RemoteURL, configured)
		}
	})

	t.Run("refresh reports missing and malformed origins as per-row failures without aborting other rows", func(t *testing.T) {
		requireGit(t)

		noOrigin := newLocalGitRepo(t) // no "origin" remote configured
		noOriginRepo := createTestRepository(t, pool, repository.Repository{
			Name:      "refresh-no-origin",
			LocalPath: &noOrigin,
		})

		malformed := newLocalGitRepo(t)
		runGitCmd(t, malformed, "remote", "add", "origin", "not a url")
		malformedRepo := createTestRepository(t, pool, repository.Repository{
			Name:      "refresh-malformed-origin",
			LocalPath: &malformed,
		})

		healthy := newLocalGitRepo(t)
		runGitCmd(t, healthy, "remote", "add", "origin", "https://example.com/foo/refresh-healthy.git")
		healthyRepo := createTestRepository(t, pool, repository.Repository{
			Name:      "refresh-healthy",
			LocalPath: &healthy,
		})

		results, err := RefreshRepositories(ctx, pool)
		if err != nil {
			t.Fatalf("RefreshRepositories() error = %v", err)
		}

		noOriginResult := findRefreshResult(t, results, noOriginRepo.ID)
		if noOriginResult.Err == nil {
			t.Fatal("RefreshResult.Err = nil for a repository with no origin remote, want an error")
		}
		if noOriginResult.Updated {
			t.Fatal("RefreshResult.Updated = true for a failed refresh, want false")
		}

		malformedResult := findRefreshResult(t, results, malformedRepo.ID)
		if malformedResult.Err == nil {
			t.Fatal("RefreshResult.Err = nil for a repository with a malformed origin url, want an error")
		}
		if malformedResult.Updated {
			t.Fatal("RefreshResult.Updated = true for a failed refresh, want false")
		}

		healthyResult := findRefreshResult(t, results, healthyRepo.ID)
		if healthyResult.Err != nil {
			t.Fatalf("RefreshResult.Err = %v for a healthy repository, want nil", healthyResult.Err)
		}
		if !healthyResult.Updated {
			t.Fatal("RefreshResult.Updated = false for a healthy repository, want true")
		}

		// The failed rows must be left untouched: neither cleared nor
		// spuriously populated.
		gotNoOrigin, err := GetRepository(ctx, pool, noOriginRepo.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if gotNoOrigin.RemoteURL != nil {
			t.Fatalf("RemoteURL = %v, want unchanged nil after failed refresh", gotNoOrigin.RemoteURL)
		}

		gotMalformed, err := GetRepository(ctx, pool, malformedRepo.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		if gotMalformed.RemoteURL != nil {
			t.Fatalf("RemoteURL = %v, want unchanged nil after failed refresh", gotMalformed.RemoteURL)
		}

		gotHealthy, err := GetRepository(ctx, pool, healthyRepo.ID)
		if err != nil {
			t.Fatalf("GetRepository() error = %v", err)
		}
		want := "https://example.com/foo/refresh-healthy.git"
		if gotHealthy.RemoteURL == nil || *gotHealthy.RemoteURL != want {
			t.Fatalf("RemoteURL = %v, want %q", gotHealthy.RemoteURL, want)
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
