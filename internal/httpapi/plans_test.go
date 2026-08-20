package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
			Position  int   `json:"position"`
			ProjectID int64 `json:"project_id"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(patchRec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}
	if len(updated.Repositories) != 1 || updated.Repositories[0].ProjectID != newRepo || updated.Repositories[0].Position != 0 {
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
