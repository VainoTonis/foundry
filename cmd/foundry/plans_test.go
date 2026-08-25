package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tonis2/foundry/internal/apiclient"
)

func TestCreatePlanUsesZeroBasedPositionsAndParallelGroups(t *testing.T) {
	var positions []int
	var groups []*int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/plans":
			var body createPlanRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.RepositoryIDs) != 2 {
				t.Fatalf("repository_ids = %v", body.RepositoryIDs)
			}
			fmt.Fprint(w, `{"id":7}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/plans/7/steps":
			var body struct {
				Position      int  `json:"position"`
				ParallelGroup *int `json:"parallel_group"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			positions = append(positions, body.Position)
			groups = append(groups, body.ParallelGroup)
			fmt.Fprintf(w, `{"id":%d,"plan_id":7,"position":%d}`, len(positions), body.Position)
		case r.Method == http.MethodGet && r.URL.Path == "/api/plans/7":
			fmt.Fprint(w, `{"id":7,"repositories":[{"position":0,"repository_id":2,"repository":{"id":2,"name":"repo"}}],"content":"body"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/plans/7/steps":
			fmt.Fprint(w, `[{"id":1,"plan_id":7,"position":0,"text":"first"},{"id":2,"plan_id":7,"position":1,"text":"second","parallel_group":3}]`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	oldURL := apiURL
	apiURL = server.URL
	defer func() { apiURL = oldURL }()
	var output bytes.Buffer
	createCmd.SetIn(strings.NewReader(`{"repository_ids":[2,3],"title":"title","content":"body","steps":["first",{"text":"second","parallel_group":3}]}`))
	createCmd.SetOut(&output)
	if err := createCmd.RunE(createCmd, nil); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(positions) != "[0 1]" || groups[0] != nil || groups[1] == nil || *groups[1] != 3 {
		t.Fatalf("positions/groups = %v/%v", positions, groups)
	}
	var result planOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Content != "body" || result.Repositories[0].Repository.Name != "repo" || len(result.Steps) != 2 {
		t.Fatalf("output = %+v", result)
	}
}

func TestPlanCommandValidationPrecedesHTTP(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	oldURL := apiURL
	apiURL = server.URL
	defer func() { apiURL = oldURL }()

	createCmd.SetIn(strings.NewReader(`{"repository_ids":[],"steps":[]}`))
	if err := createCmd.RunE(createCmd, nil); err == nil {
		t.Fatal("create accepted empty repository_ids")
	}
	updateCmd.SetIn(strings.NewReader(`{"repository_ids":[1.5]}`))
	if err := updateCmd.RunE(updateCmd, []string{"2"}); err == nil {
		t.Fatal("update accepted fractional repository ID")
	}
	updateStepCmd.SetIn(strings.NewReader(`{"plan_id":2,"position":-1,"text":"x"}`))
	if err := updateStepCmd.RunE(updateStepCmd, nil); err == nil {
		t.Fatal("update-step accepted negative position")
	}
	if requests != 0 {
		t.Fatalf("made %d HTTP requests for invalid inputs", requests)
	}
}

func TestGetUpdateAndUpdateStepRequests(t *testing.T) {
	var patchedPlan, patchedStep bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/plans/8":
			fmt.Fprint(w, `{"id":8,"content":"current"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/plans/8/steps":
			fmt.Fprint(w, `[{"id":41,"plan_id":8,"position":0},{"id":42,"plan_id":8,"position":1}]`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/plans/8":
			patchedPlan = true
			fmt.Fprint(w, `{"id":8,"title":"updated"}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/plans/8/steps/42":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["parallel_group"] != float64(4) {
				t.Fatalf("step body = %v", body)
			}
			patchedStep = true
			fmt.Fprint(w, `{"id":42,"plan_id":8,"position":1,"parallel_group":4}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	oldURL := apiURL
	apiURL = server.URL
	defer func() { apiURL = oldURL }()
	getCmd.SetOut(&bytes.Buffer{})
	if err := getCmd.RunE(getCmd, []string{"8"}); err != nil {
		t.Fatal(err)
	}
	updateCmd.SetIn(strings.NewReader(`{"title":"updated"}`))
	updateCmd.SetOut(&bytes.Buffer{})
	if err := updateCmd.RunE(updateCmd, []string{"8"}); err != nil {
		t.Fatal(err)
	}
	updateStepCmd.SetIn(strings.NewReader(`{"plan_id":8,"position":1,"parallel_group":4}`))
	updateStepCmd.SetOut(&bytes.Buffer{})
	if err := updateStepCmd.RunE(updateStepCmd, nil); err != nil {
		t.Fatal(err)
	}
	if !patchedPlan || !patchedStep {
		t.Fatalf("patches: plan=%v step=%v", patchedPlan, patchedStep)
	}
}

func TestPlanCommandSurfacesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":"cannot update"}`)
	}))
	defer server.Close()
	oldURL := apiURL
	apiURL = server.URL
	defer func() { apiURL = oldURL }()
	updateCmd.SetIn(strings.NewReader(`{"title":"updated"}`))
	err := updateCmd.RunE(updateCmd, []string{"8"})
	if err == nil || !strings.Contains(err.Error(), "API error (status 409): cannot update") {
		t.Fatalf("error = %v", err)
	}
}

