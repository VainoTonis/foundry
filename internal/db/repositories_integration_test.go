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

// TestSpecToWorkflowExecution_Postgres exercises the full spec-to-workflow
// ownership chain (CreateSpec -> ListSpecs filter -> CreateWorkflow ->
// UpdateWorkflowStatus -> WorkflowTotalCost) against real PostgreSQL, for
// both a local-only and a remote-only Repository. Externally this chain is
// expressed in terms of Repository/RepositoryID (Spec.RepositoryID,
// ListSpecsFilter.RepositoryID); physically it is specs.repository_id.
func TestSpecToWorkflowExecution_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		repo repository.Repository
	}{
		{"local-only repository", repository.Repository{Name: "spec-workflow-local"}},
		{"remote-only repository", repository.Repository{Name: "spec-workflow-remote"}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repo := tc.repo
			if tc.name == "local-only repository" {
				local := newLocalGitRepo(t)
				repo.LocalPath = &local
			} else {
				remote := "https://github.com/foo/" + tc.repo.Name + ".git"
				repo.RemoteURL = &remote
			}
			createdRepo := createTestRepository(t, pool, repo)

			spec, err := CreateSpec(ctx, pool, createdRepo.ID, "spec title", "spec content", []byte(`[]`))
			if err != nil {
				t.Fatalf("CreateSpec() error = %v", err)
			}
			t.Cleanup(func() { _ = DeleteSpec(context.Background(), pool, spec.ID) })
			if spec.RepositoryID != createdRepo.ID {
				t.Fatalf("Spec.RepositoryID = %d, want %d", spec.RepositoryID, createdRepo.ID)
			}

			gotSpec, err := GetSpec(ctx, pool, spec.ID)
			if err != nil {
				t.Fatalf("GetSpec() error = %v", err)
			}
			if gotSpec.RepositoryID != createdRepo.ID {
				t.Fatalf("GetSpec().RepositoryID = %d, want %d", gotSpec.RepositoryID, createdRepo.ID)
			}

			listed, err := ListSpecs(ctx, pool, ListSpecsFilter{RepositoryID: createdRepo.ID})
			if err != nil {
				t.Fatalf("ListSpecs() error = %v", err)
			}
			found := false
			for _, s := range listed {
				if s.ID == spec.ID {
					found = true
				}
				if s.RepositoryID != createdRepo.ID {
					t.Fatalf("ListSpecs() returned spec %d owned by repository %d, want only %d", s.ID, s.RepositoryID, createdRepo.ID)
				}
			}
			if !found {
				t.Fatalf("ListSpecs(RepositoryID=%d) = %+v, want spec %d present", createdRepo.ID, listed, spec.ID)
			}

			wf, err := CreateWorkflow(ctx, pool, spec.ID, "poc", nil)
			if err != nil {
				t.Fatalf("CreateWorkflow() error = %v", err)
			}
			t.Cleanup(func() { _ = DeleteWorkflow(context.Background(), pool, wf.ID) })

			workflows, err := ListWorkflowsBySpec(ctx, pool, spec.ID)
			if err != nil {
				t.Fatalf("ListWorkflowsBySpec() error = %v", err)
			}
			if len(workflows) != 1 || workflows[0].ID != wf.ID {
				t.Fatalf("ListWorkflowsBySpec() = %+v, want single workflow with id %d", workflows, wf.ID)
			}

			if err := UpdateWorkflowStatus(ctx, pool, wf.ID, "done"); err != nil {
				t.Fatalf("UpdateWorkflowStatus() error = %v", err)
			}
			gotWf, err := GetWorkflow(ctx, pool, wf.ID)
			if err != nil {
				t.Fatalf("GetWorkflow() error = %v", err)
			}
			if gotWf.Status != "done" {
				t.Fatalf("GetWorkflow().Status = %q, want %q", gotWf.Status, "done")
			}
			if gotWf.FinishedAt == nil {
				t.Fatal("GetWorkflow().FinishedAt = nil, want set after transitioning to done")
			}

			cost, err := WorkflowTotalCost(ctx, pool, wf.ID)
			if err != nil {
				t.Fatalf("WorkflowTotalCost() error = %v", err)
			}
			if cost != 0 {
				t.Fatalf("WorkflowTotalCost() = %v, want 0 with no phases", cost)
			}
		})
	}
}

