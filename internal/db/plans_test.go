package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tonis2/foundry/internal/repository"
)

// TestCreatePlan_EmptyProjectIDs confirms an empty project id list is
// rejected and leaves no plan row behind.
func TestCreatePlan_EmptyProjectIDs(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	before := countPlans(t, pool)

	_, err := CreatePlan(ctx, pool, nil, "empty-list", "summary", "content")
	if err == nil {
		t.Fatal("CreatePlan() error = nil, want error for empty project id list")
	}

	after := countPlans(t, pool)
	if after != before {
		t.Fatalf("CreatePlan() with empty list left %d plan row(s) behind, want %d", after, before)
	}
}

// TestCreatePlan_DuplicateProjectID confirms a duplicate project id in the
// list is rejected (per the plan_repositories UNIQUE(plan_id, project_id)
// constraint) and leaves no plan or plan_repositories rows behind.
func TestCreatePlan_DuplicateProjectID(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	repo := createTestRepository(t, pool, repository.Repository{
		Name:      "plan-dup-project",
		RemoteURL: strPtr("https://github.com/foo/plan-dup-project.git"),
	})

	beforePlans := countPlans(t, pool)
	beforeMembers := countPlanRepositories(t, pool)

	_, err := CreatePlan(ctx, pool, []int64{repo.ID, repo.ID}, "dup", "summary", "content")
	if err == nil {
		t.Fatal("CreatePlan() error = nil, want error for duplicate project id")
	}

	if got := countPlans(t, pool); got != beforePlans {
		t.Fatalf("CreatePlan() with duplicate project id left %d plan row(s) behind, want %d", got, beforePlans)
	}
	if got := countPlanRepositories(t, pool); got != beforeMembers {
		t.Fatalf("CreatePlan() with duplicate project id left %d plan_repositories row(s) behind, want %d", got, beforeMembers)
	}
}

// TestCreatePlan_UnknownProjectID confirms an unknown project id is
// rejected (foreign key violation) and the whole transaction is rolled
// back, leaving no plan or plan_repositories rows behind -- including for
// any valid project ids listed alongside the unknown one.
func TestCreatePlan_UnknownProjectID(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	repo := createTestRepository(t, pool, repository.Repository{
		Name:      "plan-unknown-project-valid",
		RemoteURL: strPtr("https://github.com/foo/plan-unknown-project-valid.git"),
	})

	const neverIssuedProjectID int64 = 1 << 40

	beforePlans := countPlans(t, pool)
	beforeMembers := countPlanRepositories(t, pool)

	_, err := CreatePlan(ctx, pool, []int64{repo.ID, neverIssuedProjectID}, "unknown", "summary", "content")
	if err == nil {
		t.Fatal("CreatePlan() error = nil, want error for unknown project id")
	}

	if got := countPlans(t, pool); got != beforePlans {
		t.Fatalf("CreatePlan() with unknown project id left %d plan row(s) behind, want %d", got, beforePlans)
	}
	if got := countPlanRepositories(t, pool); got != beforeMembers {
		t.Fatalf("CreatePlan() with unknown project id left %d plan_repositories row(s) behind, want %d", got, beforeMembers)
	}
}

