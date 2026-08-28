package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tonis2/foundry/internal/db"
)

// createTestAgentSession ensures an agent_sessions row named session
// exists, for tests that attach an externally-launched session to a
// plan without going through any Foundry-owned session-start event.
func createTestAgentSession(t *testing.T, h *Handler, session string) int64 {
	t.Helper()
	s, err := db.EnsureAgentSession(t.Context(), h.pool, db.EnsureAgentSessionParams{
		Session:         session,
		SourceSessionID: session,
		Origin:          "external",
		Kind:            "unknown",
	})
	if err != nil {
		t.Fatalf("EnsureAgentSession: %v", err)
	}
	return s.ID
}

// TestAttachSessionToPlan covers POST /api/plans/{id}/sessions: a plan-
// level attach, a plan+step-level attach, 404 for an unknown session,
// and a clear 4xx (not 500) for a plan_step_id that does not belong to
// the given plan_id.
func TestAttachSessionToPlan(t *testing.T) {
	h := newPlansHandler(t)

	repoID := createTestPlanRepository(t, h, "attach-session-repo", "https://github.com/foo/attach-session.git")
	planID := createTestPlan(t, h, []int64{repoID}, "attach-session-plan")
	otherPlanID := createTestPlan(t, h, []int64{repoID}, "attach-session-other-plan")

	stepReq := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(planID)+"/steps", strings.NewReader(`{"position":0,"text":"do the thing"}`))
	stepRec := httptest.NewRecorder()
	h.HandlePlan(stepRec, stepReq)
	if stepRec.Code != http.StatusCreated {
		t.Fatalf("create step status = %d body = %s", stepRec.Code, stepRec.Body.String())
	}
	var step struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(stepRec.Body.Bytes(), &step); err != nil {
		t.Fatalf("unmarshal step: %v", err)
	}

	otherStepReq := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(otherPlanID)+"/steps", strings.NewReader(`{"position":0,"text":"belongs elsewhere"}`))
	otherStepRec := httptest.NewRecorder()
	h.HandlePlan(otherStepRec, otherStepReq)
	if otherStepRec.Code != http.StatusCreated {
		t.Fatalf("create other-plan step status = %d body = %s", otherStepRec.Code, otherStepRec.Body.String())
	}
	var otherStep struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(otherStepRec.Body.Bytes(), &otherStep); err != nil {
		t.Fatalf("unmarshal other-plan step: %v", err)
	}

	planSession := "attach-session-plan-level"
	createTestAgentSession(t, h, planSession)

	// Plan-level attach.
	req := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(planID)+"/sessions", strings.NewReader(`{"session":"`+planSession+`"}`))
	rec := httptest.NewRecorder()
	h.HandlePlan(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("plan-level attach status = %d body = %s", rec.Code, rec.Body.String())
	}
	var link db.SessionPlanLink
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil {
		t.Fatalf("unmarshal plan-level link: %v", err)
	}
	if link.PlanID != planID || link.Method != db.SessionPlanLinkMethodExplicit || link.PlanStepID != nil {
		t.Fatalf("unexpected plan-level link: %+v", link)
	}

	// Plan+step-level attach.
	stepSession := "attach-session-step-level"
	createTestAgentSession(t, h, stepSession)
	stepAttachBody, err := json.Marshal(map[string]any{"session": stepSession, "plan_step_id": step.ID})
	if err != nil {
		t.Fatalf("marshal step attach body: %v", err)
	}
	stepAttachReq := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(planID)+"/sessions", strings.NewReader(string(stepAttachBody)))
	stepAttachRec := httptest.NewRecorder()
	h.HandlePlan(stepAttachRec, stepAttachReq)
	if stepAttachRec.Code != http.StatusCreated {
		t.Fatalf("plan+step attach status = %d body = %s", stepAttachRec.Code, stepAttachRec.Body.String())
	}
	var stepLink db.SessionPlanLink
	if err := json.Unmarshal(stepAttachRec.Body.Bytes(), &stepLink); err != nil {
		t.Fatalf("unmarshal plan+step link: %v", err)
	}
	if stepLink.PlanID != planID || stepLink.PlanStepID == nil || *stepLink.PlanStepID != step.ID {
		t.Fatalf("unexpected plan+step link: %+v", stepLink)
	}

	// Unknown session must 404, not 500.
	unknownReq := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(planID)+"/sessions", strings.NewReader(`{"session":"does-not-exist"}`))
	unknownRec := httptest.NewRecorder()
	h.HandlePlan(unknownRec, unknownReq)
	if unknownRec.Code != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want %d, body = %s", unknownRec.Code, http.StatusNotFound, unknownRec.Body.String())
	}

	// A plan_step_id belonging to a different plan must be rejected with
	// a clear 4xx, not a raw SQL/foreign-key error surfaced as a 500.
	mismatchSession := "attach-session-mismatched-step"
	createTestAgentSession(t, h, mismatchSession)
	mismatchBody, err := json.Marshal(map[string]any{"session": mismatchSession, "plan_step_id": otherStep.ID})
	if err != nil {
		t.Fatalf("marshal mismatch body: %v", err)
	}
	mismatchReq := httptest.NewRequest(http.MethodPost, "/api/plans/"+itoa(planID)+"/sessions", strings.NewReader(string(mismatchBody)))
	mismatchRec := httptest.NewRecorder()
	h.HandlePlan(mismatchRec, mismatchReq)
	if mismatchRec.Code < 400 || mismatchRec.Code >= 500 {
		t.Fatalf("mismatched plan_step_id status = %d, want 4xx, body = %s", mismatchRec.Code, mismatchRec.Body.String())
	}
}
