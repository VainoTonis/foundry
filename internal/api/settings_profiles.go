package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

// ---- settings ----

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(s.cfgPath)
		if err != nil {
			jsonErr(w, "cannot read config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		runtimeValues, err := s.loadRuntimeSettings(r.Context())
		if err != nil {
			jsonErr(w, "cannot read runtime settings: "+err.Error(), http.StatusInternalServerError)
			return
		}
		merged := mergeYAMLRuntimeSettings(string(data), runtimeValues)
		w.Header().Set("Content-Type", "application/x-yaml")
		w.Write([]byte(merged))
	case http.MethodPatch:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		runtimePatch := map[string]string{}
		configPatch := map[string]any{}
		for k, v := range body {
			if isRuntimeSetting(k) {
				runtimePatch[k] = strings.TrimSpace(fmt.Sprint(v))
			} else {
				configPatch[k] = v
			}
		}
		if len(configPatch) > 0 {
			data, err := os.ReadFile(s.cfgPath)
			if err != nil {
				jsonErr(w, "cannot read config: "+err.Error(), http.StatusInternalServerError)
				return
			}
			updated := applyYAMLPatch(string(data), configPatch)
			if err := os.WriteFile(s.cfgPath, []byte(updated), 0644); err != nil {
				jsonErr(w, "cannot write config: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		for k, v := range runtimePatch {
			if _, err := db.UpsertAppSetting(r.Context(), s.pool, k, v); err != nil {
				jsonErr(w, "cannot write setting "+k+": "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if len(runtimePatch) > 0 {
			s.updateRuntimeSettings(runtimePatch)
		}
		jsonOK(w, map[string]bool{"success": true}, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---- profiles ----

func (s *Server) writeProfileFile(ctx context.Context, session string) (string, error) {
	_, _, cerberusProfile := s.runtimeSettings()
	if cerberusProfile == "" {
		return "", nil
	}
	p, err := db.GetProfileByName(ctx, s.pool, cerberusProfile)
	if err == db.ErrNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup profile %q: %w", cerberusProfile, err)
	}
	payload := map[string]any{}
	if p.DefaultModel != "" {
		payload["default_model"] = p.DefaultModel
	}
	if p.DefaultImage != "" {
		payload["default_image"] = p.DefaultImage
	}
	if p.AWSProfile != "" {
		payload["aws_profile"] = p.AWSProfile
	}
	if p.AWSRegion != "" {
		payload["aws_region"] = p.AWSRegion
	}
	if len(p.ExtraEnv) > 0 {
		payload["extra_env"] = p.ExtraEnv
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal profile: %w", err)
	}
	path := profileFilePath(session)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write profile file: %w", err)
	}
	return path, nil
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		profiles, err := db.ListProfiles(r.Context(), s.pool)
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
		p, err := db.CreateProfile(r.Context(), s.pool, body.Name, body.DefaultModel, body.DefaultImage, body.AWSProfile, body.AWSRegion, body.ExtraEnv)
		if err != nil {
			jsonErr(w, "create profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusCreated)

	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	idStr := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/profiles/"), "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, err := db.GetProfile(r.Context(), s.pool, id)
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
		p, err := db.UpdateProfile(r.Context(), s.pool, id, db.UpdateProfileParams{
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
		if err := db.DeleteProfile(r.Context(), s.pool, id); errors.Is(err, db.ErrNotFound) {
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
