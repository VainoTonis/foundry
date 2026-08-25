package apiclient

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientDecodesPlanRepositoriesAndContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9,"repositories":[{"position":0,"repository_id":4,"repository":{"id":4,"name":"primary","local_path":"/work/repo","remote_url":"https://example/repo.git","created_at":"2024-01-02T03:04:05Z"}}],"title":"plan","summary":"summary","content":"line one\n{\"nested\":true}","status":"pending","created_at":"2024-01-02T03:04:05Z","updated_at":"2024-01-02T03:04:05Z"}`))
	}))
	defer server.Close()

	var plan Plan
	if err := NewClient(server.URL).Get("/api/plans/9", &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Repositories) != 1 || plan.Repositories[0].Position != 0 || plan.Repositories[0].Repository.Name != "primary" {
		t.Fatalf("repositories not decoded: %+v", plan.Repositories)
	}
	if plan.Content != "line one\n{\"nested\":true}" {
		t.Fatalf("content = %q", plan.Content)
	}
}

func TestClientReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"repository not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	err := NewClient(server.URL).Get("/api/plans/9", &Plan{})
	if err == nil || !strings.Contains(err.Error(), "API error (status 404): repository not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestClientDecodesPlanReviewReportAndStaleness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":3,"plan_id":9,"input_snapshot_sha256":"abc","contract_version":"v1","contract_sha256":"def","model":"m","session":"s","status":"completed","verdict":"revise","report":{"verdict":"revise","pass1":"x","pass2":"y","evidence":"z","uncertainties":"w","unavailable_repositories":[]},"stale":true,"created_at":"2024-01-02T03:04:05Z"}`))
	}))
	defer server.Close()

	var rev PlanReview
	if err := NewClient(server.URL).Get("/api/plans/9/reviews/3", &rev); err != nil {
		t.Fatal(err)
	}
	if rev.ID != 3 || rev.Verdict == nil || *rev.Verdict != "revise" || !rev.Stale {
		t.Fatalf("review = %+v", rev)
	}
	if !strings.Contains(string(rev.Report), `"uncertainties":"w"`) {
		t.Fatalf("report not decoded verbatim: %s", rev.Report)
	}
}
