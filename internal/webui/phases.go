package webui

import (
	"errors"
	"net/http"

	"github.com/tonis2/foundry/internal/db"
)

func (s *Handler) handleUIPhase(w http.ResponseWriter, r *http.Request) {
	id, suffix, ok := parseUIIDSuffix(r.URL.Path, "/phases/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch suffix {
	case "logs/fragment":
		s.handleUIPhaseLogsFragment(w, r, id)
	case "diff/fragment":
		s.handleUIPhaseDiffFragment(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (s *Handler) handleUIPhaseLogsFragment(w http.ResponseWriter, r *http.Request, id int64) {
	ph, err := db.GetPhase(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logs, _ := db.ListRecentPhaseLogs(r.Context(), s.pool, id, 300)
	var lastLogID int64
	if len(logs) > 0 {
		lastLogID = logs[len(logs)-1].ID
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "phases.logs", struct {
		Phase     db.Phase
		Logs      []db.PhaseLog
		LastLogID int64
	}{ph, logs, lastLogID}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Handler) handleUIPhaseDiffFragment(w http.ResponseWriter, r *http.Request, id int64) {
	ph, err := db.GetPhase(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var diff, msg string
	if ph.CerberusSession == nil {
		msg = "No Cerberus session for this phase yet."
	} else {
		// Get repo path: phase -> workflow -> spec -> project
		wf, err := db.GetWorkflow(r.Context(), s.pool, ph.WorkflowID)
		if err != nil {
			msg = "Failed to get workflow: " + err.Error()
		} else {
			spec, err := db.GetSpec(r.Context(), s.pool, wf.SpecID)
			if err != nil {
				msg = "Failed to get spec: " + err.Error()
			} else {
				proj, err := db.GetProject(r.Context(), s.pool, spec.ProjectID)
				if err != nil {
					msg = "Failed to get project: " + err.Error()
				} else if d, err := s.cerb.WithRepo(proj.RepoPath).Diff(r.Context(), *ph.CerberusSession); err != nil {
					msg = err.Error()
				} else {
					diff = d
				}
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "phases.diff", struct {
		Phase       db.Phase
		Diff, Error string
	}{ph, diff, msg}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
