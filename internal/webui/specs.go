package webui

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/tonis2/foundry/internal/db"
)

func (s *Handler) handleUISpec(w http.ResponseWriter, r *http.Request) {
	id, frag, ok := parseUIID(r.URL.Path, "/specs/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if frag {
		s.handleUISpecFragment(w, r, id)
		return
	}
	s.renderShell(w, "backlog", fmt.Sprintf("/specs/%d/fragment", id))
}

func (s *Handler) handleUISpecFragment(w http.ResponseWriter, r *http.Request, id int64) {
	sp, err := db.GetSpec(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	wfs, _ := db.ListWorkflowsBySpec(r.Context(), s.pool, id)
	proj, _ := db.GetProject(r.Context(), s.pool, sp.ProjectID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "specDetail", struct {
		Spec      db.Spec
		Project   db.Project
		Workflows []db.Workflow
	}{sp, proj, wfs}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
