package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UpdatePhaseParams struct {
	Status            *string
	PromptSent        *string
	CerberusSession   *string
	CerberusCommit    *string
	CostUSD           *float64
	StartedAt         *time.Time
	FinishedAt        *time.Time
	ReviewVerdict     *string
	ReviewNotes       *string
	AdjustedPrompt    *string
	DecisionSummary   *string
	DecisionRationale *string
	FilesTouched      []byte
	PhaseFeedback     []byte
	RetryCount        *int

	// Clear* flags force the corresponding column to NULL, distinct from
	// leaving the field untouched (the default when the matching pointer
	// field above is nil). This is needed for retry resets, which must
	// explicitly wipe the previous attempt's outcome fields rather than
	// simply not mentioning them.
	ClearStartedAt     bool
	ClearFinishedAt    bool
	ClearReviewVerdict bool
	ClearCerberusCommit bool
	ClearPhaseFeedback  bool
}

func CreatePhase(ctx context.Context, pool *pgxpool.Pool, workflowID int64, position int, name, goal string, timeoutSeconds int) (Phase, error) {
	return CreatePhaseWithParallelGroup(ctx, pool, workflowID, position, name, goal, timeoutSeconds, nil)
}

func CreatePhaseWithParallelGroup(ctx context.Context, pool *pgxpool.Pool, workflowID int64, position int, name, goal string, timeoutSeconds int, parallelGroup *int) (Phase, error) {
	var ph Phase
	err := pool.QueryRow(ctx,
		`INSERT INTO phases (workflow_id, position, name, goal, timeout_seconds, parallel_group)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, workflow_id, position, parallel_group, name, goal, prompt_sent, status, retry_count,
		           timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		           started_at, finished_at, review_verdict, review_notes,
		           adjusted_prompt, decision_summary, decision_rationale, files_touched, phase_feedback`,
		workflowID, position, name, goal, timeoutSeconds, parallelGroup,
	).Scan(phaseScans(&ph)...)
	return ph, err
}

func GetPhase(ctx context.Context, pool *pgxpool.Pool, id int64) (Phase, error) {
	var ph Phase
	err := pool.QueryRow(ctx,
		`SELECT id, workflow_id, position, parallel_group, name, goal, prompt_sent, status, retry_count,
		        timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		        started_at, finished_at, review_verdict, review_notes,
		        adjusted_prompt, decision_summary, decision_rationale, files_touched, phase_feedback
		 FROM phases WHERE id = $1`, id,
	).Scan(phaseScans(&ph)...)
	if err == pgx.ErrNoRows {
		return ph, ErrNotFound
	}
	return ph, err
}

func GetPhaseByCerberusSession(ctx context.Context, pool *pgxpool.Pool, session string) (Phase, error) {
	var ph Phase
	err := pool.QueryRow(ctx,
		`SELECT id, workflow_id, position, parallel_group, name, goal, prompt_sent, status, retry_count,
		        timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		        started_at, finished_at, review_verdict, review_notes,
		        adjusted_prompt, decision_summary, decision_rationale, files_touched, phase_feedback
		 FROM phases WHERE cerberus_session = $1 ORDER BY id DESC LIMIT 1`, session,
	).Scan(phaseScans(&ph)...)
	if err == pgx.ErrNoRows {
		return ph, ErrNotFound
	}
	return ph, err
}

