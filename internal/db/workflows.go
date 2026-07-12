package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func CreateWorkflow(ctx context.Context, pool *pgxpool.Pool, specID int64, track string, maxCostUSD *float64) (Workflow, error) {
	var w Workflow
	err := pool.QueryRow(ctx,
		`INSERT INTO workflows (spec_id, track, max_cost_usd)
		 VALUES ($1, $2, $3)
		 RETURNING id, spec_id, track, status, max_cost_usd, created_at, finished_at`,
		specID, track, maxCostUSD,
	).Scan(&w.ID, &w.SpecID, &w.Track, &w.Status, &w.MaxCostUSD, &w.CreatedAt, &w.FinishedAt)
	return w, err
}

func GetWorkflow(ctx context.Context, pool *pgxpool.Pool, id int64) (Workflow, error) {
	var w Workflow
	err := pool.QueryRow(ctx,
		`SELECT id, spec_id, track, status, max_cost_usd, created_at, finished_at FROM workflows WHERE id = $1`, id,
	).Scan(&w.ID, &w.SpecID, &w.Track, &w.Status, &w.MaxCostUSD, &w.CreatedAt, &w.FinishedAt)
	if err == pgx.ErrNoRows {
		return w, ErrNotFound
	}
	return w, err
}

func ListWorkflowsBySpec(ctx context.Context, pool *pgxpool.Pool, specID int64) ([]Workflow, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, spec_id, track, status, max_cost_usd, created_at, finished_at FROM workflows WHERE spec_id = $1 ORDER BY id DESC`, specID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		var w Workflow
		if err := rows.Scan(&w.ID, &w.SpecID, &w.Track, &w.Status, &w.MaxCostUSD, &w.CreatedAt, &w.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func UpdateWorkflowStatus(ctx context.Context, pool *pgxpool.Pool, id int64, status string) error {
	var finishedAt *time.Time
	if status == "done" || status == "failed" || status == "paused" {
		now := time.Now()
		finishedAt = &now
	}
	_, err := pool.Exec(ctx,
		`UPDATE workflows SET status = $1, finished_at = $2 WHERE id = $3`,
		status, finishedAt, id,
	)
	return err
}

func DeleteWorkflow(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	tag, err := pool.Exec(ctx, `DELETE FROM workflows WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func WorkflowTotalCost(ctx context.Context, pool *pgxpool.Pool, workflowID int64) (float64, error) {
	var total float64
	err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(cost_usd), 0) FROM phases WHERE workflow_id = $1`, workflowID,
	).Scan(&total)
	return total, err
}
