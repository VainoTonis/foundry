package httpapi

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/authoring"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
	"github.com/tonis2/foundry/internal/review"
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
	AttachRepository(context.Context, int64, int64) error
	DetachRepository(context.Context, int64, int64) error
	ListSessionRepositories(context.Context, int64) ([]repository.Repository, error)
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
	DefaultBudget                  float64
	SpecDraftsService              func() *authoring.Service
	ChatService                    func() ChatService
	Cerberus                       *cerberus.Client
	RepositoryLocalPathForWorkflow func(context.Context, int64) (string, error)
	RemoveProfileFile              func(string)
	// ReviewRunner executes one bounded Steward plan review. If nil,
	// review create/list/detail endpoints report the feature as not
	// configured rather than failing with an internal error.
	ReviewRunner   ReviewRunner
	ReviewContract review.ContractSource
	ReviewModel    string
	ReviewTimeout  time.Duration
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
	defaultBudget                  float64
	specDraftsService              func() *authoring.Service
	chatSvc                        func() ChatService
	cerb                           *cerberus.Client
	repositoryLocalPathForWorkflow func(context.Context, int64) (string, error)
	removeProfileFile              func(string)
	reviewRunner                   ReviewRunner
	reviewContract                 review.ContractSource
	reviewModel                    string
	reviewTimeout                  time.Duration
}

func New(pool *pgxpool.Pool, cfg Config) *Handler {
	return &Handler{
		pool:                           pool,
		gitRoot:                        cfg.GitRoot,
		configPath:                     cfg.ConfigPath,
		loadRuntimeSettings:            cfg.LoadRuntimeSettings,
		updateRuntime:                  cfg.UpdateRuntime,
		workflowRunner:                 cfg.WorkflowRunner,
		defaultBudget:                  cfg.DefaultBudget,
		specDraftsService:              cfg.SpecDraftsService,
		chatSvc:                        cfg.ChatService,
		cerb:                           cfg.Cerberus,
		repositoryLocalPathForWorkflow: cfg.RepositoryLocalPathForWorkflow,
		removeProfileFile:              cfg.RemoveProfileFile,
		reviewRunner:                   cfg.ReviewRunner,
		reviewContract:                 cfg.ReviewContract,
		reviewModel:                    cfg.ReviewModel,
		reviewTimeout:                  cfg.ReviewTimeout,
	}
}
