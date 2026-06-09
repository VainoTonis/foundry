package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/tonis2/foundry/internal/authoring"
	"github.com/tonis2/foundry/internal/db"
)

type chatMessage struct{ Role, Content string }

func (s *Handler) handleUISpecBuilderPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/spec-builder" {
		http.NotFound(w, r)
		return
	}
	s.renderShell(w, "builder", "/spec-builder/fragment")
}

func (s *Handler) handleUISpecBuilderStartFragment(w http.ResponseWriter, r *http.Request) {
	projects, _ := db.ListProjects(r.Context(), s.pool)
	drafts, _ := db.ListSpecDrafts(r.Context(), s.pool)
	active := []db.SpecDraft{}
	for _, d := range drafts {
		if d.Status == "active" {
			active = append(active, d)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "builderStart", struct {
		Projects []db.Project
		Drafts   []db.SpecDraft
	}{projects, active}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Handler) handleUISpecBuilder(w http.ResponseWriter, r *http.Request) {
	id, suffix, ok := parseUIIDSuffix(r.URL.Path, "/spec-builder/")
	if !ok || (suffix != "" && suffix != "fragment") {
		http.NotFound(w, r)
		return
	}
	if suffix == "fragment" {
		s.handleUISpecBuilderDetailFragment(w, r, id)
		return
	}
	s.renderShell(w, "builder", fmt.Sprintf("/spec-builder/%d/fragment", id))
}

func (s *Handler) handleUISpecBuilderDetailFragment(w http.ResponseWriter, r *http.Request, id int64) {
	draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var msgs []chatMessage
	_ = json.Unmarshal(draft.Messages, &msgs)
	preview := authoring.ExtractFinalSpec(draft.Messages)
	var proj db.Project
	hasProject := false
	if draft.ProjectID != nil {
		if p, err := db.GetProject(r.Context(), s.pool, *draft.ProjectID); err == nil {
			proj = p
			hasProject = true
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "builderDetail", struct {
		Draft      db.SpecDraft
		Messages   []chatMessage
		Preview    string
		Project    db.Project
		HasProject bool
	}{draft, msgs, preview, proj, hasProject}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
