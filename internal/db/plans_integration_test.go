package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tonis2/foundry/internal/repository"
)

// countPlansByTitle returns how many rows in plans currently have the
// given title. Test cases use unique, per-case titles so this doubles as
// an existence check without needing a returned plan id (e.g. after a
// CreatePlan call that is expected to fail and leave nothing behind).
func countPlansByTitle(t *testing.T, pool *pgxpool.Pool, title string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM plans WHERE title = $1`, title).Scan(&n); err != nil {
		t.Fatalf("count plans by title %q: %v", title, err)
	}
	return n
}

// createTestPlanRepo is a small wrapper around createTestRepository that
// gives each repository a unique name derived from t.Name(), so plan
// tests in this file can freely create several repositories per subtest
// without name collisions.
func createTestPlanRepo(t *testing.T, pool *pgxpool.Pool, suffix string) repository.Repository {
	t.Helper()
	remote := "https://example.com/foo/plan-" + suffix + ".git"
	return createTestRepository(t, pool, repository.Repository{
		Name:      "plan-repo-" + suffix,
		RemoteURL: &remote,
	})
}

// deleteTestPlan removes a plan row directly (there is no DeletePlan),
// ignoring errors so it is safe to use unconditionally from t.Cleanup.
func deleteTestPlan(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DELETE FROM plans WHERE id = $1`, id); err != nil {
		t.Errorf("cleanup delete plan %d: %v", id, err)
	}
}

// TestCreatePlan_InvalidRepositoryIDs_Postgres exercises CreatePlan's
// validation and transactional rollback behavior: an empty repository id
// list, a duplicate repository id, and a repository id that does not exist
// must all fail without persisting a plan row or any plan_repositories
// rows.
func TestCreatePlan_InvalidRepositoryIDs_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("empty repository id list errors and persists nothing", func(t *testing.T) {
		title := "plan-empty-project-ids"
		_, err := CreatePlan(ctx, pool, nil, title, "summary", "content")
		if err == nil {
			t.Fatal("CreatePlan() error = nil, want an error for an empty repository id list")
		}
		if got := countPlansByTitle(t, pool, title); got != 0 {
			t.Fatalf("plans with title %q after failed CreatePlan() = %d, want 0", title, got)
		}
	})

	t.Run("duplicate repository id errors and persists nothing", func(t *testing.T) {
		repo := createTestPlanRepo(t, pool, "dup")

		title := "plan-duplicate-project-id"
		_, err := CreatePlan(ctx, pool, []int64{repo.ID, repo.ID}, title, "summary", "content")
		if err == nil {
			t.Fatal("CreatePlan() error = nil, want an error for a duplicated repository id")
		}
		if got := countPlansByTitle(t, pool, title); got != 0 {
			t.Fatalf("plans with title %q after failed CreatePlan() = %d, want 0", title, got)
		}

		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM plan_repositories WHERE repository_id = $1`, repo.ID).Scan(&n); err != nil {
			t.Fatalf("count plan_repositories: %v", err)
		}
		if n != 0 {
			t.Fatalf("plan_repositories rows for repository %d = %d, want 0", repo.ID, n)
		}
	})

	t.Run("unknown repository id errors and rolls back the whole transaction", func(t *testing.T) {
		repo := createTestPlanRepo(t, pool, "unknown-sibling")
		const neverIssuedRepositoryID int64 = 1 << 40

		title := "plan-unknown-project-id"
		_, err := CreatePlan(ctx, pool, []int64{repo.ID, neverIssuedRepositoryID}, title, "summary", "content")
		if err == nil {
			t.Fatal("CreatePlan() error = nil, want an error for an unknown repository id")
		}
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("CreatePlan() error = %v, want it to wrap ErrNotFound", err)
		}
		if got := countPlansByTitle(t, pool, title); got != 0 {
			t.Fatalf("plans with title %q after failed CreatePlan() = %d, want 0", title, got)
		}

		// The valid sibling repository id must not have been left with a
		// plan_repositories row from the rolled-back transaction either.
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM plan_repositories WHERE repository_id = $1`, repo.ID).Scan(&n); err != nil {
			t.Fatalf("count plan_repositories: %v", err)
		}
		if n != 0 {
			t.Fatalf("plan_repositories rows for repository %d = %d, want 0 after rollback", repo.ID, n)
		}
	})
}

