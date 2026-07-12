package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

func (h *Handler) HandleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		profiles, err := db.ListProfiles(r.Context(), h.pool)
		if err != nil {
			jsonErr(w, "list profiles: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if profiles == nil {
			profiles = []db.Profile{}
		}
		jsonOK(w, profiles, http.StatusOK)

	case http.MethodPost:
		var body struct {
			Name         string            `json:"name"`
			DefaultModel string            `json:"default_model"`
			DefaultImage string            `json:"default_image"`
			AWSProfile   string            `json:"aws_profile"`
			AWSRegion    string            `json:"aws_region"`
			ExtraEnv     map[string]string `json:"extra_env"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Name == "" {
			jsonErr(w, "name is required", http.StatusBadRequest)
			return
		}
		if body.ExtraEnv == nil {
			body.ExtraEnv = map[string]string{}
		}
		p, err := db.CreateProfile(r.Context(), h.pool, body.Name, body.DefaultModel, body.DefaultImage, body.AWSProfile, body.AWSRegion, body.ExtraEnv)
		if err != nil {
			jsonErr(w, "create profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusCreated)

	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) HandleProfile(w http.ResponseWriter, r *http.Request) {
	idStr := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/profiles/"), "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, err := db.GetProfile(r.Context(), h.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, "get profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case http.MethodPatch:
		var body struct {
			Name         *string           `json:"name"`
			DefaultModel *string           `json:"default_model"`
			DefaultImage *string           `json:"default_image"`
			AWSProfile   *string           `json:"aws_profile"`
			AWSRegion    *string           `json:"aws_region"`
			ExtraEnv     map[string]string `json:"extra_env"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Name != nil && *body.Name == "" {
			jsonErr(w, "name is required", http.StatusBadRequest)
			return
		}
		p, err := db.UpdateProfile(r.Context(), h.pool, id, db.UpdateProfileParams{
			Name: body.Name, DefaultModel: body.DefaultModel, DefaultImage: body.DefaultImage,
			AWSProfile: body.AWSProfile, AWSRegion: body.AWSRegion, ExtraEnv: body.ExtraEnv,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, "update profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case http.MethodDelete:
		if err := db.DeleteProfile(r.Context(), h.pool, id); errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		} else if err != nil {
			jsonErr(w, "delete profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
