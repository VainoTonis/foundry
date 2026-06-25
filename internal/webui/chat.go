package webui

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/tonis2/foundry/internal/db"
)

func (h *Handler) handleUIChatPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/chat" {
		http.NotFound(w, r)
		return
	}
	h.renderShell(w, "chat", "/chat/fragment")
}

func (h *Handler) handleUIChatFragment(w http.ResponseWriter, r *http.Request) {
	sessions, _ := db.ListChatSessions(r.Context(), h.pool)
	if sessions == nil {
		sessions = []db.ChatSession{}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "chat.list", struct {
		Sessions []db.ChatSession
	}{sessions}); err != nil {
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "chat.detail", struct {
		Session  db.ChatSession
		Messages []db.ChatMessage
	}{sess, msgs}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
