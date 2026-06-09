package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func InsertPhaseLog(ctx context.Context, pool *pgxpool.Pool, phaseID int64, line string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO phase_logs (phase_id, line) VALUES ($1, $2)`, phaseID, line,
	)
	return err
}

func ListPhaseLogs(ctx context.Context, pool *pgxpool.Pool, phaseID int64) ([]PhaseLog, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, phase_id, line, ts FROM phase_logs WHERE phase_id = $1 ORDER BY id`, phaseID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PhaseLog
	for rows.Next() {
		var l PhaseLog
		if err := rows.Scan(&l.ID, &l.PhaseID, &l.Line, &l.Ts); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func ListRecentPhaseLogs(ctx context.Context, pool *pgxpool.Pool, phaseID int64, limit int) ([]PhaseLog, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := pool.Query(ctx,
		`SELECT id, phase_id, line, ts FROM (
			SELECT id, phase_id, line, ts FROM phase_logs WHERE phase_id = $1 ORDER BY id DESC LIMIT $2
		) recent ORDER BY id`, phaseID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PhaseLog
	for rows.Next() {
		var l PhaseLog
		if err := rows.Scan(&l.ID, &l.PhaseID, &l.Line, &l.Ts); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func StreamPhaseLogs(ctx context.Context, pool *pgxpool.Pool, phaseID int64, afterID int64) ([]PhaseLog, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, phase_id, line, ts FROM phase_logs WHERE phase_id = $1 AND id > $2 ORDER BY id`,
		phaseID, afterID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PhaseLog
	for rows.Next() {
		var l PhaseLog
		if err := rows.Scan(&l.ID, &l.PhaseID, &l.Line, &l.Ts); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
