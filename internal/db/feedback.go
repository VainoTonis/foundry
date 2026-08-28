package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const feedbackColumns = `id, body, model, session_id, processed, dimension, target, score, tags,
	evidence, impact, recommended_action, owner, status, created_at, scope_status, agent_session_id`

func scanFeedback(row interface {
	Scan(dest ...interface{}) error
}) (Feedback, error) {
	var f Feedback
	var body, model, sessionID *string
	err := row.Scan(&f.ID, &body, &model, &sessionID, &f.Processed, &f.Dimension, &f.Target, &f.Score, &f.Tags,
		&f.Evidence, &f.Impact, &f.RecommendedAction, &f.Owner, &f.Status, &f.CreatedAt, &f.ScopeStatus, &f.AgentSessionID)
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

// lookupAgentSessionIDBySourceSessionID resolves sessionID to the id of the
// matching agent_sessions row (by source_session_id), for best-effort
// attribution of a feedback row to the agent session it was submitted
// from. It returns nil, nil (never an error) when sessionID is empty or
// does not match any agent_sessions row, so callers can always proceed
// with feedback creation regardless of the lookup outcome.
func lookupAgentSessionIDBySourceSessionID(ctx context.Context, q querier, sessionID string) (*int64, error) {
	if sessionID == "" {
		return nil, nil
	}
	s, err := GetAgentSessionBySourceSessionID(ctx, q, sessionID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s.ID, nil
}

// loadFeedbackRepositories returns the (unordered) repository membership
// for a single feedback row, resolving each repository_id to its Repository
// fields in the same query (no N+1 per repository).
func loadFeedbackRepositories(ctx context.Context, q querier, feedbackID int64) ([]FeedbackRepository, error) {
	rows, err := q.Query(ctx,
		`SELECT fr.repository_id, proj.name, proj.local_path, proj.remote_url, proj.created_at
		 FROM feedback_repositories fr JOIN repositories proj ON proj.id = fr.repository_id
		 WHERE fr.feedback_id = $1 ORDER BY fr.repository_id`, feedbackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedbackRepository
	for rows.Next() {
		var fr FeedbackRepository
		if err := rows.Scan(&fr.RepositoryID, &fr.Repository.Name, &fr.Repository.LocalPath, &fr.Repository.RemoteURL, &fr.Repository.CreatedAt); err != nil {
			return nil, err
		}
		fr.Repository.ID = fr.RepositoryID
		out = append(out, fr)
	}
	return out, rows.Err()
}

// loadFeedbackRepositoriesForFeedbacks returns the repository membership
// for every feedback id in feedbackIDs, grouped by feedback id, in a
// single query (avoiding an N+1 query per feedback row for callers like
// ListFeedback).
func loadFeedbackRepositoriesForFeedbacks(ctx context.Context, q querier, feedbackIDs []int64) (map[int64][]FeedbackRepository, error) {
	out := make(map[int64][]FeedbackRepository, len(feedbackIDs))
	if len(feedbackIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx,
		`SELECT fr.feedback_id, fr.repository_id, proj.name, proj.local_path, proj.remote_url, proj.created_at
		 FROM feedback_repositories fr JOIN repositories proj ON proj.id = fr.repository_id
		 WHERE fr.feedback_id = ANY($1) ORDER BY fr.feedback_id, fr.repository_id`, feedbackIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var feedbackID int64
		var fr FeedbackRepository
		if err := rows.Scan(&feedbackID, &fr.RepositoryID, &fr.Repository.Name, &fr.Repository.LocalPath, &fr.Repository.RemoteURL, &fr.Repository.CreatedAt); err != nil {
			return nil, err
		}
		fr.Repository.ID = fr.RepositoryID
		out[feedbackID] = append(out[feedbackID], fr)
	}
	return out, rows.Err()
}

// insertFeedbackRepositories inserts one feedback_repositories row per id
// in repositoryIDs, rejecting an empty list or a duplicate id up front
// (before any insert), and translating a foreign_key_violation into an
// error wrapping ErrNotFound. Callers must run this inside a transaction
// so a failure leaves no feedback_repositories rows behind.
func insertFeedbackRepositories(ctx context.Context, tx pgx.Tx, feedbackID int64, repositoryIDs []int64) error {
	if len(repositoryIDs) == 0 {
		return fmt.Errorf("create feedback: at least one repository id is required")
	}
	seen := make(map[int64]bool, len(repositoryIDs))
	for _, id := range repositoryIDs {
		if seen[id] {
			return fmt.Errorf("create feedback: repository %d listed more than once", id)
		}
		seen[id] = true
	}
	for _, repositoryID := range repositoryIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO feedback_repositories (feedback_id, repository_id) VALUES ($1, $2)`,
			feedbackID, repositoryID,
		); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				switch pgErr.Code {
				case "23503": // foreign_key_violation
					return fmt.Errorf("create feedback: repository %d does not exist: %w", repositoryID, ErrNotFound)
				case "23505": // unique_violation
					return fmt.Errorf("create feedback: repository %d listed more than once", repositoryID)
				}
			}
			return fmt.Errorf("create feedback: insert feedback_repositories for repository %d: %w", repositoryID, err)
		}
	}
	return nil
}

// CreateFeedback creates a legacy free-form feedback row (body/model/session_id)
// scoped to the given non-empty, duplicate-free list of repository ids, and
// its feedback_repositories rows, all in a single transaction. It returns
// an error and leaves no rows persisted if repositoryIDs is empty,
// contains a duplicate id, or contains an id that does not exist in
// repositories.
func CreateFeedback(ctx context.Context, pool *pgxpool.Pool, body, model, sessionID string, repositoryIDs []int64) (Feedback, error) {
	if len(repositoryIDs) == 0 {
		return Feedback{}, fmt.Errorf("create feedback: at least one repository id is required")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Feedback{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	agentSessionID, err := lookupAgentSessionIDBySourceSessionID(ctx, tx, sessionID)
	if err != nil {
		return Feedback{}, err
	}

	f, err := scanFeedback(tx.QueryRow(ctx,
		`INSERT INTO feedback (body, model, session_id, scope_status, agent_session_id) VALUES ($1, $2, $3, 'linked', $4) RETURNING `+feedbackColumns,
		body, model, sessionID, agentSessionID,
	))
	if err != nil {
		return Feedback{}, err
	}

	if err := insertFeedbackRepositories(ctx, tx, f.ID, repositoryIDs); err != nil {
		return Feedback{}, err
	}

	repos, err := loadFeedbackRepositories(ctx, tx, f.ID)
	if err != nil {
		return Feedback{}, err
	}
	f.Repositories = repos

	if err := tx.Commit(ctx); err != nil {
		return Feedback{}, err
	}
	return f, nil
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

// CreateStructuredFeedback creates a structured, per-dimension feedback
// row scoped to the given non-empty, duplicate-free list of repository
// ids, and its feedback_repositories rows, all in a single transaction.
// It returns an error and leaves no rows persisted if repositoryIDs is
// empty, contains a duplicate id, or contains an id that does not exist
// in repositories.
func CreateStructuredFeedback(ctx context.Context, pool *pgxpool.Pool, in StructuredFeedbackInput, repositoryIDs []int64) (Feedback, error) {
	if len(repositoryIDs) == 0 {
		return Feedback{}, fmt.Errorf("create feedback: at least one repository id is required")
	}

	status := in.Status
	if status == "" {
		status = "open"
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Feedback{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	agentSessionID, err := lookupAgentSessionIDBySourceSessionID(ctx, tx, in.SessionID)
	if err != nil {
		return Feedback{}, err
	}

	f, err := scanFeedback(tx.QueryRow(ctx,
		`INSERT INTO feedback (body, model, session_id, dimension, target, score, tags, evidence, impact, recommended_action, owner, status, scope_status, agent_session_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'linked', $13)
		 RETURNING `+feedbackColumns,
		in.Body, nullableString(in.Model), nullableString(in.SessionID), nullableString(in.Dimension), nullableString(in.Target),
		nullableInt(in.Score), in.Tags, nullableString(in.Evidence), nullableString(in.Impact),
		nullableString(in.RecommendedAction), nullableString(in.Owner), status, agentSessionID,
	))
	if err != nil {
		return Feedback{}, err
	}

	if err := insertFeedbackRepositories(ctx, tx, f.ID, repositoryIDs); err != nil {
		return Feedback{}, err
	}

	repos, err := loadFeedbackRepositories(ctx, tx, f.ID)
	if err != nil {
		return Feedback{}, err
	}
	f.Repositories = repos

	if err := tx.Commit(ctx); err != nil {
		return Feedback{}, err
	}
	return f, nil
}

func ListFeedback(ctx context.Context, pool *pgxpool.Pool) ([]Feedback, error) {
	rows, err := pool.Query(ctx, `SELECT `+feedbackColumns+` FROM feedback ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	var out []Feedback
	var ids []int64
	for rows.Next() {
		f, err := scanFeedback(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, f)
		ids = append(ids, f.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	repoByFeedback, err := loadFeedbackRepositoriesForFeedbacks(ctx, pool, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Repositories = repoByFeedback[out[i].ID]
	}
	return out, nil
}

// ListFeedbackFiltered lists feedback rows optionally filtered by
// dimension, session_id, status, and repositoryID (0 means no filter on
// repositoryID). When repositoryID is non-zero only feedback rows with a
// matching feedback_repositories row are returned, so legacy_unscoped
// rows (which have no feedback_repositories rows at all) are excluded.
func ListFeedbackFiltered(ctx context.Context, pool *pgxpool.Pool, dimension, sessionID, status string, repositoryID int64) ([]Feedback, error) {
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
	if repositoryID != 0 {
		args = append(args, repositoryID)
		query += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM feedback_repositories fr WHERE fr.feedback_id = feedback.id AND fr.repository_id = $%d)", len(args))
	}
	query += " ORDER BY id DESC"

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var out []Feedback
	var ids []int64
	for rows.Next() {
		f, err := scanFeedback(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, f)
		ids = append(ids, f.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	repoByFeedback, err := loadFeedbackRepositoriesForFeedbacks(ctx, pool, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Repositories = repoByFeedback[out[i].ID]
	}
	return out, nil
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
