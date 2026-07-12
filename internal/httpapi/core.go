package httpapi

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/authoring"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
)

type ChatService interface {
	CreateSession(context.Context, string) (db.ChatSession, error)
	GetSession(context.Context, int64) (db.ChatSession, error)
	ListSessions(context.Context) ([]db.ChatSession, error)
	ListMessages(context.Context, int64) ([]db.ChatMessage, error)
	SendMessageWithProfile(context.Context, int64, string, *string) error
	SuspendSession(context.Context, int64) error
	UpdateSessionProfile(context.Context, int64, string) error
	DeleteSession(context.Context, int64) error
	AttachProject(context.Context, int64, int64) error
	DetachProject(context.Context, int64, int64) error
	ListSessionProjects(context.Context, int64) ([]db.Project, error)
}

type Config struct {
	GitRoot             func() string
	ConfigPath          string
	LoadRuntimeSettings func(context.Context) (map[string]string, error)
	UpdateRuntime       func(map[string]string)
	WorkflowRunner      interface {
		Start(int64)
		Stop(int64)
	}
	DefaultBudget          float64
	SpecDraftsService      func() *authoring.Service
	ChatService            func() ChatService
	Cerberus               *cerberus.Client
	ProjectRepoForWorkflow func(context.Context, int64) (string, error)
	RemoveProfileFile      func(string)
}

type Handler struct {
	pool                *pgxpool.Pool
	gitRoot             func() string
	configPath          string
	loadRuntimeSettings func(context.Context) (map[string]string, error)
	updateRuntime       func(map[string]string)
	workflowRunner      interface {
		Start(int64)
		Stop(int64)
	}
	defaultBudget          float64
	specDraftsService      func() *authoring.Service
	chatSvc                func() ChatService
	cerb                   *cerberus.Client
	projectRepoForWorkflow func(context.Context, int64) (string, error)
	removeProfileFile      func(string)
}

func New(pool *pgxpool.Pool, cfg Config) *Handler {
	return &Handler{
		pool:                   pool,
		gitRoot:                cfg.GitRoot,
		configPath:             cfg.ConfigPath,
		loadRuntimeSettings:    cfg.LoadRuntimeSettings,
		updateRuntime:          cfg.UpdateRuntime,
		workflowRunner:         cfg.WorkflowRunner,
		defaultBudget:          cfg.DefaultBudget,
		specDraftsService:      cfg.SpecDraftsService,
		chatSvc:                cfg.ChatService,
		cerb:                   cfg.Cerberus,
		projectRepoForWorkflow: cfg.ProjectRepoForWorkflow,
		removeProfileFile:      cfg.RemoveProfileFile,
	}
}
