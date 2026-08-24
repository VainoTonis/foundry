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
