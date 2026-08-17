package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const feedbackColumns = `id, body, model, session_id, processed, dimension, target, score, tags,
	evidence, impact, recommended_action, owner, status, created_at`

func scanFeedback(row interface {
	Scan(dest ...interface{}) error
}) (Feedback, error) {
	var f Feedback
	var body, model, sessionID *string
	err := row.Scan(&f.ID, &body, &model, &sessionID, &f.Processed, &f.Dimension, &f.Target, &f.Score, &f.Tags,
		&f.Evidence, &f.Impact, &f.RecommendedAction, &f.Owner, &f.Status, &f.CreatedAt)
	if body != nil {
		f.Body = *body
	}
	if model != nil {
		f.Model = *model
	}
	if sessionID != nil {
		f.SessionID = *sessionID
	}
	return f, err
}

// CreateFeedback creates a legacy free-form feedback row (body/model/session_id).
func CreateFeedback(ctx context.Context, pool *pgxpool.Pool, body, model, sessionID string) (Feedback, error) {
	row := pool.QueryRow(ctx,
		`INSERT INTO feedback (body, model, session_id) VALUES ($1, $2, $3) RETURNING `+feedbackColumns,
		body, model, sessionID,
	)
	return scanFeedback(row)
}

// StructuredFeedbackInput represents a per-dimension session feedback entry.
type StructuredFeedbackInput struct {
	Body              string
	Model             string
	SessionID         string
	Dimension         string
	Target            string
	Score             int
	Tags              []string
	Evidence          string
	Impact            string
	RecommendedAction string
	Owner             string
	Status            string
}

// CreateStructuredFeedback creates a structured, per-dimension feedback row.
func CreateStructuredFeedback(ctx context.Context, pool *pgxpool.Pool, in StructuredFeedbackInput) (Feedback, error) {
	status := in.Status
	if status == "" {
		status = "open"
	}
	row := pool.QueryRow(ctx,
		`INSERT INTO feedback (body, model, session_id, dimension, target, score, tags, evidence, impact, recommended_action, owner, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING `+feedbackColumns,
		in.Body, nullableString(in.Model), nullableString(in.SessionID), nullableString(in.Dimension), nullableString(in.Target),
		nullableInt(in.Score), in.Tags, nullableString(in.Evidence), nullableString(in.Impact),
		nullableString(in.RecommendedAction), nullableString(in.Owner), status,
	)
	return scanFeedback(row)
}

func ListFeedback(ctx context.Context, pool *pgxpool.Pool) ([]Feedback, error) {
	rows, err := pool.Query(ctx, `SELECT `+feedbackColumns+` FROM feedback ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Feedback
	for rows.Next() {
		f, err := scanFeedback(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListFeedbackFiltered lists feedback rows optionally filtered by dimension, session_id, and status.
func ListFeedbackFiltered(ctx context.Context, pool *pgxpool.Pool, dimension, sessionID, status string) ([]Feedback, error) {
	query := `SELECT ` + feedbackColumns + ` FROM feedback WHERE 1=1`
	args := []interface{}{}
	if dimension != "" {
		args = append(args, dimension)
		query += fmt.Sprintf(" AND dimension = $%d", len(args))
	}
	if sessionID != "" {
		args = append(args, sessionID)
		query += fmt.Sprintf(" AND session_id = $%d", len(args))
	}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}
	query += " ORDER BY id DESC"

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Feedback
	for rows.Next() {
		f, err := scanFeedback(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FeedbackLifecycleUpdate holds the fields updatable via PATCH lifecycle transitions.
type FeedbackLifecycleUpdate struct {
	Status            string
	Owner             *string
	RecommendedAction *string
}

// UpdateFeedbackLifecycle applies lifecycle updates (status/owner/recommended_action) to a feedback row.
func UpdateFeedbackLifecycle(ctx context.Context, pool *pgxpool.Pool, id int64, upd FeedbackLifecycleUpdate) (Feedback, error) {
	row := pool.QueryRow(ctx,
		`UPDATE feedback SET
			status = $1,
			owner = COALESCE($2, owner),
			recommended_action = COALESCE($3, recommended_action)
		 WHERE id = $4
		 RETURNING `+feedbackColumns,
		upd.Status, upd.Owner, upd.RecommendedAction, id,
	)
	return scanFeedback(row)
}

func nullableInt(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}
