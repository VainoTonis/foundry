package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/tonis2/foundry/internal/db"
)

var validKnowledgeFeedbackKinds = map[string]bool{
	"stale":    true,
	"wrong":    true,
	"thin":     true,
	"conflict": true,
	"gap":      true,
	"confirm":  true,
}

func (h *Handler) HandleKnowledgeFeedback(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Kind       string `json:"kind"`
			NotePath   string `json:"note_path"`
			Topic      string `json:"topic"`
			Evidence   string `json:"evidence"`
			Suggestion string `json:"suggestion"`
			Origin     string `json:"origin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Evidence == "" {
			jsonErr(w, "evidence is required", http.StatusBadRequest)
			return
		}
		if !validKnowledgeFeedbackKinds[body.Kind] {
			jsonErr(w, "invalid kind", http.StatusBadRequest)
			return
		}
		if body.NotePath == "" && body.Kind != "gap" {
			jsonErr(w, "note_path is required", http.StatusBadRequest)
			return
		}
		result, err := db.CreateKnowledgeFeedback(r.Context(), h.pool, body.Kind, body.NotePath, body.Topic, body.Evidence, body.Suggestion, body.Origin)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, result, http.StatusCreated)
	case http.MethodGet:
		status := r.URL.Query().Get("status")
		notePath := r.URL.Query().Get("note_path")
		list, err := db.ListKnowledgeFeedback(r.Context(), h.pool, status, notePath)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) HandleKnowledgeFeedbackByID(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r.URL.Path, "/api/knowledge-feedback/")
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		f, err := db.UpdateKnowledgeFeedbackStatus(r.Context(), h.pool, id, body.Status)
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, f, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
