package httpapi

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/authoring"
	"github.com/tonis2/foundry/internal/cerberus"
)

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
		cerb:                   cfg.Cerberus,
		projectRepoForWorkflow: cfg.ProjectRepoForWorkflow,
		removeProfileFile:      cfg.RemoveProfileFile,
	}
}
