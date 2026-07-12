package webui

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
)

type Handler struct {
	pool                *pgxpool.Pool
	runner              interface{ Start(int64) }
	cerb                *cerberus.Client
	defaultBudget       float64
	cfgPath             string
	runtimeSettings     func() (string, string)
	loadRuntimeSettings func(context.Context) (map[string]string, error)
}

type Config struct {
	DefaultBudget       float64
	ConfigPath          string
	RuntimeSettings     func() (string, string)
	LoadRuntimeSettings func(context.Context) (map[string]string, error)
}

func New(pool *pgxpool.Pool, runner interface{ Start(int64) }, cerb *cerberus.Client, cfg Config) *Handler {
	return &Handler{
		pool:                pool,
		runner:              runner,
		cerb:                cerb,
		defaultBudget:       cfg.DefaultBudget,
		cfgPath:             cfg.ConfigPath,
		runtimeSettings:     cfg.RuntimeSettings,
		loadRuntimeSettings: cfg.LoadRuntimeSettings,
	}
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleUIShell)
	// Legacy spec/backlog URLs now converge on the canonical plans UI.
	mux.HandleFunc("/backlog", h.redirectToPlans)
	mux.HandleFunc("/backlog/", h.redirectToPlans)
	mux.HandleFunc("/projects", h.handleUIProjectsPage)
	mux.HandleFunc("/projects/fragment", h.handleUIProjectsFragment)
	mux.HandleFunc("/projects/", h.handleUIProject)
	mux.HandleFunc("/settings", h.handleUISettingsPage)
	mux.HandleFunc("/settings/fragment", h.handleUISettingsFragment)
	mux.HandleFunc("/specs/", h.redirectToPlans)
	mux.HandleFunc("/workflows/", h.handleUIWorkflow)
	mux.HandleFunc("/phases/", h.handleUIPhase)
	mux.HandleFunc("/spec-builder", h.handleUISpecBuilderPage)
	mux.HandleFunc("/spec-builder/fragment", h.handleUISpecBuilderStartFragment)
	mux.HandleFunc("/spec-builder/", h.handleUISpecBuilder)
	mux.HandleFunc("/chat", h.handleUIChatPage)
	mux.HandleFunc("/chat/fragment", h.handleUIChatFragment)
	mux.HandleFunc("/chat/", h.handleUIChat)
	mux.HandleFunc("/plans", h.handleUIPlansPage)
	mux.HandleFunc("/plans/fragment", h.handleUIPlansFragment)
	mux.HandleFunc("/plans/", h.handleUIPlan)
}