// TestPlanRepositories_RoundTrip_Postgres exercises a plan owned by more
// than one repository end to end: CreatePlan's returned Repositories,
// GetPlan, ListPlans, GetPlanByWorkflow, and UpdatePlan must all report
// the same ordered repository membership, with position 0 always the
// primary (first-listed) repository.
func TestPlanRepositories_RoundTrip_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	primary := createTestPlanRepo(t, pool, "primary")
	secondary := createTestPlanRepo(t, pool, "secondary")
	tertiary := createTestPlanRepo(t, pool, "tertiary")

	title := "plan-multi-repo-roundtrip"
	created, err := CreatePlan(ctx, pool, []int64{primary.ID, secondary.ID, tertiary.ID}, title, "summary", "content")
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	t.Cleanup(func() { deleteTestPlan(t, pool, created.ID) })

	assertOrderedRepos := func(t *testing.T, label string, repos []PlanRepository) {
		t.Helper()
		if len(repos) != 3 {
			t.Fatalf("%s Repositories = %+v, want 3 entries", label, repos)
		}
		wantOrder := []int64{primary.ID, secondary.ID, tertiary.ID}
		for i, want := range wantOrder {
			if repos[i].Position != i {
				t.Fatalf("%s Repositories[%d].Position = %d, want %d", label, i, repos[i].Position, i)
			}
			if repos[i].RepositoryID != want {
				t.Fatalf("%s Repositories[%d].RepositoryID = %d, want %d", label, i, repos[i].RepositoryID, want)
			}
			if repos[i].Repository.ID != want {
				t.Fatalf("%s Repositories[%d].Repository.ID = %d, want %d", label, i, repos[i].Repository.ID, want)
			}
		}
		if repos[0].RepositoryID != primary.ID {
			t.Fatalf("%s Repositories[0] (primary) = %d, want %d", label, repos[0].RepositoryID, primary.ID)
		}
	}

	assertOrderedRepos(t, "CreatePlan()", created.Repositories)

	got, err := GetPlan(ctx, pool, created.ID)
	if err != nil {
		t.Fatalf("GetPlan() error = %v", err)
	}
	assertOrderedRepos(t, "GetPlan()", got.Repositories)

	listed, err := ListPlans(ctx, pool)
	if err != nil {
		t.Fatalf("ListPlans() error = %v", err)
	}
	var foundInList *Plan
	for i := range listed {
		if listed[i].ID == created.ID {
			foundInList = &listed[i]
		}
	}
	if foundInList == nil {
		t.Fatalf("ListPlans() did not include plan %d", created.ID)
	}
	assertOrderedRepos(t, "ListPlans()", foundInList.Repositories)

	spec, err := CreateSpec(ctx, pool, primary.ID, "spec for plan workflow link", "content", []byte(`[]`))
	if err != nil {
		t.Fatalf("CreateSpec() error = %v", err)
	}
	t.Cleanup(func() { _ = DeleteSpec(context.Background(), pool, spec.ID) })

	wf, err := CreateWorkflow(ctx, pool, spec.ID, "poc", nil)
	if err != nil {
		t.Fatalf("CreateWorkflow() error = %v", err)
	}
	t.Cleanup(func() { _ = DeleteWorkflow(context.Background(), pool, wf.ID) })

	if err := LinkPlanWorkflow(ctx, pool, created.ID, wf.ID); err != nil {
		t.Fatalf("LinkPlanWorkflow() error = %v", err)
	}

	byWorkflow, err := GetPlanByWorkflow(ctx, pool, wf.ID)
	if err != nil {
		t.Fatalf("GetPlanByWorkflow() error = %v", err)
	}
	if byWorkflow.ID != created.ID {
		t.Fatalf("GetPlanByWorkflow().ID = %d, want %d", byWorkflow.ID, created.ID)
	}
	assertOrderedRepos(t, "GetPlanByWorkflow()", byWorkflow.Repositories)

	newTitle := "plan-multi-repo-roundtrip-renamed"
	updated, err := UpdatePlan(ctx, pool, created.ID, UpdatePlanParams{Title: &newTitle})
	if err != nil {
		t.Fatalf("UpdatePlan() error = %v", err)
	}
	if updated.Title != newTitle {
		t.Fatalf("UpdatePlan().Title = %q, want %q", updated.Title, newTitle)
	}
	assertOrderedRepos(t, "UpdatePlan()", updated.Repositories)
}
