package webui

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/discover"
)

type uiRepoItem struct {
	discover.Repo
	Imported bool
}

func (s *Handler) handleUIProjectsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/projects" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.renderShell(w, "projects", "/projects/fragment")
	case http.MethodPost:
		s.handleUIProjectCreate(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Handler) handleUIProjectsFragment(w http.ResponseWriter, r *http.Request) {
	projects, err := db.ListProjects(r.Context(), s.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	gitRoot := ""
	if s.runtimeSettings != nil {
		gitRoot, _ = s.runtimeSettings()
	}
	var repos []uiRepoItem
	var discoverErr string
	if r.URL.Query().Get("discover") == "1" {
		if gitRoot == "" {
			discoverErr = "git_root not configured"
		} else if found, err := discover.FindRepos(gitRoot); err != nil {
			discoverErr = err.Error()
		} else {
			byPath := map[string]db.Project{}
			for _, p := range projects {
				byPath[p.RepoPath] = p
			}
			for _, repo := range found {
				_, imported := byPath[repo.Path]
				repos = append(repos, uiRepoItem{Repo: repo, Imported: imported})
			}
		}
	}
	data := struct {
		Projects    []db.Project
		Repos       []uiRepoItem
		DiscoverErr string
	}{projects, repos, discoverErr}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "projects", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Handler) handleUIProjectCreate(w http.ResponseWriter, r *http.Request) {
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
	s.handleUIProjectsFragment(w, r)
}

func (s *Handler) handleUIProject(w http.ResponseWriter, r *http.Request) {
	id, frag, ok := parseUIID(r.URL.Path, "/projects/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if frag {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleUIProjectFragment(w, r, id)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.renderShell(w, "projects", fmt.Sprintf("/projects/%d/fragment", id))
	case http.MethodPatch, http.MethodPost:
		s.handleUIProjectUpdate(w, r, id)
	case http.MethodDelete:
		s.handleUIProjectDelete(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Handler) handleUIProjectUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	repoPath := strings.TrimSpace(r.FormValue("repo_path"))
	if _, err := db.UpdateProject(r.Context(), s.pool, id, db.UpdateProjectParams{
		Name:     &name,
		RepoPath: &repoPath,
	}); errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleUIProjectFragment(w, r, id)
}

func (s *Handler) handleUIProjectDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := db.DeleteProject(r.Context(), s.pool, id); errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/projects")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Handler) handleUIProjectFragment(w http.ResponseWriter, r *http.Request, id int64) {
	p, err := db.GetProject(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "projectDetail", struct {
		Project db.Project
	}{p}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
