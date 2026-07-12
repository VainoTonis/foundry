package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/tonis2/foundry/internal/db"
)

// JSON response helpers
func jsonOK(w http.ResponseWriter, v any, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Profile file path helpers
func profileFilePath(session string) string {
	return "/tmp/foundry-profile-" + session + ".json"
}

// removeProfileFile deletes the profile file for a session if it exists.
func removeProfileFile(session string) {
	os.Remove(profileFilePath(session))
}

func (s *Server) workflowProject(ctx context.Context, workflowID int64) (db.Workflow, db.Spec, db.Project, error) {
	wf, err := db.GetWorkflow(ctx, s.pool, workflowID)
	if err != nil {
		return wf, db.Spec{}, db.Project{}, err
	}
	sp, err := db.GetSpec(ctx, s.pool, wf.SpecID)
	if err != nil {
		return wf, sp, db.Project{}, err
	}
	proj, err := db.GetProject(ctx, s.pool, sp.ProjectID)
	return wf, sp, proj, err
}
