package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tonis2/foundry/internal/apiclient"
)

func TestSessionsAttachSendsSessionAndOptionalStepID(t *testing.T) {
	var captured attachSessionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/api/plans/9/sessions" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(w, `{"id":1,"agent_session_id":2,"plan_id":9,"plan_step_id":3,"method":"explicit"}`)
	}))
	defer server.Close()
	oldURL := apiURL
	apiURL = server.URL
	defer func() { apiURL = oldURL }()

	oldPlanID, oldStepID := attachSessionPlanID, attachSessionStepID
	attachSessionPlanID, attachSessionStepID = 9, 3
	defer func() { attachSessionPlanID, attachSessionStepID = oldPlanID, oldStepID }()

	var output bytes.Buffer
	attachCmd.SetOut(&output)
	if err := attachCmd.RunE(attachCmd, []string{"my-session"}); err != nil {
		t.Fatal(err)
	}
	if captured.Session != "my-session" || captured.PlanStepID == nil || *captured.PlanStepID != 3 {
		t.Fatalf("captured request = %+v", captured)
	}
	var link apiclient.SessionPlanLink
	if err := json.Unmarshal(output.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	if link.PlanID != 9 || link.Method != "explicit" || link.PlanStepID == nil || *link.PlanStepID != 3 {
		t.Fatalf("output = %+v", link)
	}
}

func TestSessionsAttachRequiresPlanID(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	oldURL := apiURL
	apiURL = server.URL
	defer func() { apiURL = oldURL }()

	oldPlanID, oldStepID := attachSessionPlanID, attachSessionStepID
	attachSessionPlanID, attachSessionStepID = 0, 0
	defer func() { attachSessionPlanID, attachSessionStepID = oldPlanID, oldStepID }()

	if err := attachCmd.RunE(attachCmd, []string{"my-session"}); err == nil {
		t.Fatal("expected error for missing --plan-id")
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0 (validation must precede HTTP)", requests)
	}
}
