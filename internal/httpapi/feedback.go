package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/tonis2/foundry/internal/db"
)

func (h *Handler) HandleFeedbacks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var reqBody struct {
			Body      string `json:"body"`
			Model     string `json:"model"`
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if reqBody.Body == "" {
			jsonErr(w, "body is required", http.StatusBadRequest)
			return
		}
		result, err := db.CreateFeedback(r.Context(), h.pool, reqBody.Body, reqBody.Model, reqBody.SessionID)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, result, http.StatusCreated)
	case http.MethodGet:
		list, err := db.ListFeedback(r.Context(), h.pool)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