// TestDraftSave_Postgres exercises spec draft save round-trips (create,
// get, get-by-cerberus-session, update) against real PostgreSQL, for both
// a local-only and a remote-only Repository, as well as an unattached
// (nil RepositoryID) draft. Externally this uses SpecDraft.RepositoryID;
// physically it is spec_drafts.repository_id.
func TestDraftSave_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("draft owned by a local-only repository saves and reads back", func(t *testing.T) {
		local := newLocalGitRepo(t)
		repo := createTestRepository(t, pool, repository.Repository{Name: "draft-save-local", LocalPath: &local})

		draft, err := CreateSpecDraft(ctx, pool, &repo.ID, "draft title")
		if err != nil {
			t.Fatalf("CreateSpecDraft() error = %v", err)
		}
		t.Cleanup(func() { _ = DeleteSpecDraft(context.Background(), pool, draft.ID) })
		if draft.RepositoryID == nil || *draft.RepositoryID != repo.ID {
			t.Fatalf("SpecDraft.RepositoryID = %v, want %d", draft.RepositoryID, repo.ID)
		}

		got, err := GetSpecDraft(ctx, pool, draft.ID)
		if err != nil {
			t.Fatalf("GetSpecDraft() error = %v", err)
		}
		if got.RepositoryID == nil || *got.RepositoryID != repo.ID {
			t.Fatalf("GetSpecDraft().RepositoryID = %v, want %d", got.RepositoryID, repo.ID)
		}

		session := "draft-save-local-session"
		updated, err := UpdateSpecDraft(ctx, pool, draft.ID, UpdateSpecDraftParams{CerberusSession: &session})
		if err != nil {
			t.Fatalf("UpdateSpecDraft() error = %v", err)
		}
		if updated.CerberusSession != session {
			t.Fatalf("UpdateSpecDraft().CerberusSession = %q, want %q", updated.CerberusSession, session)
		}
		if updated.RepositoryID == nil || *updated.RepositoryID != repo.ID {
			t.Fatalf("UpdateSpecDraft().RepositoryID = %v, want unchanged %d", updated.RepositoryID, repo.ID)
		}

		bySession, err := GetSpecDraftByCerberusSession(ctx, pool, session)
		if err != nil {
			t.Fatalf("GetSpecDraftByCerberusSession() error = %v", err)
		}
		if bySession.ID != draft.ID {
			t.Fatalf("GetSpecDraftByCerberusSession().ID = %d, want %d", bySession.ID, draft.ID)
		}
		if bySession.RepositoryID == nil || *bySession.RepositoryID != repo.ID {
			t.Fatalf("GetSpecDraftByCerberusSession().RepositoryID = %v, want %d", bySession.RepositoryID, repo.ID)
		}
	})

	t.Run("draft owned by a remote-only repository saves and reads back", func(t *testing.T) {
		remote := "https://github.com/foo/draft-save-remote.git"
		repo := createTestRepository(t, pool, repository.Repository{Name: "draft-save-remote", RemoteURL: &remote})

		draft, err := CreateSpecDraft(ctx, pool, &repo.ID, "draft title")
		if err != nil {
			t.Fatalf("CreateSpecDraft() error = %v", err)
		}
		t.Cleanup(func() { _ = DeleteSpecDraft(context.Background(), pool, draft.ID) })
		if draft.RepositoryID == nil || *draft.RepositoryID != repo.ID {
			t.Fatalf("SpecDraft.RepositoryID = %v, want %d", draft.RepositoryID, repo.ID)
		}

		got, err := GetSpecDraft(ctx, pool, draft.ID)
		if err != nil {
			t.Fatalf("GetSpecDraft() error = %v", err)
		}
		if got.RepositoryID == nil || *got.RepositoryID != repo.ID {
			t.Fatalf("GetSpecDraft().RepositoryID = %v, want %d", got.RepositoryID, repo.ID)
		}
	})

	t.Run("draft with no repository saves and reads back with a nil RepositoryID", func(t *testing.T) {
		draft, err := CreateSpecDraft(ctx, pool, nil, "unattached draft")
		if err != nil {
			t.Fatalf("CreateSpecDraft() error = %v", err)
		}
		t.Cleanup(func() { _ = DeleteSpecDraft(context.Background(), pool, draft.ID) })
		if draft.RepositoryID != nil {
			t.Fatalf("SpecDraft.RepositoryID = %v, want nil", draft.RepositoryID)
		}

		got, err := GetSpecDraft(ctx, pool, draft.ID)
		if err != nil {
			t.Fatalf("GetSpecDraft() error = %v", err)
		}
		if got.RepositoryID != nil {
			t.Fatalf("GetSpecDraft().RepositoryID = %v, want nil", got.RepositoryID)
		}
	})
}

