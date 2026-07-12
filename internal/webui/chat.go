package webui

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/tonis2/foundry/internal/db"
)

type chatSessionView struct {
	db.ChatSession
	ActiveProfileName  string
	UsesRuntimeProfile bool
}

func (h *Handler) handleUIChatPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/chat" {
		http.NotFound(w, r)
		return
	}
	h.renderShell(w, "chat", "/chat/fragment")
}

func (h *Handler) handleUIChatFragment(w http.ResponseWriter, r *http.Request) {
	sessions, err := db.ListChatSessions(r.Context(), h.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sessions == nil {
		sessions = []db.ChatSession{}
	}
	profiles, _ := db.ListProfiles(r.Context(), h.pool)
	_, runtimeProfile := h.runtimeProfiles()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "chat.list", struct {
		Sessions       []chatSessionView
		Profiles       []db.Profile
		RuntimeProfile string
	}{chatSessionViews(sessions, runtimeProfile), profiles, runtimeProfile}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleUIChat(w http.ResponseWriter, r *http.Request) {
	id, suffix, ok := parseUIIDSuffix(r.URL.Path, "/chat/")
	if !ok || (suffix != "" && suffix != "fragment") {
		http.NotFound(w, r)
		return
	}
	if suffix == "fragment" {
		h.handleUIChatDetailFragment(w, r, id)
		return
	}
	h.renderShell(w, "chat", fmt.Sprintf("/chat/%d/fragment", id))
}

func (h *Handler) handleUIChatDetailFragment(w http.ResponseWriter, r *http.Request, id int64) {
	sess, err := db.GetChatSession(r.Context(), h.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	msgs, err := db.ListChatMessages(r.Context(), h.pool, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if msgs == nil {
		msgs = []db.ChatMessage{}
	}
	profiles, _ := db.ListProfiles(r.Context(), h.pool)
	attachedProjects, _ := db.ListSessionProjects(r.Context(), h.pool, id)
	allProjects, _ := db.ListProjects(r.Context(), h.pool)
	if attachedProjects == nil {
		attachedProjects = []db.Project{}
	}

	attachedSet := make(map[int64]bool, len(attachedProjects))
	for _, p := range attachedProjects {
		attachedSet[p.ID] = true
	}
	var availableProjects []db.Project
	for _, p := range allProjects {
		if !attachedSet[p.ID] {
			availableProjects = append(availableProjects, p)
		}
	}
	if availableProjects == nil {
		availableProjects = []db.Project{}
	}

	_, runtimeProfile := h.runtimeProfiles()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "chat.detail", struct {
		Session            db.ChatSession
		Messages           []db.ChatMessage
		Profiles           []db.Profile
		ActiveProfileName  string
		RuntimeProfile     string
		AttachedProjects   []db.Project
		AvailableProjects  []db.Project
	}{sess, msgs, profiles, activeProfileName(sess.ProfileName, runtimeProfile), runtimeProfile, attachedProjects, availableProjects}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) runtimeProfiles() (string, string) {
	if h.runtimeSettings == nil {
		return "", ""
	}
	return h.runtimeSettings()
}

func chatSessionViews(sessions []db.ChatSession, runtimeProfile string) []chatSessionView {
	out := make([]chatSessionView, len(sessions))
	for i, sess := range sessions {
		out[i] = chatSessionView{
			ChatSession:        sess,
			ActiveProfileName:  activeProfileName(sess.ProfileName, runtimeProfile),
			UsesRuntimeProfile: sess.ProfileName == "" && runtimeProfile != "",
		}
	}
	return out
}

func activeProfileName(sessionProfile, runtimeProfile string) string {
	if sessionProfile != "" {
		return sessionProfile
	}
	return runtimeProfile
}
