package workflow

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/hub"
)

type Config struct {
	DefaultPhaseTimeoutSeconds int
	DefaultWorkflowBudgetUSD   float64
	MaxConcurrentWorkflows     int
	CerberusProfile            string
	CerberusCallbackURL        string
}

type Runner struct {
	pool    *pgxpool.Pool
	cerb    *cerberus.Client
	cfg     Config
	hub     *hub.EventHub
	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
}
