package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/config"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/hub"
	"github.com/tonis2/foundry/internal/workflow"
)

// Server holds all handler dependencies.
type Server struct {
	pool            *pgxpool.Pool
	runner          *workflow.Runner
	cerb            *cerberus.Client
	mux             *http.ServeMux
	eventHub        *hub.EventHub
	defaultBudget   float64
	settingsMu      sync.RWMutex
	gitRoot         string
	cfgPath         string
	serverPort      int
	cerberusProfile string
	cerbEventsMu    sync.Mutex
	cerbBuffers     map[string]*cerberusTextBuffer
}

func NewServer(pool *pgxpool.Pool, runner *workflow.Runner, cerb *cerberus.Client, eventHub *hub.EventHub, defaultBudget float64, gitRoot string, cfgPath string, cerberusProfile string, serverPort int) *Server {
	s := &Server{pool: pool, runner: runner, cerb: cerb, eventHub: eventHub, defaultBudget: defaultBudget, gitRoot: gitRoot, cfgPath: cfgPath, serverPort: serverPort, cerberusProfile: cerberusProfile, cerbBuffers: make(map[string]*cerberusTextBuffer)}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

func (s *Server) callbackURL() string {
	return fmt.Sprintf("http://localhost:%d/api/cerberus/events", s.serverPort)
}

func (s *Server) runtimeSettings() (gitRoot, cerberusProfile string) {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.gitRoot, s.cerberusProfile
}

func (s *Server) updateRuntimeSettings(values map[string]string) {
	s.settingsMu.Lock()
	if v, ok := values["git_root"]; ok {
		s.gitRoot = strings.TrimSpace(v)
	}
	if v, ok := values["cerberus_profile"]; ok {
		s.cerberusProfile = strings.TrimSpace(v)
	}
	cerberusProfile := s.cerberusProfile
	s.settingsMu.Unlock()
	if s.runner != nil {
		if _, ok := values["cerberus_profile"]; ok {
			s.runner.SetCerberusProfile(cerberusProfile)
		}
	}
}

func (s *Server) loadRuntimeSettings(ctx context.Context) (map[string]string, error) {
	gitRoot, cerberusProfile := s.runtimeSettings()
	values := map[string]string{"git_root": gitRoot, "cerberus_profile": cerberusProfile}
	for key := range runtimeSettingKeys() {
		setting, err := db.GetAppSetting(ctx, s.pool, key)
		if err == db.ErrNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		values[key] = setting.Value
	}
	s.updateRuntimeSettings(values)
	return values, nil
}

func runtimeSettingKeys() map[string]bool {
	return config.RuntimeSettingKeys()
}

func isRuntimeSetting(key string) bool { return runtimeSettingKeys()[key] }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleUIShell)
	s.mux.HandleFunc("/backlog", s.handleUIBacklogPage)
	s.mux.HandleFunc("/backlog/fragment", s.handleUIBacklogFragment)
	s.mux.HandleFunc("/backlog/projects", s.handleUIBacklogCreateProject)
	s.mux.HandleFunc("/backlog/specs", s.handleUIBacklogCreateSpec)
	s.mux.HandleFunc("/backlog/workflows", s.handleUIBacklogCreateWorkflow)
	s.mux.HandleFunc("/projects", s.handleUIProjectsPage)
	s.mux.HandleFunc("/projects/fragment", s.handleUIProjectsFragment)
	s.mux.HandleFunc("/projects/", s.handleUIProject)
	s.mux.HandleFunc("/settings", s.handleUISettingsPage)
	s.mux.HandleFunc("/settings/fragment", s.handleUISettingsFragment)
	s.mux.HandleFunc("/specs/", s.handleUISpec)
	s.mux.HandleFunc("/workflows/", s.handleUIWorkflow)
	s.mux.HandleFunc("/phases/", s.handleUIPhase)
	s.mux.HandleFunc("/spec-builder", s.handleUISpecBuilderPage)
	s.mux.HandleFunc("/spec-builder/fragment", s.handleUISpecBuilderStartFragment)
	s.mux.HandleFunc("/spec-builder/", s.handleUISpecBuilder)

	s.mux.HandleFunc("/api/export", s.handleExport)
	s.mux.HandleFunc("/api/projects", s.handleProjects)
	s.mux.HandleFunc("/api/projects/discover", s.handleDiscover)
	s.mux.HandleFunc("/api/projects/", s.handleProject)

	s.mux.HandleFunc("/api/specs", s.handleSpecs)
	s.mux.HandleFunc("/api/specs/", s.handleSpec)

	s.mux.HandleFunc("/api/workflows", s.handleWorkflows)
	s.mux.HandleFunc("/api/workflows/", s.handleWorkflow)
	s.mux.HandleFunc("/api/phases/", s.handlePhase)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/profiles", s.handleProfiles)
	s.mux.HandleFunc("/api/profiles/", s.handleProfile)
	s.mux.HandleFunc("/api/cerberus/sessions", s.handleCerberusSessions)
	s.mux.HandleFunc("/api/cerberus/sessions/", s.handleCerberusSession)
	s.mux.HandleFunc("/api/cerberus/events", s.handleCerberusCallback)
	s.mux.HandleFunc("/api/spec-drafts", s.handleSpecDrafts)
	s.mux.HandleFunc("/api/spec-drafts/", s.handleSpecDraft)
}
