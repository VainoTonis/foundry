package webui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/app"
	"github.com/tonis2/foundry/internal/db"
)

type specRow struct {
	db.Spec
	ProjectName string
}

type specGroup struct {
	Label string
	Items []specRow
}

func (s *Handler) handleUIBacklogPage(w http.ResponseWriter, r *http.Request) {
	s.renderShell(w, "backlog", "/backlog/fragment")
}

func (s *Handler) handleUIBacklogFragment(w http.ResponseWriter, r *http.Request) {
	projects, _ := db.ListProjects(r.Context(), s.pool)
	specs, err := db.ListSpecs(r.Context(), s.pool, db.ListSpecsFilter{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	drafts, _ := db.ListSpecDrafts(r.Context(), s.pool)
	activeDrafts := make([]db.SpecDraft, 0)
	for _, d := range drafts {
		if d.Status == "active" {
			activeDrafts = append(activeDrafts, d)
		}
	}
	projectNames := map[int64]string{}
	for _, p := range projects {
		projectNames[p.ID] = p.Name
	}
	groups := []specGroup{{Label: "Needs attention"}, {Label: "Running / queued"}, {Label: "Ready to run"}, {Label: "Completed"}, {Label: "Other states"}}
	for _, sp := range specs {
		row := specRow{Spec: sp, ProjectName: projectNames[sp.ProjectID]}
		if row.ProjectName == "" {
			row.ProjectName = fmt.Sprintf("Project #%d", sp.ProjectID)
		}
		switch sp.Status {
		case "failed", "blocked", "awaiting_review", "review", "paused":
			groups[0].Items = append(groups[0].Items, row)
		case "running", "queued":
			groups[1].Items = append(groups[1].Items, row)
		case "pending", "idle", "draft":
			groups[2].Items = append(groups[2].Items, row)
		case "done", "accepted":
			groups[3].Items = append(groups[3].Items, row)
		default:
			groups[4].Items = append(groups[4].Items, row)
		}
	}
	data := struct {
		Projects []db.Project
		Groups   []specGroup
		HasSpecs bool
		Drafts   []db.SpecDraft
	}{projects, groups, len(specs) > 0, activeDrafts}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "backlog.main", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Handler) handleUIBacklogCreateProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	repoPath := strings.TrimSpace(r.FormValue("repo_path"))

	if _, err := db.CreateProject(r.Context(), s.pool, name, repoPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleUIBacklogFragment(w, r)
}

func (s *Handler) handleUIBacklogCreateSpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projectID, err := strconv.ParseInt(r.FormValue("project_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid project_id", http.StatusBadRequest)
		return
	}
	if _, err := db.CreateSpec(r.Context(), s.pool, projectID, strings.TrimSpace(r.FormValue("title")), r.FormValue("content"), []byte("[]")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleUIBacklogFragment(w, r)
}

func (s *Handler) handleUIBacklogCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Parse form input
	specID, err := strconv.ParseInt(r.FormValue("spec_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid spec_id", http.StatusBadRequest)
		return
	}

	var maxCost *float64
	if raw := strings.TrimSpace(r.FormValue("max_cost_usd")); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			http.Error(w, "invalid max_cost_usd", http.StatusBadRequest)
			return
		}
		maxCost = &parsed
	} else {
		maxCost = &s.defaultBudget
	}

	// Execute use case
	uc := app.NewWorkflowStartUseCase(s.pool, s.runner)
	res, err := uc.Execute(r.Context(), app.WorkflowStartRequest{
		SpecID:     specID,
		MaxCostUSD: maxCost,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Return HX redirect
	w.Header().Set("HX-Redirect", fmt.Sprintf("/workflows/%d", res.WorkflowID))
	w.WriteHeader(http.StatusCreated)
}