// TestCreateAndGetPlan_MultipleRepositories confirms a plan created with
// 2+ repositories round-trips through CreatePlan/GetPlan/ListPlans with
// its ordering preserved and position 0 returned as the primary
// repository.
func TestCreateAndGetPlan_MultipleRepositories(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	primary := createTestRepository(t, pool, repository.Repository{
		Name:      "plan-multi-primary",
		RemoteURL: strPtr("https://github.com/foo/plan-multi-primary.git"),
	})
	secondary := createTestRepository(t, pool, repository.Repository{
		Name:      "plan-multi-secondary",
		RemoteURL: strPtr("https://github.com/foo/plan-multi-secondary.git"),
	})
	tertiary := createTestRepository(t, pool, repository.Repository{
		Name:      "plan-multi-tertiary",
		RemoteURL: strPtr("https://github.com/foo/plan-multi-tertiary.git"),
	})

	plan, err := CreatePlan(ctx, pool, []int64{primary.ID, secondary.ID, tertiary.ID}, "multi-repo plan", "summary", "content")
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM plans WHERE id = $1`, plan.ID); err != nil {
			t.Errorf("cleanup delete plan %d: %v", plan.ID, err)
		}
	})

	assertOrderedRepositories(t, "CreatePlan()", plan.Repositories, primary.ID, secondary.ID, tertiary.ID)

	got, err := GetPlan(ctx, pool, plan.ID)
	if err != nil {
		t.Fatalf("GetPlan() error = %v", err)
	}
	assertOrderedRepositories(t, "GetPlan()", got.Repositories, primary.ID, secondary.ID, tertiary.ID)

	list, err := ListPlans(ctx, pool)
	if err != nil {
		t.Fatalf("ListPlans() error = %v", err)
	}
	var found *Plan
	for i := range list {
		if list[i].ID == plan.ID {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatalf("ListPlans() did not include plan %d", plan.ID)
	}
	assertOrderedRepositories(t, "ListPlans()", found.Repositories, primary.ID, secondary.ID, tertiary.ID)
}

// assertOrderedRepositories checks that repos is exactly the given
// project ids, in order, with position 0 as the primary (first) entry.
func assertOrderedRepositories(t *testing.T, label string, repos []PlanRepository, wantOrder ...int64) {
	t.Helper()
	if len(repos) != len(wantOrder) {
		t.Fatalf("%s Repositories = %+v, want %d entries", label, repos, len(wantOrder))
	}
	for i, want := range wantOrder {
		if repos[i].Position != i {
			t.Fatalf("%s Repositories[%d].Position = %d, want %d", label, i, repos[i].Position, i)
		}
		if repos[i].ProjectID != want {
			t.Fatalf("%s Repositories[%d].ProjectID = %d, want %d", label, i, repos[i].ProjectID, want)
		}
		if repos[i].Repository.ID != want {
			t.Fatalf("%s Repositories[%d].Repository.ID = %d, want %d", label, i, repos[i].Repository.ID, want)
		}
	}
	if repos[0].Position != 0 {
		t.Fatalf("%s primary Repositories[0].Position = %d, want 0", label, repos[0].Position)
	}
}

// TestUpdatePlan_RepositoryIDs_Replaces confirms UpdatePlan with a new,
// valid ordered repository_ids list fully replaces plan_repositories
// membership and returns the new list with position 0 as the primary.
func TestUpdatePlan_RepositoryIDs_Replaces(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	oldRepo := createTestRepository(t, pool, repository.Repository{
		Name:      "update-plan-old",
		RemoteURL: strPtr("https://github.com/foo/update-plan-old.git"),
	})
	newPrimary := createTestRepository(t, pool, repository.Repository{
		Name:      "update-plan-new-primary",
		RemoteURL: strPtr("https://github.com/foo/update-plan-new-primary.git"),
	})
	newSecondary := createTestRepository(t, pool, repository.Repository{
		Name:      "update-plan-new-secondary",
		RemoteURL: strPtr("https://github.com/foo/update-plan-new-secondary.git"),
	})

	plan, err := CreatePlan(ctx, pool, []int64{oldRepo.ID}, "update-plan-replace", "summary", "content")
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM plans WHERE id = $1`, plan.ID); err != nil {
			t.Errorf("cleanup delete plan %d: %v", plan.ID, err)
		}
	})

	newIDs := []int64{newPrimary.ID, newSecondary.ID}
	updated, err := UpdatePlan(ctx, pool, plan.ID, UpdatePlanParams{RepositoryIDs: &newIDs})
	if err != nil {
		t.Fatalf("UpdatePlan() error = %v", err)
	}
	assertOrderedRepositories(t, "UpdatePlan()", updated.Repositories, newPrimary.ID, newSecondary.ID)

	got, err := GetPlan(ctx, pool, plan.ID)
	if err != nil {
		t.Fatalf("GetPlan() error = %v", err)
	}
	assertOrderedRepositories(t, "GetPlan() after UpdatePlan()", got.Repositories, newPrimary.ID, newSecondary.ID)
}

// TestUpdatePlan_RepositoryIDs_Empty confirms UpdatePlan rejects an empty
// repository_ids slice and leaves existing membership untouched.
func TestUpdatePlan_RepositoryIDs_Empty(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	repo := createTestRepository(t, pool, repository.Repository{
		Name:      "update-plan-empty",
		RemoteURL: strPtr("https://github.com/foo/update-plan-empty.git"),
	})

	plan, err := CreatePlan(ctx, pool, []int64{repo.ID}, "update-plan-empty-ids", "summary", "content")
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM plans WHERE id = $1`, plan.ID); err != nil {
			t.Errorf("cleanup delete plan %d: %v", plan.ID, err)
		}
	})

	empty := []int64{}
	if _, err := UpdatePlan(ctx, pool, plan.ID, UpdatePlanParams{RepositoryIDs: &empty}); err == nil {
		t.Fatal("UpdatePlan() error = nil, want error for empty repository_ids")
	}

	got, err := GetPlan(ctx, pool, plan.ID)
	if err != nil {
		t.Fatalf("GetPlan() error = %v", err)
	}
	assertOrderedRepositories(t, "GetPlan() after failed UpdatePlan()", got.Repositories, repo.ID)
}

// TestUpdatePlan_RepositoryIDs_Unknown confirms UpdatePlan rejects an
// unknown repository id and leaves existing membership fully unchanged
// (no partial replace).
func TestUpdatePlan_RepositoryIDs_Unknown(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	repo := createTestRepository(t, pool, repository.Repository{
		Name:      "update-plan-unknown",
		RemoteURL: strPtr("https://github.com/foo/update-plan-unknown.git"),
	})
	validReplacement := createTestRepository(t, pool, repository.Repository{
		Name:      "update-plan-unknown-valid-replacement",
		RemoteURL: strPtr("https://github.com/foo/update-plan-unknown-valid-replacement.git"),
	})

	plan, err := CreatePlan(ctx, pool, []int64{repo.ID}, "update-plan-unknown-id", "summary", "content")
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM plans WHERE id = $1`, plan.ID); err != nil {
			t.Errorf("cleanup delete plan %d: %v", plan.ID, err)
		}
	})

	const neverIssuedProjectID int64 = 1 << 40
	badIDs := []int64{validReplacement.ID, neverIssuedProjectID}
	if _, err := UpdatePlan(ctx, pool, plan.ID, UpdatePlanParams{RepositoryIDs: &badIDs}); err == nil {
		t.Fatal("UpdatePlan() error = nil, want error for unknown repository id")
	}

	got, err := GetPlan(ctx, pool, plan.ID)
	if err != nil {
		t.Fatalf("GetPlan() error = %v", err)
	}
	assertOrderedRepositories(t, "GetPlan() after failed UpdatePlan()", got.Repositories, repo.ID)
}

func countPlans(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM plans`).Scan(&n); err != nil {
		t.Fatalf("count plans: %v", err)
	}
	return n
}

func countPlanRepositories(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM plan_repositories`).Scan(&n); err != nil {
		t.Fatalf("count plan_repositories: %v", err)
	}
	return n
}
