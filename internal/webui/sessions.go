package webui

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

type cerberusSessionView struct {
	db.KnownCerberusSession
	CerberusStatus string `json:"cerberus_status"`
	CerberusError  string `json:"cerberus_error,omitempty"`
}

func (s *Handler) handleUISessionsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/sessions" {
		http.NotFound(w, r)
		return
	}
	s.renderShell(w, "sessions", "/sessions/fragment")
}

func (s *Handler) handleUISessionsFragment(w http.ResponseWriter, r *http.Request) {
	sessions, sessionErr := s.knownCerberusSessionViews(r.Context(), true)
	sessionErrMsg := ""
	if sessionErr != nil {
		sessionErrMsg = sessionErr.Error()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "sessions.list", struct {
		Sessions     []cerberusSessionView
		SessionError string
	}{sessions, sessionErrMsg}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Handler) handleUISession(w http.ResponseWriter, r *http.Request) {
	name, suffix, ok := parseUISessionSuffix(r.URL.Path, "/sessions/")
	if !ok || (suffix != "" && suffix != "fragment") {
		http.NotFound(w, r)
		return
	}
	if suffix == "fragment" {
		s.handleUISessionFragment(w, r, name)
		return
	}
	s.renderShell(w, "sessions", fmt.Sprintf("/sessions/%s/fragment", name))
}

func (s *Handler) handleUISessionFragment(w http.ResponseWriter, r *http.Request, name string) {
	sessions, err := s.knownCerberusSessionViews(r.Context(), true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var found *cerberusSessionView
	for i := range sessions {
		if sessions[i].Session == name {
			found = &sessions[i]
			break
		}
	}
	if found == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "sessions.detail", struct {
		Session cerberusSessionView
	}{*found}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// parseUISessionSuffix splits a "/sessions/<name>[/<suffix>]" path into the
// session name and optional trailing suffix (e.g. "fragment"). Session names
// are opaque strings (not numeric IDs), so this mirrors parseUIIDSuffix
// without the integer parsing.
func parseUISessionSuffix(path, prefix string) (string, string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], parts[1], true
}

func (s *Handler) knownCerberusSessionViews(ctx context.Context, withStatus bool) ([]cerberusSessionView, error) {
	known, err := db.ListKnownCerberusSessions(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	views := make([]cerberusSessionView, 0, len(known))
	for _, k := range known {
		v := cerberusSessionView{KnownCerberusSession: k}
		if withStatus && s.cerb != nil {
			var status string
			var err error
			repoPath := strings.TrimSpace(k.RepositoryLocalPath)
			if repoPath != "" {
				status, err = s.cerb.WithRepo(repoPath).Status(ctx, k.Session)
			} else {
				status, err = s.cerb.Status(ctx, k.Session)
			}
			if err != nil {
				v.CerberusError = err.Error()
			} else {
				v.CerberusStatus = status
			}
		}
		views = append(views, v)
	}
	return views, nil
}