func ListPhasesByWorkflow(ctx context.Context, pool *pgxpool.Pool, workflowID int64) ([]Phase, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, workflow_id, position, parallel_group, name, goal, prompt_sent, status, retry_count,
		        timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		        started_at, finished_at, review_verdict, review_notes,
		        adjusted_prompt, decision_summary, decision_rationale, files_touched, phase_feedback
		 FROM phases WHERE workflow_id = $1 ORDER BY position`, workflowID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Phase
	for rows.Next() {
		var ph Phase
		if err := rows.Scan(phaseScans(&ph)...); err != nil {
			return nil, err
		}
		out = append(out, ph)
	}
	return out, rows.Err()
}

func UpdatePhase(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdatePhaseParams) (Phase, error) {
	set := []string{}
	args := []any{}
	n := 1
	maybeStr := func(field string, v *string, clear bool) {
		if clear {
			set = append(set, field+" = NULL")
			return
		}
		if v != nil {
			set = append(set, field+" = $"+itoa(n))
			args = append(args, *v)
			n++
		}
	}
	maybeTime := func(field string, v *time.Time, clear bool) {
		if clear {
			set = append(set, field+" = NULL")
			return
		}
		if v != nil {
			set = append(set, field+" = $"+itoa(n))
			args = append(args, *v)
			n++
		}
	}
	maybeStr("status", p.Status, false)
	maybeStr("prompt_sent", p.PromptSent, false)
	maybeStr("cerberus_session", p.CerberusSession, false)
	maybeStr("cerberus_commit", p.CerberusCommit, p.ClearCerberusCommit)
	maybeStr("review_verdict", p.ReviewVerdict, p.ClearReviewVerdict)
	maybeStr("review_notes", p.ReviewNotes, false)
	maybeStr("adjusted_prompt", p.AdjustedPrompt, false)
	maybeStr("decision_summary", p.DecisionSummary, false)
	maybeStr("decision_rationale", p.DecisionRationale, false)
	maybeTime("started_at", p.StartedAt, p.ClearStartedAt)
	maybeTime("finished_at", p.FinishedAt, p.ClearFinishedAt)
	if p.CostUSD != nil {
		set = append(set, "cost_usd = $"+itoa(n))
		args = append(args, *p.CostUSD)
		n++
	}
	if p.FilesTouched != nil {
		set = append(set, "files_touched = $"+itoa(n))
		args = append(args, p.FilesTouched)
		n++
	}
	if p.ClearPhaseFeedback {
		set = append(set, `phase_feedback = '{"result":"","useful_context":[],"problems":[],"confidence":0}'::jsonb`)
	} else if p.PhaseFeedback != nil {
		set = append(set, "phase_feedback = $"+itoa(n))
		args = append(args, p.PhaseFeedback)
		n++
	}
	if p.RetryCount != nil {
		set = append(set, "retry_count = $"+itoa(n))
		args = append(args, *p.RetryCount)
		n++
	}
	if len(set) == 0 {
		return GetPhase(ctx, pool, id)
	}
	args = append(args, id)
	q := `UPDATE phases SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) +
		` RETURNING id, workflow_id, position, parallel_group, name, goal, prompt_sent, status, retry_count,
		            timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		            started_at, finished_at, review_verdict, review_notes,
		            adjusted_prompt, decision_summary, decision_rationale, files_touched, phase_feedback`
	var ph Phase
	err := pool.QueryRow(ctx, q, args...).Scan(phaseScans(&ph)...)
	if err == pgx.ErrNoRows {
		return ph, ErrNotFound
	}
	return ph, err
}

func AddPhaseCost(ctx context.Context, pool *pgxpool.Pool, id int64, deltaUSD float64) error {
	tag, err := pool.Exec(ctx,
		`UPDATE phases SET cost_usd = COALESCE(cost_usd, 0) + $1 WHERE id = $2`,
		deltaUSD, id,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func NextPendingPhase(ctx context.Context, pool *pgxpool.Pool, workflowID int64) (Phase, error) {
	var ph Phase
	err := pool.QueryRow(ctx,
		`SELECT id, workflow_id, position, parallel_group, name, goal, prompt_sent, status, retry_count,
		        timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		        started_at, finished_at, review_verdict, review_notes,
		        adjusted_prompt, decision_summary, decision_rationale, files_touched, phase_feedback
		 FROM phases WHERE workflow_id = $1 AND status = 'pending' ORDER BY position LIMIT 1`, workflowID,
	).Scan(phaseScans(&ph)...)
	if err == pgx.ErrNoRows {
		return ph, ErrNotFound
	}
	return ph, err
}

func phaseScans(ph *Phase) []any {
	return []any{
		&ph.ID, &ph.WorkflowID, &ph.Position, &ph.ParallelGroup, &ph.Name, &ph.Goal,
		&ph.PromptSent, &ph.Status, &ph.RetryCount,
		&ph.TimeoutSeconds, &ph.CerberusSession, &ph.CerberusCommit, &ph.CostUSD,
		&ph.StartedAt, &ph.FinishedAt, &ph.ReviewVerdict, &ph.ReviewNotes,
		&ph.AdjustedPrompt, &ph.DecisionSummary, &ph.DecisionRationale, &ph.FilesTouched, &ph.PhaseFeedback,
	}
}
