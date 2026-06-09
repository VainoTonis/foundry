package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/config"
	"github.com/tonis2/foundry/internal/db"
)

func (h *Handler) HandleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(h.configPath)
		if err != nil {
			jsonErr(w, "cannot read config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		runtimeValues, err := h.loadRuntimeSettings(r.Context())
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
			data, err := os.ReadFile(h.configPath)
			if err != nil {
				jsonErr(w, "cannot read config: "+err.Error(), http.StatusInternalServerError)
				return
			}
			updated := applyYAMLPatch(string(data), configPatch)
			if err := os.WriteFile(h.configPath, []byte(updated), 0644); err != nil {
				jsonErr(w, "cannot write config: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		for k, v := range runtimePatch {
			if _, err := db.UpsertAppSetting(r.Context(), h.pool, k, v); err != nil {
				jsonErr(w, "cannot write setting "+k+": "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if len(runtimePatch) > 0 && h.updateRuntime != nil {
			h.updateRuntime(runtimePatch)
		}
		jsonOK(w, map[string]bool{"success": true}, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func isRuntimeSetting(key string) bool { return config.RuntimeSettingKeys()[key] }

func mergeYAMLRuntimeSettings(yaml string, values map[string]string) string {
	patch := make(map[string]any, len(values))
	for k, v := range values {
		patch[k] = v
	}
	return applyYAMLPatch(yaml, patch)
}

func applyYAMLPatch(yaml string, patch map[string]any) string {
	lines := strings.Split(yaml, "\n")
	replaced := map[string]bool{}
	for i, line := range lines {
		for k, v := range patch {
			prefix := k + ":"
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				lines[i] = fmt.Sprintf("%s: %s", k, yamlValue(v))
				replaced[k] = true
			}
		}
	}
	for k, v := range patch {
		if !replaced[k] {
			lines = append(lines, fmt.Sprintf("%s: %s", k, yamlValue(v)))
		}
	}
	return strings.Join(lines, "\n")
}

func yamlValue(v any) string {
	s := fmt.Sprint(v)
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	if _, err := strconv.ParseBool(s); err == nil {
		return s
	}
	return fmt.Sprintf("%q", s)
}
