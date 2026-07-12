package webui

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/tonis2/foundry/internal/db"
)

func (s *Handler) redirectToPlans(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("HX-Redirect", "/plans")
	http.Redirect(w, r, "/plans", http.StatusSeeOther)
}

func (s *Handler) handleUIPlansPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/plans" {
		http.NotFound(w, r)
		return
	}
	s.renderShell(w, "plans", "/plans/fragment")
}

func (s *Handler) handleUIPlansFragment(w http.ResponseWriter, r *http.Request) {
	plans, err := db.ListPlans(r.Context(), s.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	projects, err := db.ListProjects(r.Context(), s.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := struct {
		Plans    []db.Plan
		Projects []db.Project
	}{plans, projects}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "plans.list", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Handler) handleUIPlan(w http.ResponseWriter, r *http.Request) {
	id, suffix, ok := parseUIIDSuffix(r.URL.Path, "/plans/")
	if !ok || (suffix != "" && suffix != "fragment") {
		http.NotFound(w, r)
		return
	}
	if suffix == "fragment" {
		s.handleUIPlanFragment(w, r, id)
		return
	}
	s.renderShell(w, "plans", fmt.Sprintf("/plans/%d/fragment", id))
}

func (s *Handler) handleUIPlanFragment(w http.ResponseWriter, r *http.Request, id int64) {
	plan, err := db.GetPlan(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	steps, err := db.ListPlanSteps(r.Context(), s.pool, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := struct {
		Plan  db.Plan
		Steps []db.PlanStep
	}{plan, steps}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "plans.detail", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
