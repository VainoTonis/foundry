package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/tonis2/foundry/internal/db"
)

var validFeedbackDimensions = map[string]bool{
	"code_quality": true,
	"tests":        true,
	"docs":         true,
	"performance":  true,
	"security":     true,
	"architecture": true,
	"process":      true,
	"other":        true,
}

var validFeedbackImpacts = map[string]bool{
	"low":      true,
	"medium":   true,
	"high":     true,
	"critical": true,
}

var validFeedbackStatuses = map[string]bool{
	"open":         true,
	"acknowledged": true,
	"in_progress":  true,
	"resolved":     true,
	"dismissed":    true,
}

func (h *Handler) HandleFeedbacks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var reqBody struct {
			Body      string `json:"body"`
			Model     string `json:"model"`
			SessionID string `json:"session_id"`

			Dimension         string   `json:"dimension"`
			Target            string   `json:"target"`
			Score             int      `json:"score"`
			Tags              []string `json:"tags"`
			Evidence          string   `json:"evidence"`
			Impact            string   `json:"impact"`
			RecommendedAction string   `json:"recommended_action"`
			Owner             string   `json:"owner"`
			Status            string   `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Structured, per-dimension feedback: any structured field present
		// routes through structured validation and creation.
		structured := reqBody.Dimension != "" || reqBody.Target != "" || reqBody.Evidence != "" ||
			reqBody.Score != 0 || len(reqBody.Tags) > 0 || reqBody.Impact != "" ||
			reqBody.RecommendedAction != "" || reqBody.Owner != ""

		if structured {
			if !validFeedbackDimensions[reqBody.Dimension] {
				jsonErr(w, "invalid or missing dimension", http.StatusBadRequest)
				return
			}
			if reqBody.Target == "" {
				jsonErr(w, "target is required", http.StatusBadRequest)
				return
			}
			if reqBody.Score < 1 || reqBody.Score > 5 {
				jsonErr(w, "score must be between 1 and 5", http.StatusBadRequest)
				return
			}
			if reqBody.Evidence == "" {
				jsonErr(w, "evidence is required", http.StatusBadRequest)
				return
			}
			if reqBody.Impact != "" && !validFeedbackImpacts[reqBody.Impact] {
				jsonErr(w, "invalid impact", http.StatusBadRequest)
				return
			}
			if reqBody.Status != "" && !validFeedbackStatuses[reqBody.Status] {
				jsonErr(w, "invalid status", http.StatusBadRequest)
				return
			}

			result, err := db.CreateStructuredFeedback(r.Context(), h.pool, db.StructuredFeedbackInput{
				Model:             reqBody.Model,
				SessionID:         reqBody.SessionID,
				Dimension:         reqBody.Dimension,
				Target:            reqBody.Target,
				Score:             reqBody.Score,
				Tags:              reqBody.Tags,
				Evidence:          reqBody.Evidence,
				Impact:            reqBody.Impact,
				RecommendedAction: reqBody.RecommendedAction,
				Owner:             reqBody.Owner,
				Status:            reqBody.Status,
			})
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
			jsonOK(w, result, http.StatusCreated)
			return
		}

		// Legacy free-form feedback.
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
		dimension := r.URL.Query().Get("dimension")
		sessionID := r.URL.Query().Get("session_id")
		status := r.URL.Query().Get("status")
		if dimension != "" || sessionID != "" || status != "" {
			list, err := db.ListFeedbackFiltered(r.Context(), h.pool, dimension, sessionID, status)
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
			jsonOK(w, list, http.StatusOK)
			return
		}
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

func (h *Handler) HandleFeedbackByID(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r.URL.Path, "/api/feedback/")
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Status            string  `json:"status"`
			Owner             *string `json:"owner"`
			RecommendedAction *string `json:"recommended_action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Status == "" {
			jsonErr(w, "status is required", http.StatusBadRequest)
			return
		}
		if !validFeedbackStatuses[body.Status] {
			jsonErr(w, "invalid status", http.StatusBadRequest)
			return
		}
		f, err := db.UpdateFeedbackLifecycle(r.Context(), h.pool, id, db.FeedbackLifecycleUpdate{
			Status:            body.Status,
			Owner:             body.Owner,
			RecommendedAction: body.RecommendedAction,
		})
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