func TestListKnownCerberusSessionsPage_TiedSources_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	at := time.Date(2095, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, name := range []string{"known-page-tie-z", "known-page-tie-x"} {
		draft, err := CreateSpecDraft(ctx, pool, nil, name)
		if err != nil {
			t.Fatalf("CreateSpecDraft(%q): %v", name, err)
		}
		t.Cleanup(func() { _ = DeleteSpecDraft(context.Background(), pool, draft.ID) })
		if _, err := pool.Exec(ctx, `UPDATE spec_drafts SET cerberus_session = $1, updated_at = $2 WHERE id = $3`, name, at, draft.ID); err != nil {
			t.Fatalf("update draft session %q: %v", name, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO external_cerberus_sessions (session, status, first_seen_at, last_seen_at) VALUES ($1, 'active', $2, $2)`, "known-page-tie-y", at); err != nil {
		t.Fatalf("insert external session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM external_cerberus_sessions WHERE session = $1`, "known-page-tie-y")
	})

	first, err := ListKnownCerberusSessionsPage(ctx, pool, KnownCerberusSessionPageParams{Limit: 2})
	if err != nil {
		t.Fatalf("ListKnownCerberusSessionsPage(first): %v", err)
	}
	if len(first) != 2 || first[0].Session != "known-page-tie-z" || first[1].Session != "known-page-tie-y" {
		t.Fatalf("first tied page = %+v, want z then y", first)
	}
	cursorAt, cursorSession := first[1].LastUpdatedAt, first[1].Session
	second, err := ListKnownCerberusSessionsPage(ctx, pool, KnownCerberusSessionPageParams{
		Limit: 2, BeforeAt: &cursorAt, BeforeSession: cursorSession,
	})
	if err != nil {
		t.Fatalf("ListKnownCerberusSessionsPage(second): %v", err)
	}
	if len(second) == 0 || second[0].Session != "known-page-tie-x" {
		t.Fatalf("second tied page = %+v, want x first", second)
	}
	seen := map[string]bool{}
	for _, rows := range [][]KnownCerberusSession{first, second} {
		for _, row := range rows {
			if seen[row.Session] {
				t.Fatalf("session %q duplicated across pages", row.Session)
			}
			seen[row.Session] = true
		}
	}
	for _, name := range []string{"known-page-tie-x", "known-page-tie-y", "known-page-tie-z"} {
		if !seen[name] {
			t.Fatalf("session %q omitted across tied pages", name)
		}
	}
}

