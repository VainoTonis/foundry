package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func CreateKnowledgeFeedback(ctx context.Context, pool *pgxpool.Pool, kind, notePath, topic, evidence, suggestion, origin string) (KnowledgeFeedback, error) {
	var f KnowledgeFeedback
	err := pool.QueryRow(ctx,
		`INSERT INTO knowledge_feedback (kind, note_path, topic, evidence, suggestion, origin)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, kind, note_path, topic, evidence, suggestion, origin, status, created_at`,
		kind, nullableString(notePath), nullableString(topic), evidence, nullableString(suggestion), origin,
	).Scan(&f.ID, &f.Kind, &f.NotePath, &f.Topic, &f.Evidence, &f.Suggestion, &f.Origin, &f.Status, &f.CreatedAt)
	return f, err
}

func ListKnowledgeFeedback(ctx context.Context, pool *pgxpool.Pool, status, notePath string) ([]KnowledgeFeedback, error) {
	query := `SELECT id, kind, note_path, topic, evidence, suggestion, origin, status, created_at FROM knowledge_feedback WHERE 1=1`
	args := []interface{}{}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if notePath != "" {
		args = append(args, notePath)
		query += fmt.Sprintf(" AND note_path = $%d", len(args))
	}
	query += " ORDER BY id DESC"

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeFeedback
	for rows.Next() {
		var f KnowledgeFeedback
		if err := rows.Scan(&f.ID, &f.Kind, &f.NotePath, &f.Topic, &f.Evidence, &f.Suggestion, &f.Origin, &f.Status, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func UpdateKnowledgeFeedbackStatus(ctx context.Context, pool *pgxpool.Pool, id int64, status string) (KnowledgeFeedback, error) {
	var f KnowledgeFeedback
	err := pool.QueryRow(ctx,
		`UPDATE knowledge_feedback SET status = $1 WHERE id = $2
		 RETURNING id, kind, note_path, topic, evidence, suggestion, origin, status, created_at`,
		status, id,
	).Scan(&f.ID, &f.Kind, &f.NotePath, &f.Topic, &f.Evidence, &f.Suggestion, &f.Origin, &f.Status, &f.CreatedAt)
	return f, err
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
