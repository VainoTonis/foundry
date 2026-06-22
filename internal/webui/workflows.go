package webui

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/tonis2/foundry/internal/db"
)

func (s *Handler) handleUIWorkflow(w http.ResponseWriter, r *http.Request) {
	id, suffix, ok := parseUIIDSuffix(r.URL.Path, "/workflows/")
	if !ok || (suffix != "" && suffix != "fragment") {
		http.NotFound(w, r)
		return
	}
	if suffix == "fragment" {
		s.handleUIWorkflowFragment(w, r, id)
		return
	}
	s.renderShell(w, "backlog", fmt.Sprintf("/workflows/%d/fragment", id))
}

func (s *Handler) handleUIWorkflowFragment(w http.ResponseWriter, r *http.Request, id int64) {
	wf, err := db.GetWorkflow(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sp, _ := db.GetSpec(r.Context(), s.pool, wf.SpecID)
	proj, _ := db.GetProject(r.Context(), s.pool, sp.ProjectID)
	phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	initialPhase, hasInitialPhase := selectInitialPhase(phases)
	currentPhaseName := "no phase"
	if hasInitialPhase {
		currentPhaseName = initialPhase.Name
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "workflows.detail", struct {
		Workflow         db.Workflow
		Spec             db.Spec
		Project          db.Project
		Phases           []db.Phase
		InitialPhase     db.Phase
		HasInitialPhase  bool
		CurrentPhaseName string
	}{wf, sp, proj, phases, initialPhase, hasInitialPhase, currentPhaseName}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func selectInitialPhase(phases []db.Phase) (db.Phase, bool) {
	if len(phases) == 0 {
		return db.Phase{}, false
	}
	for _, ph := range phases {
		if ph.Status == "running" {
			return ph, true
		}
	}
	for _, ph := range phases {
		if ph.Status == "awaiting_review" {
			return ph, true
		}
	}
	for _, ph := range phases {
		if ph.Status == "failed" {
			return ph, true
		}
	}
	for i := len(phases) - 1; i >= 0; i-- {
		if phases[i].Status == "done" {
			return phases[i], true
		}
	}
	return phases[0], true
}
