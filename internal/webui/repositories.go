package webui

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/discover"
	"github.com/tonis2/foundry/internal/repository"
)

// discoveredRepo pairs a filesystem-discovered git repository with
// whether it has already been registered as a canonical Repository (by
// local path), for rendering in the discovery panel.
type discoveredRepo struct {
	discover.Repo
	Imported bool
}

// strPtrOrNil trims s and returns nil if the result is empty, or a
// pointer to the trimmed value otherwise. Form inputs are always plain
// strings, so this is how form-submitted locator fields become the
// nil-means-absent *string values the repository domain model expects.
func strPtrOrNil(s string) *string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func (s *Handler) handleUIRepositoriesPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/repositories" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.renderShell(w, "repositories", "/repositories/fragment")
	case http.MethodPost:
		s.handleUIRepositoryCreate(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Handler) handleUIRepositoriesFragment(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.renderRepositoriesFragment(w, r, nil)
	case http.MethodPost:
		if r.URL.Query().Get("refresh") != "1" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		results, err := db.RefreshRepositories(r.Context(), s.pool)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.renderRepositoriesFragment(w, r, results)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// renderRepositoriesFragment renders the repositories list fragment,
// optionally including a discovery scan (triggered via ?discover=1) and/or
// the results of a just-completed refresh action.
func (s *Handler) renderRepositoriesFragment(w http.ResponseWriter, r *http.Request, refreshResults []db.RefreshResult) {
	repos, err := db.ListRepositories(r.Context(), s.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	gitRoot := ""
	if s.runtimeSettings != nil {
		gitRoot, _ = s.runtimeSettings()
	}
	var discovered []discoveredRepo
	var discoverErr string
	if r.URL.Query().Get("discover") == "1" {
		if gitRoot == "" {
			discoverErr = "git_root not configured"
		} else if found, err := discover.FindRepos(gitRoot); err != nil {
			discoverErr = err.Error()
		} else {
			byLocalPath := map[string]bool{}
			for _, repo := range repos {
				if repo.LocalPath != nil {
					byLocalPath[*repo.LocalPath] = true
				}
			}
			for _, repo := range found {
				discovered = append(discovered, discoveredRepo{Repo: repo, Imported: byLocalPath[repo.Path]})
			}
		}
	}
	data := struct {
		Repositories   []repository.Repository
		Discovered     []discoveredRepo
		DiscoverErr    string
		RefreshResults []db.RefreshResult
	}{repos, discovered, discoverErr, refreshResults}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "repositories.main", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Handler) handleUIRepositoryCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	localPath := strPtrOrNil(r.FormValue("local_path"))
	remoteURL := strPtrOrNil(r.FormValue("remote_url"))
	if _, err := db.CreateRepository(r.Context(), s.pool, repository.Repository{
		Name:      name,
		LocalPath: localPath,
		RemoteURL: remoteURL,
	}); errors.Is(err, repository.ErrNoLocator) || errors.Is(err, repository.ErrInvalidLocator) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderRepositoriesFragment(w, r, nil)
}

func (s *Handler) handleUIRepository(w http.ResponseWriter, r *http.Request) {
	id, frag, ok := parseUIID(r.URL.Path, "/repositories/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if frag {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.renderRepositoryFragment(w, r, id)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.renderShell(w, "repositories", fmt.Sprintf("/repositories/%d/fragment", id))
	case http.MethodPatch, http.MethodPost:
		s.handleUIRepositoryUpdate(w, r, id)
	case http.MethodDelete:
		s.handleUIRepositoryDelete(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Handler) handleUIRepositoryUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	localPath := strPtrOrNil(r.FormValue("local_path"))
	remoteURL := strPtrOrNil(r.FormValue("remote_url"))
	_, err := db.UpdateRepository(r.Context(), s.pool, id, db.UpdateRepositoryParams{
		Name:      &name,
		LocalPath: db.SetLocator(localPath),
		RemoteURL: db.SetLocator(remoteURL),
	})
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, repository.ErrNoLocator) || errors.Is(err, repository.ErrInvalidLocator) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderRepositoryFragment(w, r, id)
}

func (s *Handler) handleUIRepositoryDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := db.DeleteRepository(r.Context(), s.pool, id); errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/repositories")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Handler) renderRepositoryFragment(w http.ResponseWriter, r *http.Request, id int64) {
	repo, err := db.GetRepository(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "repositories.detail", struct {
		Repository repository.Repository
	}{repo}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
