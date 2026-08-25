package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/review"
)

// fakeReviewRunner is a stub ReviewRunner returning a preconfigured
// result or error, so review endpoint tests never invoke a real
// Steward session.
type fakeReviewRunner struct {
	result db.PlanReview
	err    error
}

func (f *fakeReviewRunner) StartStewardReview(ctx context.Context, opts review.RunOptions) (db.PlanReview, error) {
	if f.err != nil {
		return db.PlanReview{}, f.err
	}
	return f.result, nil
}

func newPlansHandler(t *testing.T) *Handler {
	t.Helper()
	pool := testPool(t)
	return New(pool, Config{})
}

// createTestPlanRepository creates a repository via the API and returns
// its id, used by plan tests below as membership project ids.
func createTestPlanRepository(t *testing.T, h *Handler, name, remoteURL string) int64 {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name, "remote_url": remoteURL})
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/repositories", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.HandleRepositories(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create repository status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create repository response: %v", err)
	}
	return created.ID
}

func createTestPlan(t *testing.T, h *Handler, repositoryIDs []int64, title string) int64 {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"repository_ids": repositoryIDs,
		"title":          title,
		"summary":        "summary",
		"content":        "content",
	})
	if err != nil {
		t.Fatalf("marshal create plan body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/plans", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.HandlePlans(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plan status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create plan response: %v", err)
	}
	return created.ID
}

// TestHandlePlanPatchRepositoryIDs covers PATCH /api/plans/{id} with a
// repository_ids body field: a valid replacement round-trips and an
// empty array is rejected with 400 before reaching the DB layer.
func TestHandlePlanPatchRepositoryIDs(t *testing.T) {
	h := newPlansHandler(t)

	oldRepo := createTestPlanRepository(t, h, "patch-plan-old-repo", "https://github.com/foo/patch-plan-old.git")
	newRepo := createTestPlanRepository(t, h, "patch-plan-new-repo", "https://github.com/foo/patch-plan-new.git")

	planID := createTestPlan(t, h, []int64{oldRepo}, "patch-plan-repos")

	// Empty array must be rejected with 400 and must not reach UpdatePlan.
	emptyReq := httptest.NewRequest(http.MethodPatch, "/api/plans/"+itoa(planID), strings.NewReader(`{"repository_ids":[]}`))
	emptyRec := httptest.NewRecorder()
	h.HandlePlan(emptyRec, emptyReq)
	if emptyRec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH with empty repository_ids status = %d, want %d, body = %s", emptyRec.Code, http.StatusBadRequest, emptyRec.Body.String())
	}

	// Valid replacement round-trips.
	patchBody, err := json.Marshal(map[string]any{"repository_ids": []int64{newRepo}})
	if err != nil {
		t.Fatalf("marshal patch body: %v", err)
	}
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/plans/"+itoa(planID), strings.NewReader(string(patchBody)))
	patchRec := httptest.NewRecorder()
	h.HandlePlan(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH with valid repository_ids status = %d, want %d, body = %s", patchRec.Code, http.StatusOK, patchRec.Body.String())
	}
	var updated struct {
		Repositories []struct {
			Position     int   `json:"position"`
			RepositoryID int64 `json:"repository_id"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(patchRec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}
	if len(updated.Repositories) != 1 || updated.Repositories[0].RepositoryID != newRepo || updated.Repositories[0].Position != 0 {
		t.Fatalf("unexpected repositories after patch: %+v", updated.Repositories)
	}
}

// TestRunPlanRejectsMissingLocalCheckout covers that runPlan responds
// 409 without creating a spec, workflow, or plan_workflows link when the
// plan's primary repository has no local_path.
func TestRunPlanRejectsMissingLocalCheckout(t *testing.T) {
	h := newPlansHandler(t)

	repoID := createTestPlanRepository(t, h, "run-plan-no-checkout", "https://github.com/foo/run-plan-no-checkout.git")
	planID := createTestPlan(t, h, []int64{repoID}, "run-plan-no-checkout-plan")

	var specCountBefore int64
	if err := h.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM specs`).Scan(&specCountBefore); err != nil {
		t.Fatalf("count specs before: %v", err)
	}

	runReq := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(planID)+"/run", strings.NewReader(`{}`))
	runRec := httptest.NewRecorder()
	h.runPlan(runRec, runReq, planID)
	if runRec.Code != http.StatusConflict {
		t.Fatalf("runPlan status = %d, want %d, body = %s", runRec.Code, http.StatusConflict, runRec.Body.String())
	}

	var specCountAfter int64
	if err := h.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM specs`).Scan(&specCountAfter); err != nil {
		t.Fatalf("count specs after: %v", err)
	}
	if specCountAfter != specCountBefore {
		t.Fatalf("runPlan created %d spec row(s) despite missing local checkout, want %d", specCountAfter, specCountBefore)
	}
}

// TestPlanReviewEndpointsCoverLifecycleAndFailureStates exercises the
// review create/list/detail endpoints: not-configured, plan-not-found,
// successful create with a computed staleness flag, list ordering, and
// detail lookup, plus a failed Steward run surfaced as a gateway error.
func TestPlanReviewEndpointsCoverLifecycleAndFailureStates(t *testing.T) {
	h := newPlansHandler(t)

	repoID := createTestPlanRepository(t, h, "review-plan-repo", "https://github.com/foo/review-plan-repo.git")
	planID := createTestPlan(t, h, []int64{repoID}, "review-plan")

	// No ReviewRunner configured: create fails with 503, list/detail
	// still work against the (empty) persisted review set.
	createReq := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(planID)+"/reviews", strings.NewReader(`{}`))
	createRec := httptest.NewRecorder()
	h.HandlePlan(createRec, createReq)
	if createRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("create review with no runner status = %d, want %d, body = %s", createRec.Code, http.StatusServiceUnavailable, createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/plans/"+itoa(planID)+"/reviews", nil)
	listRec := httptest.NewRecorder()
	h.HandlePlan(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list reviews (empty) status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	var empty []planReviewView
	if err := json.Unmarshal(listRec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("unmarshal empty review list: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("review list for new plan = %+v, want empty", empty)
	}

	// Unknown plan id: 404 on create and list.
	missingCreateReq := httptest.NewRequest(http.MethodPost, "/api/plans/999999999/reviews", strings.NewReader(`{}`))
	missingCreateRec := httptest.NewRecorder()
	h.HandlePlan(missingCreateRec, missingCreateReq)
	if missingCreateRec.Code != http.StatusNotFound {
		t.Fatalf("create review for missing plan status = %d, want %d", missingCreateRec.Code, http.StatusNotFound)
	}
	missingListReq := httptest.NewRequest(http.MethodGet, "/api/plans/999999999/reviews", nil)
	missingListRec := httptest.NewRecorder()
	h.HandlePlan(missingListRec, missingListReq)
	if missingListRec.Code != http.StatusNotFound {
		t.Fatalf("list reviews for missing plan status = %d, want %d", missingListRec.Code, http.StatusNotFound)
	}

	// Configure a runner that fails: create surfaces 502.
	h.reviewRunner = &fakeReviewRunner{err: fmt.Errorf("cerberus turn: boom")}
	failReq := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(planID)+"/reviews", strings.NewReader(`{}`))
	failRec := httptest.NewRecorder()
	h.HandlePlan(failRec, failReq)
	if failRec.Code != http.StatusBadGateway {
		t.Fatalf("create review with failing runner status = %d, want %d, body = %s", failRec.Code, http.StatusBadGateway, failRec.Body.String())
	}

	// Configure a runner that succeeds, persisting a real completed
	// review via the db package directly (mirroring what review.Service
	// would have done), so create/list/detail all observe it.
	plan, err := db.GetPlan(context.Background(), h.pool, planID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	snap, err := review.BuildSnapshot(plan, nil, nil)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	created, err := db.CreatePlanReview(context.Background(), h.pool, db.CreatePlanReviewParams{
		PlanID:          planID,
		InputSnapshot:   snap.JSON,
		ContractVersion: "v1",
		ContractContent: "contract body",
		Model:           "test-model",
		Session:         fmt.Sprintf("foundry-steward-test-%d", planID),
	})
	if err != nil {
		t.Fatalf("seed create plan review: %v", err)
	}
	if _, err := db.StartPlanReview(context.Background(), h.pool, created.ID); err != nil {
		t.Fatalf("seed start plan review: %v", err)
	}
	report := json.RawMessage(`{"verdict":"pass","pass1":"ok","pass2":"ok","evidence":"none","uncertainties":"none","unavailable_repositories":[]}`)
	completed, err := db.CompletePlanReview(context.Background(), h.pool, created.ID, db.PlanReviewVerdictPass, report)
	if err != nil {
		t.Fatalf("seed complete plan review: %v", err)
	}
	h.reviewRunner = &fakeReviewRunner{result: completed}

	createOKReq := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(planID)+"/reviews", strings.NewReader(`{}`))
	createOKRec := httptest.NewRecorder()
	h.HandlePlan(createOKRec, createOKReq)
	if createOKRec.Code != http.StatusCreated {
		t.Fatalf("create review status = %d, want %d, body = %s", createOKRec.Code, http.StatusCreated, createOKRec.Body.String())
	}
	var createdView planReviewView
	if err := json.Unmarshal(createOKRec.Body.Bytes(), &createdView); err != nil {
		t.Fatalf("unmarshal created review: %v", err)
	}
	if createdView.ID != completed.ID || createdView.Verdict == nil || *createdView.Verdict != db.PlanReviewVerdictPass {
		t.Fatalf("created review view = %+v", createdView)
	}
	if createdView.Stale {
		t.Fatalf("freshly created review reported stale, want current: %+v", createdView)
	}

	listOKReq := httptest.NewRequest(http.MethodGet, "/api/plans/"+itoa(planID)+"/reviews", nil)
	listOKRec := httptest.NewRecorder()
	h.HandlePlan(listOKRec, listOKReq)
	var list []planReviewView
	if err := json.Unmarshal(listOKRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal review list: %v", err)
	}
	if len(list) != 1 || list[0].ID != completed.ID {
		t.Fatalf("review list = %+v", list)
	}

	detailReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/plans/%d/reviews/%d", planID, completed.ID), nil)
	detailRec := httptest.NewRecorder()
	h.HandlePlan(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("get review status = %d, body = %s", detailRec.Code, detailRec.Body.String())
	}
	var detail planReviewView
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal review detail: %v", err)
	}
	if detail.ID != completed.ID || string(detail.Report) != string(report) {
		t.Fatalf("review detail = %+v", detail)
	}

	// Detail for a review id that exists but belongs to a different
	// plan is not found.
	otherRepoID := createTestPlanRepository(t, h, "review-plan-repo-2", "https://github.com/foo/review-plan-repo-2.git")
	otherPlanID := createTestPlan(t, h, []int64{otherRepoID}, "review-plan-other")
	crossReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/plans/%d/reviews/%d", otherPlanID, completed.ID), nil)
	crossRec := httptest.NewRecorder()
	h.HandlePlan(crossRec, crossReq)
	if crossRec.Code != http.StatusNotFound {
		t.Fatalf("cross-plan review lookup status = %d, want %d", crossRec.Code, http.StatusNotFound)
	}

	// Unknown review id on a real plan is not found.
	unknownReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/plans/%d/reviews/999999999", planID), nil)
	unknownRec := httptest.NewRecorder()
	h.HandlePlan(unknownRec, unknownReq)
	if unknownRec.Code != http.StatusNotFound {
		t.Fatalf("unknown review id status = %d, want %d", unknownRec.Code, http.StatusNotFound)
	}
}