// TestCerberusSessionLookup_Postgres exercises ListKnownCerberusSessions
// against real PostgreSQL for both a workflow-phase-backed session (owned,
// via specs.repository_id, by a local-only Repository) and a spec-draft-backed
// session (owned by a remote-only Repository), confirming the returned
// RepositoryID/RepositoryName/RepositoryLocalPath fields reflect the
// correct Repository via the physical join through repository_id.
func TestCerberusSessionLookup_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("a terminal workflow phase session resolves to its local-only repository", func(t *testing.T) {
		local := newLocalGitRepo(t)
		repo := createTestRepository(t, pool, repository.Repository{Name: "cerberus-lookup-local", LocalPath: &local})

		spec, err := CreateSpec(ctx, pool, repo.ID, "cerberus lookup spec", "content", []byte(`[]`))
		if err != nil {
			t.Fatalf("CreateSpec() error = %v", err)
		}
		t.Cleanup(func() { _ = DeleteSpec(context.Background(), pool, spec.ID) })

		wf, err := CreateWorkflow(ctx, pool, spec.ID, "poc", nil)
		if err != nil {
			t.Fatalf("CreateWorkflow() error = %v", err)
		}
		t.Cleanup(func() { _ = DeleteWorkflow(context.Background(), pool, wf.ID) })

		session := "cerberus-lookup-local-session"
		var phaseID int64
		row := pool.QueryRow(ctx,
			`INSERT INTO phases (workflow_id, position, name, goal, status, cerberus_session, started_at, finished_at, review_verdict, cerberus_commit)
			 VALUES ($1, 1, 'phase-1', 'goal', 'done', $2, now(), now(), 'pass', 'deadbeef') RETURNING id`,
			wf.ID, session,
		)
		if err := row.Scan(&phaseID); err != nil {
			t.Fatalf("insert phase: %v", err)
		}

		sessions, err := ListKnownCerberusSessions(ctx, pool)
		if err != nil {
			t.Fatalf("ListKnownCerberusSessions() error = %v", err)
		}
		var found *KnownCerberusSession
		for i := range sessions {
			if sessions[i].Session == session {
				found = &sessions[i]
			}
		}
		if found == nil {
			t.Fatalf("ListKnownCerberusSessions() did not include session %q", session)
		}
		if found.Type != "workflow_phase" {
			t.Fatalf("Type = %q, want %q", found.Type, "workflow_phase")
		}
		if found.RepositoryID == nil || *found.RepositoryID != repo.ID {
			t.Fatalf("RepositoryID = %v, want %d", found.RepositoryID, repo.ID)
		}
		if found.RepositoryName != repo.Name {
			t.Fatalf("RepositoryName = %q, want %q", found.RepositoryName, repo.Name)
		}
		if found.RepositoryLocalPath != local {
			t.Fatalf("RepositoryLocalPath = %q, want %q", found.RepositoryLocalPath, local)
		}
		if !found.SafeToClean {
			t.Fatal("SafeToClean = false for a done phase, want true")
		}
	})

	t.Run("an active spec-draft session resolves to its remote-only repository", func(t *testing.T) {
		remote := "https://github.com/foo/cerberus-lookup-remote.git"
		repo := createTestRepository(t, pool, repository.Repository{Name: "cerberus-lookup-remote", RemoteURL: &remote})

		session := "cerberus-lookup-remote-session"
		draft, err := CreateSpecDraft(ctx, pool, &repo.ID, "remote draft")
		if err != nil {
			t.Fatalf("CreateSpecDraft() error = %v", err)
		}
		t.Cleanup(func() { _ = DeleteSpecDraft(context.Background(), pool, draft.ID) })
		if _, err := UpdateSpecDraft(ctx, pool, draft.ID, UpdateSpecDraftParams{CerberusSession: &session}); err != nil {
			t.Fatalf("UpdateSpecDraft() error = %v", err)
		}

		sessions, err := ListKnownCerberusSessions(ctx, pool)
		if err != nil {
			t.Fatalf("ListKnownCerberusSessions() error = %v", err)
		}
		var found *KnownCerberusSession
		for i := range sessions {
			if sessions[i].Session == session {
				found = &sessions[i]
			}
		}
		if found == nil {
			t.Fatalf("ListKnownCerberusSessions() did not include session %q", session)
		}
		if found.Type != "spec_draft" {
			t.Fatalf("Type = %q, want %q", found.Type, "spec_draft")
		}
		if found.RepositoryID == nil || *found.RepositoryID != repo.ID {
			t.Fatalf("RepositoryID = %v, want %d", found.RepositoryID, repo.ID)
		}
		if found.RepositoryName != repo.Name {
			t.Fatalf("RepositoryName = %q, want %q", found.RepositoryName, repo.Name)
		}
		// remote-only repository has no local_path; the lookup's COALESCE
		// must surface that as an empty string, not fail to scan.
		if found.RepositoryLocalPath != "" {
			t.Fatalf("RepositoryLocalPath = %q, want empty for remote-only repository", found.RepositoryLocalPath)
		}
		if found.SafeToClean {
			t.Fatal("SafeToClean = true for an active draft, want false")
		}
	})
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

	t.Run("legacy rows with NULL local_path read back with a nil LocalPath", func(t *testing.T) {
		// Bypasses CreateRepository (which always canonicalizes/validates
		// on write) to reproduce a row shaped like data written before
		// remote_url existed, or a row whose local_path was cleared and
		// only remote_url remains -- exercising the read path's handling
		// of NULL local_path directly against the physical column.
		remote := "https://github.com/foo/legacy-null-repo-path.git"
		var id int64
		row := pool.QueryRow(ctx,
			`INSERT INTO repositories (name, local_path, remote_url) VALUES ($1, NULL, $2) RETURNING id`,
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
			t.Fatalf("LocalPath = %v, want nil for legacy NULL local_path", got.LocalPath)
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
