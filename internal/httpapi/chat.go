package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/chat"
	"github.com/tonis2/foundry/internal/db"
)

// HandleChatSessions handles GET /api/chat/sessions and POST /api/chat/sessions.
func (h *Handler) HandleChatSessions(w http.ResponseWriter, r *http.Request) {
	svc := h.chatService()
	switch r.Method {
	case http.MethodGet:
		sessions, err := svc.ListSessions(r.Context())
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if sessions == nil {
			sessions = []db.ChatSession{}
		}
		jsonOK(w, sessions, http.StatusOK)

	case http.MethodPost:
		var body struct {
			ProfileName string `json:"profile_name"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
				jsonErr(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		sess, err := svc.CreateSession(r.Context(), body.ProfileName)
		if err != nil {
			if errors.Is(err, chat.ErrProfileNotFound) {
				jsonErr(w, "profile not found", http.StatusBadRequest)
				return
			}
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sess, http.StatusCreated)

	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleChatSession handles requests under /api/chat/sessions/{id}/...
// The /stream suffix is intercepted by httpserver before reaching here.
func (h *Handler) HandleChatSession(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/chat/sessions/")
	parts := strings.SplitN(path, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}

	svc := h.chatService()

	switch {
	case suffix == "" && r.Method == http.MethodGet:
		sess, err := svc.GetSession(r.Context(), id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		msgs, err := svc.ListMessages(r.Context(), id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if msgs == nil {
			msgs = []db.ChatMessage{}
		}
		jsonOK(w, map[string]any{"session": sess, "messages": msgs}, http.StatusOK)

	case suffix == "message" && r.Method == http.MethodPost:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Content == "" {
			jsonErr(w, "content required", http.StatusBadRequest)
			return
		}
		if err := svc.SendMessage(r.Context(), id, body.Content); err != nil {
			if errors.Is(err, chat.ErrSessionBusy) {
				jsonErr(w, "session has an active turn", http.StatusConflict)
				return
			}
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case suffix == "suspend" && r.Method == http.MethodPost:
		if err := svc.SuspendSession(r.Context(), id); err != nil {
			if errors.Is(err, chat.ErrSessionBusy) {
				jsonErr(w, "session has an active turn", http.StatusConflict)
				return
			}
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case suffix == "profile" && r.Method == http.MethodPatch:
		var body struct {
			ProfileName string `json:"profile_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := svc.UpdateSessionProfile(r.Context(), id, body.ProfileName); err != nil {
			if errors.Is(err, chat.ErrProfileNotFound) {
				jsonErr(w, "profile not found", http.StatusBadRequest)
				return
			}
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case suffix == "" && r.Method == http.MethodDelete:
		if err := svc.DeleteSession(r.Context(), id); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func (h *Handler) chatService() *chat.Service {
	return h.chatSvc()
}
