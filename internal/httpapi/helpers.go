package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

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

func pathID(path, prefix string) (int64, error) {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, "/")
	return strconv.ParseInt(s, 10, 64)
}

func indentBlock(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}

func truncateString(s string, max int) string {
	const marker = "\n... truncated ..."
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= len(marker) {
		return s[:max]
	}
	return s[:max-len(marker)] + marker
}

func (h *Handler) workflowProject(ctx context.Context, workflowID int64) (db.Workflow, db.Spec, db.Project, error) {
	wf, err := db.GetWorkflow(ctx, h.pool, workflowID)
	if err != nil {
		return wf, db.Spec{}, db.Project{}, err
	}
	sp, err := db.GetSpec(ctx, h.pool, wf.SpecID)
	if err != nil {
		return wf, sp, db.Project{}, err
	}
	proj, err := db.GetProject(ctx, h.pool, sp.ProjectID)
	return wf, sp, proj, err
}
