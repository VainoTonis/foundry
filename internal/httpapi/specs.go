package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

// ---- specs ----

func (h *Handler) HandleSpecs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			ProjectID int64           `json:"project_id"`
			Title     string          `json:"title"`
			Content   string          `json:"content"`
			Tags      json.RawMessage `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		tags := []byte("[]")
		if body.Tags != nil {
			tags = body.Tags
		}
		sp, err := db.CreateSpec(r.Context(), h.pool, body.ProjectID, body.Title, body.Content, tags)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusCreated)
	case http.MethodGet:
		f := db.ListSpecsFilter{
			Status: r.URL.Query().Get("status"),
		}
		if pid := r.URL.Query().Get("project_id"); pid != "" {
			f.ProjectID, _ = strconv.ParseInt(pid, 10, 64)
		}
		list, err := db.ListSpecs(r.Context(), h.pool, f)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) HandleSpec(w http.ResponseWriter, r *http.Request) {
	// routes under /api/specs/:id and /api/specs/:id/promote
	path := strings.TrimPrefix(r.URL.Path, "/api/specs/")
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

	switch {
	case suffix == "workflows" && r.Method == http.MethodGet:
		wfs, err := db.ListWorkflowsBySpec(r.Context(), h.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, wfs, http.StatusOK)
	case suffix == "promote" && r.Method == http.MethodPost:
		sp, err := db.GetSpec(r.Context(), h.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		track := "polish"
		sp, err = db.UpdateSpec(r.Context(), h.pool, sp.ID, db.UpdateSpecParams{Track: &track})
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusOK)
	case suffix == "" && r.Method == http.MethodGet:
		sp, err := db.GetSpec(r.Context(), h.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusOK)
	case suffix == "" && r.Method == http.MethodPatch:
		var body struct {
			Title   *string         `json:"title"`
			Content *string         `json:"content"`
			Tags    json.RawMessage `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		var tags []byte
		if body.Tags != nil {
			tags = body.Tags
		}
		sp, err := db.UpdateSpec(r.Context(), h.pool, id, db.UpdateSpecParams{
			Title:   body.Title,
			Content: body.Content,
			Tags:    tags,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusOK)
	case suffix == "" && r.Method == http.MethodDelete:
		_, err := db.GetSpec(r.Context(), h.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		err = db.DeleteSpec(r.Context(), h.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}
