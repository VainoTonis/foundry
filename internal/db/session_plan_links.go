package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SessionPlanLink records how an agent session got attributed to a plan
// (and optionally a specific plan step), and with what confidence. See
// migration 040_session_plan_links for the underlying table shape and
// constraints.
type SessionPlanLink struct {
	ID             int64     `json:"id"`
	AgentSessionID int64     `json:"agent_session_id"`
	PlanID         int64     `json:"plan_id"`
	PlanStepID     *int64    `json:"plan_step_id,omitempty"`
	Method         string    `json:"method"`
	Confidence     *float64  `json:"confidence,omitempty"`
	Note           *string   `json:"note,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Session plan link attribution methods, matching the
// session_plan_links_method_check constraint added in migration 040.
const (
	SessionPlanLinkMethodSystemDerived = "system_derived"
	SessionPlanLinkMethodExplicit      = "explicit"
	SessionPlanLinkMethodAPIInferred   = "api_inferred"
	SessionPlanLinkMethodHeuristic     = "heuristic"
)

const sessionPlanLinkSelectColumns = `id, agent_session_id, plan_id, plan_step_id, method, confidence, note, created_at`

func scanSessionPlanLink(row pgx.Row) (SessionPlanLink, error) {
	var l SessionPlanLink
	err := row.Scan(&l.ID, &l.AgentSessionID, &l.PlanID, &l.PlanStepID, &l.Method, &l.Confidence, &l.Note, &l.CreatedAt)
	return l, err
}

// CreateSessionPlanLinkParams holds the fields needed to insert a new
// session_plan_links row. PlanStepID, Confidence, and Note are all
// optional; Method must be one of the values accepted by
// session_plan_links_method_check.
type CreateSessionPlanLinkParams struct {
	AgentSessionID int64
	PlanID         int64
	PlanStepID     *int64
	Method         string
	Confidence     *float64
	Note           *string
}

// CreateSessionPlanLink inserts a new session_plan_links row linking
// p.AgentSessionID to p.PlanID (and optionally p.PlanStepID). It fails if
// p.Method is empty, or if the referenced agent session, plan, or plan
// step does not exist (in which case the returned error wraps
// ErrNotFound).
func CreateSessionPlanLink(ctx context.Context, pool querier, p CreateSessionPlanLinkParams) (SessionPlanLink, error) {
	if p.Method == "" {
		return SessionPlanLink{}, fmt.Errorf("create session plan link: method is required")
	}

	row := pool.QueryRow(ctx,
		`INSERT INTO session_plan_links (agent_session_id, plan_id, plan_step_id, method, confidence, note)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+sessionPlanLinkSelectColumns,
		p.AgentSessionID, p.PlanID, p.PlanStepID, p.Method, p.Confidence, p.Note,
	)
	l, err := scanSessionPlanLink(row)
	if err != nil {
		if isForeignKeyViolation(err) {
			return SessionPlanLink{}, fmt.Errorf("create session plan link: %w", ErrNotFound)
		}
		return SessionPlanLink{}, err
	}
	return l, nil
}

// ListSessionPlanLinksByPlan returns every session_plan_links row for
// planID, most recently created first.
func ListSessionPlanLinksByPlan(ctx context.Context, pool querier, planID int64) ([]SessionPlanLink, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+sessionPlanLinkSelectColumns+` FROM session_plan_links WHERE plan_id = $1 ORDER BY id DESC`,
		planID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionPlanLink
	for rows.Next() {
		l, err := scanSessionPlanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
