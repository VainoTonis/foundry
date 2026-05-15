package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/api"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/config"
	"github.com/tonis2/foundry/internal/hub"
	"github.com/tonis2/foundry/internal/workflow"
)

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// run migrations
	migrationsPath, err := filepath.Abs("migrations")
	if err != nil {
		log.Fatalf("migrations path: %v", err)
	}
	m, err := migrate.New("file:///"+migrationsPath, cfg.DBURL)
	if err != nil {
		log.Fatalf("migrate init: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("migrate up: %v", err)
	}

	// db pool
	pool, err := pgxpool.New(context.Background(), cfg.DBURL)
	if err != nil {
		log.Fatalf("db pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		log.Fatalf("db ping: %v", err)
	}

	// cerberus client — profile is resolved per-session by the runner; pass empty here
	cerb := cerberus.New(cfg.CerberusBin, cfg.CerberusImage, cfg.CerberusModel, "")

	// shared event hub for real-time streaming
	eventHub := hub.New()

	// workflow runner
	runnerCfg := workflow.Config{
		DefaultPhaseTimeoutSeconds: cfg.DefaultPhaseTimeoutSeconds,
		DefaultWorkflowBudgetUSD:   cfg.DefaultWorkflowBudgetUSD,
		MaxConcurrentWorkflows:     cfg.MaxConcurrentWorkflows,
		CerberusProfile:            cfg.CerberusProfile,
	}
	runner := workflow.NewRunner(pool, cerb, runnerCfg, eventHub)

	// orphan draft recovery (non-blocking)
	go api.RecoverOrphanDrafts(context.Background(), pool, cerb)

	// API server
	srv := api.NewServer(pool, runner, cerb, eventHub, cfg.DefaultWorkflowBudgetUSD, cfg.GitRoot, cfgPath, cfg.CerberusProfile, cfg.ServerPort)

	// serve web static files
	mux := http.NewServeMux()
	mux.Handle("/api/", srv)
	mux.Handle("/", noCacheMiddleware(http.FileServer(http.Dir("web"))))

	addr := fmt.Sprintf(":%d", cfg.ServerPort)
	log.Printf("foundry listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func noCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
