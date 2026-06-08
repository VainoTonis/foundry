package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

// JSON response helpers
func jsonOK(w http.ResponseWriter, v any, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Path parsing helpers
func pathID(path, prefix string) (int64, error) {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, "/")
	return strconv.ParseInt(s, 10, 64)
}

func parseUIID(path, prefix string) (id int64, fragment bool, ok bool) {
	id, suffix, ok := parseUIIDSuffix(path, prefix)
	return id, suffix == "fragment", ok && (suffix == "" || suffix == "fragment")
}

func parseUIIDSuffix(path, prefix string) (int64, string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return 0, "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", false
	}
	if len(parts) == 1 {
		return id, "", true
	}
	return id, parts[1], true
}

// YAML settings helpers
func mergeYAMLRuntimeSettings(yaml string, values map[string]string) string {
	patch := make(map[string]any, len(values))
	for k, v := range values {
		patch[k] = v
	}
	return applyYAMLPatch(yaml, patch)
}

// applyYAMLPatch does a naive line-by-line replacement of top-level YAML keys.
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

// yamlValue formats a value for a YAML line.
// Strings get quoted; numbers and booleans are written bare.
func yamlValue(v any) string {
	s := fmt.Sprint(v)
	// if it parses as a number or bool, write bare
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	if _, err := strconv.ParseBool(s); err == nil {
		return s
	}
	return fmt.Sprintf("%q", s)
}

// Profile file path helpers
func profileFilePath(session string) string {
	return "/tmp/foundry-profile-" + session + ".json"
}

// removeProfileFile deletes the profile file for a session if it exists.
func removeProfileFile(session string) {
	os.Remove(profileFilePath(session))
}

// String manipulation helpers
func indentBlock(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}

func truncateString(s string, max int) string {
	const marker = "\n... truncated ..."
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= len(marker) {
		return s[:max]
	}
	return s[:max-len(marker)] + marker
}

func (s *Server) workflowProject(ctx context.Context, workflowID int64) (db.Workflow, db.Spec, db.Project, error) {
	wf, err := db.GetWorkflow(ctx, s.pool, workflowID)
	if err != nil {
		return wf, db.Spec{}, db.Project{}, err
	}
	sp, err := db.GetSpec(ctx, s.pool, wf.SpecID)
	if err != nil {
		return wf, sp, db.Project{}, err
	}
	proj, err := db.GetProject(ctx, s.pool, sp.ProjectID)
	return wf, sp, proj, err
}
