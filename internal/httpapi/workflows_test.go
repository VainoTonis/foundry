package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

// TestHandleWorkflowsRejectsRemoteOnlyRepository covers the conflict path
// required before any workflow side effect: a spec whose repository has
// no local path configured (remote-only) must not have a workflow started
// against it, since workflow execution runs Cerberus against a local
// worktree. The handler must report this as a conflict rather than
// creating a workflow row and asynchronously failing deep inside the
// runner.
func TestHandleWorkflowsRejectsRemoteOnlyRepository(t *testing.T) {
	pool := testPool(t)
	h := New(pool, Config{})

	repo, err := db.CreateRepository(t.Context(), pool, repository.Repository{
		Name:      "remote-only",
		RemoteURL: strp("https://github.com/foo/bar.git"),
	})
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}

	sp, err := db.CreateSpec(t.Context(), pool, repo.ID, "spec title", "## Phase 1\ngoal\n", []byte("[]"))
	if err != nil {
		t.Fatalf("create spec: %v", err)
	}

	body, _ := json.Marshal(map[string]any{"spec_id": sp.ID})
	req := httptest.NewRequest(http.MethodPost, "/api/workflows", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleWorkflows(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	// No workflow should have been created for the spec.
	wfs, err := db.ListWorkflowsBySpec(t.Context(), pool, sp.ID)
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	if len(wfs) != 0 {
		t.Fatalf("workflows created for remote-only repository spec: %+v", wfs)
	}
}

// TestHandleWorkflowsAllowsLocalRepository is the accompanying positive
// case: a repository with a local path configured must still be usable to
// start a workflow, preserving local execution.
func TestHandleWorkflowsAllowsLocalRepository(t *testing.T) {
	requireGit(t)
	pool := testPool(t)
	h := New(pool, Config{})

	dir := t.TempDir()
	initRepo(t, dir)

	repo, err := db.CreateRepository(t.Context(), pool, repository.Repository{
		Name:      "local",
		LocalPath: strp(dir),
	})
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}

	sp, err := db.CreateSpec(t.Context(), pool, repo.ID, "spec title", "## Phase 1\ngoal\n", []byte("[]"))
	if err != nil {
		t.Fatalf("create spec: %v", err)
	}

	body, _ := json.Marshal(map[string]any{"spec_id": sp.ID})
	req := httptest.NewRequest(http.MethodPost, "/api/workflows", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleWorkflows(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func strp(s string) *string { return &s }