func TestReviewReviewsAndCheckCommands(t *testing.T) {
	var ranReview bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/plans/11/reviews":
			ranReview = true
			fmt.Fprint(w, `{"id":5,"plan_id":11,"status":"completed","verdict":"pass","stale":false}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/plans/11/reviews":
			fmt.Fprint(w, `[{"id":5,"plan_id":11,"status":"completed","verdict":"pass","stale":true},{"id":4,"plan_id":11,"status":"failed","error":"boom"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/plans/12/reviews":
			fmt.Fprint(w, `[]`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	oldURL := apiURL
	apiURL = server.URL
	defer func() { apiURL = oldURL }()

	var reviewOut bytes.Buffer
	reviewCmd.SetOut(&reviewOut)
	if err := reviewCmd.RunE(reviewCmd, []string{"11"}); err != nil {
		t.Fatal(err)
	}
	if !ranReview {
		t.Fatal("review command did not POST /api/plans/11/reviews")
	}
	var created apiclient.PlanReview
	if err := json.Unmarshal(reviewOut.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != 5 || created.Verdict == nil || *created.Verdict != "pass" {
		t.Fatalf("review output = %+v", created)
	}

	var reviewsOut bytes.Buffer
	reviewsCmd.SetOut(&reviewsOut)
	if err := reviewsCmd.RunE(reviewsCmd, []string{"11"}); err != nil {
		t.Fatal(err)
	}
	var reviews []apiclient.PlanReview
	if err := json.Unmarshal(reviewsOut.Bytes(), &reviews); err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 2 || reviews[0].ID != 5 || reviews[1].Status != "failed" {
		t.Fatalf("reviews output = %+v", reviews)
	}

	var checkOut bytes.Buffer
	checkCmd.SetOut(&checkOut)
	if err := checkCmd.RunE(checkCmd, []string{"11"}); err != nil {
		t.Fatal(err)
	}
	var check planReviewCheck
	if err := json.Unmarshal(checkOut.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	if check.PlanID != 11 || check.Status != "completed" || check.Review == nil || check.Review.ID != 5 {
		t.Fatalf("check output = %+v", check)
	}

	var noReviewOut bytes.Buffer
	checkCmd.SetOut(&noReviewOut)
	if err := checkCmd.RunE(checkCmd, []string{"12"}); err != nil {
		t.Fatal(err)
	}
	var noReview planReviewCheck
	if err := json.Unmarshal(noReviewOut.Bytes(), &noReview); err != nil {
		t.Fatal(err)
	}
	if noReview.PlanID != 12 || noReview.Status != "no_review" || noReview.Review != nil {
		t.Fatalf("no-review check output = %+v", noReview)
	}
}
