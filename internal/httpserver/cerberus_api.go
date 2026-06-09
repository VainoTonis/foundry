package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

type cerberusSessionView struct {
	db.KnownCerberusSession
	CerberusStatus string `json:"cerberus_status"`
	CerberusError  string `json:"cerberus_error,omitempty"`
}

// ---- cerberus callback ----

func (s *Server) handleCerberusCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		jsonErr(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.handleCompactCerberusEvent(r.Context(), raw); err != nil {
		code := http.StatusInternalServerError
		if strings.HasPrefix(err.Error(), "invalid json") || strings.Contains(err.Error(), "session and type required") {
			code = http.StatusBadRequest
		}
		jsonErr(w, err.Error(), code)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- cerberus sessions ----

func (s *Server) handleCerberusSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		views, err := s.knownCerberusSessionViews(r.Context(), true)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, views, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCerberusSession(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/cerberus/sessions/")
	if strings.HasSuffix(path, "/clean") && r.Method == http.MethodPost {
		session := strings.TrimSuffix(path, "/clean")
		force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
		var body struct {
			Force bool `json:"force"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Force {
				force = true
			}
		}
		s.cleanKnownCerberusSession(w, r, session, force)
		return
	}
	jsonErr(w, "not found", http.StatusNotFound)
}

func (s *Server) cleanKnownCerberusSession(w http.ResponseWriter, r *http.Request, session string, force bool) {
	known, err := db.ListKnownCerberusSessions(r.Context(), s.pool)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var item *db.KnownCerberusSession
	for i := range known {
		if known[i].Session == session {
			item = &known[i]
			break
		}
	}
	if item == nil {
		jsonErr(w, "unknown Foundry session", http.StatusNotFound)
		return
	}
	if !item.SafeToClean && !force {
		jsonErr(w, "refusing to clean active session: "+item.UnsafeReason, http.StatusConflict)
		return
	}
	if strings.TrimSpace(item.ProjectRepo) != "" {
		s.cerb.SetRepoPath(item.ProjectRepo)
	}
	if err := s.cerb.Clean(r.Context(), item.Session); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	db.DeleteCerberusEvents(r.Context(), s.pool, item.Session)
	removeProfileFile(item.Session)
	jsonOK(w, map[string]string{"status": "cleaned", "session": item.Session}, http.StatusOK)
}

func (s *Server) knownCerberusSessionViews(ctx context.Context, withStatus bool) ([]cerberusSessionView, error) {
	known, err := db.ListKnownCerberusSessions(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	views := make([]cerberusSessionView, 0, len(known))
	for _, k := range known {
		v := cerberusSessionView{KnownCerberusSession: k}
		if withStatus && s.cerb != nil {
			if strings.TrimSpace(k.ProjectRepo) != "" {
				s.cerb.SetRepoPath(k.ProjectRepo)
			}
			status, err := s.cerb.Status(ctx, k.Session)
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
