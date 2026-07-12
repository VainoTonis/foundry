package httpserver

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/chat"
	"github.com/tonis2/foundry/internal/config"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/httpapi"
	"github.com/tonis2/foundry/internal/hub"
	"github.com/tonis2/foundry/internal/webui"
	"github.com/tonis2/foundry/internal/workflow"
)

const chatIdleSuspendAfter = 20 * time.Minute

// Server wires HTTP routes and shared edge dependencies.
type Server struct {
	pool            *pgxpool.Pool
	runner          *workflow.Runner
	cerb            *cerberus.Client
	chatSvc         *chat.Service
	jsonAPI         *httpapi.Handler
	webUI           *webui.Handler
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
	s.chatSvc = chat.NewService(pool, cerb, s.callbackURL(), func() string {
		_, profile := s.runtimeSettings()
		return profile
	})
	s.jsonAPI = httpapi.New(pool, httpapi.Config{
		GitRoot: func() string {
			gitRoot, _ := s.runtimeSettings()
			return gitRoot
		},
		ConfigPath:          cfgPath,
		LoadRuntimeSettings: s.loadRuntimeSettings,
		UpdateRuntime:       s.updateRuntimeSettings,
		WorkflowRunner:      runner,
		DefaultBudget:       defaultBudget,
		SpecDraftsService:   s.newSpecDraftsService,
		ChatService:         func() httpapi.ChatService { return s.chatSvc },
		Cerberus:            cerb,
		ProjectRepoForWorkflow: func(ctx context.Context, workflowID int64) (string, error) {
			_, _, project, err := s.workflowProject(ctx, workflowID)
			return project.RepoPath, err
		},
		RemoveProfileFile: removeProfileFile,
	})
	s.webUI = webui.New(pool, runner, cerb, webui.Config{
		DefaultBudget:       defaultBudget,
		ConfigPath:          cfgPath,
		RuntimeSettings:     s.runtimeSettings,
		LoadRuntimeSettings: s.loadRuntimeSettings,
	})
	s.mux = http.NewServeMux()
	s.routes()
	go s.runChatIdleJanitor(context.Background())
	return s
}

func (s *Server) runChatIdleJanitor(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.chatSvc.AutoSuspendIdleSessions(ctx, chatIdleSuspendAfter); err != nil {
				log.Printf("chat idle suspend: %v", err)
			}
		}
	}
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
	s.webUI.Routes(s.mux)

	s.mux.HandleFunc("/api/export", s.jsonAPI.HandleExport)
	s.mux.HandleFunc("/api/projects", s.jsonAPI.HandleProjects)
	s.mux.HandleFunc("/api/projects/discover", s.jsonAPI.HandleDiscover)
	s.mux.HandleFunc("/api/projects/", s.jsonAPI.HandleProject)

	s.mux.HandleFunc("/api/plans", s.jsonAPI.HandlePlans)
	s.mux.HandleFunc("/api/plans/", s.jsonAPI.HandlePlan)
	s.mux.HandleFunc("/api/feedback", s.jsonAPI.HandleFeedbacks)

	s.mux.HandleFunc("/api/specs", s.jsonAPI.HandleSpecs)
	s.mux.HandleFunc("/api/specs/", s.jsonAPI.HandleSpec)

	s.mux.HandleFunc("/api/workflows", s.jsonAPI.HandleWorkflows)
	s.mux.HandleFunc("/api/workflows/", s.handleWorkflow)
	s.mux.HandleFunc("/api/phases/", s.handlePhase)
	s.mux.HandleFunc("/api/settings", s.jsonAPI.HandleSettings)
	s.mux.HandleFunc("/api/profiles", s.jsonAPI.HandleProfiles)
	s.mux.HandleFunc("/api/profiles/", s.jsonAPI.HandleProfile)
	s.mux.HandleFunc("/api/cerberus/sessions", s.handleCerberusSessions)
	s.mux.HandleFunc("/api/cerberus/sessions/", s.handleCerberusSession)
	s.mux.HandleFunc("/api/cerberus/events", s.handleCerberusCallback)
	s.mux.HandleFunc("/api/spec-drafts", s.jsonAPI.HandleSpecDrafts)
	s.mux.HandleFunc("/api/spec-drafts/", s.handleSpecDraft)
	s.mux.HandleFunc("/api/chat/sessions", s.jsonAPI.HandleChatSessions)
	s.mux.HandleFunc("/api/chat/sessions/", s.handleChatSessionRoute)
}
