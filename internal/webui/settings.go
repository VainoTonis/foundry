package webui

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

type cerberusSessionView struct {
	db.KnownCerberusSession
	CerberusStatus string `json:"cerberus_status"`
	CerberusError  string `json:"cerberus_error,omitempty"`
}

func (s *Handler) handleUISettingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderShell(w, "settings", "/settings/fragment")
}

func (s *Handler) handleUISettingsFragment(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.cfgPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	runtimeValues, err := s.loadRuntimeSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cerberusProfile := runtimeValues["cerberus_profile"]
	mergedConfig := mergeYAMLRuntimeSettings(string(data), runtimeValues)
	profiles, _ := db.ListProfiles(r.Context(), s.pool)
	sessions, sessionErr := s.knownCerberusSessionViews(r.Context(), true)
	sessionErrMsg := ""
	if sessionErr != nil {
		sessionErrMsg = sessionErr.Error()
	}
	type setting struct {
		Key, Value        string
		IsVerbosity       bool
		IsCerberusProfile bool
		IsRuntime         bool
	}
	var settings []setting
	var verbosityKey, verbosityValue string
	foundCerberusProfile := false
	for _, line := range strings.Split(mergedConfig, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			key := strings.TrimSpace(parts[0])
			value := strings.Trim(strings.TrimSpace(parts[1]), "\"")
			isVerbosity := key == "verbosity" || key == "ui_verbosity" || key == "log_verbosity"
			isCerberusProfile := key == "cerberus_profile"
			if isVerbosity && verbosityKey == "" {
				verbosityKey, verbosityValue = key, value
			}
			if isCerberusProfile {
				cerberusProfile = value
				foundCerberusProfile = true
			}
			settings = append(settings, setting{Key: key, Value: value, IsVerbosity: isVerbosity, IsCerberusProfile: isCerberusProfile, IsRuntime: isRuntimeSetting(key)})
		}
	}
	if !foundCerberusProfile && cerberusProfile == "" {
		cerberusProfile = ""
	}
	cerberusProfileExists := cerberusProfile == ""
	for _, p := range profiles {
		if p.Name == cerberusProfile {
			cerberusProfileExists = true
			break
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "settings.main", struct {
		Settings              []setting
		Profiles              []db.Profile
		Sessions              []cerberusSessionView
		SessionError          string
		HasVerbosity          bool
		VerbosityKey          string
		VerbosityValue        string
		CerberusProfile       string
		CerberusProfileExists bool
	}{settings, profiles, sessions, sessionErrMsg, verbosityKey != "", verbosityKey, verbosityValue, cerberusProfile, cerberusProfileExists}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Handler) knownCerberusSessionViews(ctx context.Context, withStatus bool) ([]cerberusSessionView, error) {
	known, err := db.ListKnownCerberusSessions(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	views := make([]cerberusSessionView, 0, len(known))
	for _, k := range known {
		v := cerberusSessionView{KnownCerberusSession: k}
		if withStatus && s.cerb != nil {
			if strings.TrimSpace(k.ProjectRepo) != "" {
				s.cerb.SetRepoPath(k.ProjectRepo)
			}
			status, err := s.cerb.Status(ctx, k.Session)
			if err != nil {
				v.CerberusError = err.Error()
			} else {
				v.CerberusStatus = status
			}
		}
		views = append(views, v)
	}
	return views, nil
}
