package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func CreateFeedback(ctx context.Context, pool *pgxpool.Pool, body, model, sessionID string) (Feedback, error) {
	var f Feedback
	err := pool.QueryRow(ctx,
		`INSERT INTO feedback (body, model, session_id) VALUES ($1, $2, $3) RETURNING id, body, model, session_id, processed, created_at`,
		body, model, sessionID,
	).Scan(&f.ID, &f.Body, &f.Model, &f.SessionID, &f.Processed, &f.CreatedAt)
	return f, err
}

func ListFeedback(ctx context.Context, pool *pgxpool.Pool) ([]Feedback, error) {
	rows, err := pool.Query(ctx, `SELECT id, body, model, session_id, processed, created_at FROM feedback ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Feedback
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.ID, &f.Body, &f.Model, &f.SessionID, &f.Processed, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
