package db

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	SpecDraftStatusFrozen = "frozen"
	SpecDraftStatusError  = "error"
)

type UpdateSpecDraftParams struct {
	Title                 *string
	Messages              json.RawMessage
	Status                *string
	CerberusSession       *string
	OriginalIntent        *string
	CurrentDecisionNeeded *string
}

type UpdateDraftAttemptParams struct {
	AttemptNumber   *int
	CerberusSession *string
	Status          *string
	Prompt          *string
	Result          *string
	ErrorMessage    *string
}

type UpdateDraftDecisionParams struct {
	Prompt    *string
	Options   json.RawMessage
	Decision  *string
	Rationale *string
	Status    *string
}

func IsSpecDraftSafeToCleanStatus(status string) bool {
	return status == SpecDraftStatusFrozen || status == SpecDraftStatusError
}

func CreateSpecDraft(ctx context.Context, pool *pgxpool.Pool, projectID *int64, title string) (SpecDraft, error) {
	var d SpecDraft
	err := pool.QueryRow(ctx,
		`INSERT INTO spec_drafts (project_id, title) VALUES ($1, $2)
		 RETURNING id, project_id, title, cerberus_session, messages, status, original_intent, current_decision_needed, created_at, updated_at`,
		projectID, title,
	).Scan(&d.ID, &d.ProjectID, &d.Title, &d.CerberusSession, &d.Messages, &d.Status, &d.OriginalIntent, &d.CurrentDecisionNeeded, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func GetSpecDraft(ctx context.Context, pool *pgxpool.Pool, id int64) (SpecDraft, error) {
	var d SpecDraft
	err := pool.QueryRow(ctx,
		`SELECT id, project_id, title, cerberus_session, messages, status, original_intent, current_decision_needed, created_at, updated_at FROM spec_drafts WHERE id = $1`, id,
	).Scan(&d.ID, &d.ProjectID, &d.Title, &d.CerberusSession, &d.Messages, &d.Status, &d.OriginalIntent, &d.CurrentDecisionNeeded, &d.CreatedAt, &d.UpdatedAt)
	if err == pgx.ErrNoRows {
		return d, ErrNotFound
	}
	return d, err
}

func ListSpecDrafts(ctx context.Context, pool *pgxpool.Pool) ([]SpecDraft, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, project_id, title, cerberus_session, messages, status, original_intent, current_decision_needed, created_at, updated_at FROM spec_drafts ORDER BY id DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpecDraft
	for rows.Next() {
		var d SpecDraft
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Title, &d.CerberusSession, &d.Messages, &d.Status, &d.OriginalIntent, &d.CurrentDecisionNeeded, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func UpdateSpecDraft(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdateSpecDraftParams) (SpecDraft, error) {
	set := []string{"updated_at = NOW()"}
	args := []any{}
	n := 1
	if p.Title != nil {
		set = append(set, "title = $"+itoa(n))
		args = append(args, *p.Title)
		n++
	}
	if p.CerberusSession != nil {
		set = append(set, "cerberus_session = $"+itoa(n))
		args = append(args, *p.CerberusSession)
		n++
	}
	if p.Messages != nil {
		set = append(set, "messages = $"+itoa(n))
		args = append(args, p.Messages)
		n++
	}
	if p.Status != nil {
		set = append(set, "status = $"+itoa(n))
		args = append(args, *p.Status)
		n++
	}
	if p.OriginalIntent != nil {
		set = append(set, "original_intent = $"+itoa(n))
		args = append(args, *p.OriginalIntent)
		n++
	}
	if p.CurrentDecisionNeeded != nil {
		set = append(set, "current_decision_needed = $"+itoa(n))
		args = append(args, *p.CurrentDecisionNeeded)
		n++
	}
	args = append(args, id)
	q := `UPDATE spec_drafts SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) +
		` RETURNING id, project_id, title, cerberus_session, messages, status, original_intent, current_decision_needed, created_at, updated_at`
	var d SpecDraft
	err := pool.QueryRow(ctx, q, args...).Scan(
		&d.ID, &d.ProjectID, &d.Title, &d.CerberusSession, &d.Messages, &d.Status, &d.OriginalIntent, &d.CurrentDecisionNeeded, &d.CreatedAt, &d.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return d, ErrNotFound
	}
	return d, err
}

func DeleteSpecDraft(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	_, err := pool.Exec(ctx, `DELETE FROM spec_drafts WHERE id = $1`, id)
	return err
}

func CreateDraftAttempt(ctx context.Context, pool *pgxpool.Pool, draftID int64, attemptNumber int, prompt string) (DraftAttempt, error) {
	var a DraftAttempt
	err := pool.QueryRow(ctx,
		`INSERT INTO draft_attempts (draft_id, attempt_number, prompt) VALUES ($1, $2, $3)
		 RETURNING id, draft_id, attempt_number, cerberus_session, status, prompt, result, error_message, created_at, updated_at`,
		draftID, attemptNumber, prompt,
	).Scan(&a.ID, &a.DraftID, &a.AttemptNumber, &a.CerberusSession, &a.Status, &a.Prompt, &a.Result, &a.ErrorMessage, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func GetDraftAttempt(ctx context.Context, pool *pgxpool.Pool, id int64) (DraftAttempt, error) {
	var a DraftAttempt
	err := pool.QueryRow(ctx,
		`SELECT id, draft_id, attempt_number, cerberus_session, status, prompt, result, error_message, created_at, updated_at FROM draft_attempts WHERE id = $1`, id,
	).Scan(&a.ID, &a.DraftID, &a.AttemptNumber, &a.CerberusSession, &a.Status, &a.Prompt, &a.Result, &a.ErrorMessage, &a.CreatedAt, &a.UpdatedAt)
	if err == pgx.ErrNoRows {
		return a, ErrNotFound
	}
	return a, err
}

func ListDraftAttemptsByDraft(ctx context.Context, pool *pgxpool.Pool, draftID int64) ([]DraftAttempt, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, draft_id, attempt_number, cerberus_session, status, prompt, result, error_message, created_at, updated_at FROM draft_attempts WHERE draft_id = $1 ORDER BY attempt_number, id`,
		draftID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DraftAttempt
	for rows.Next() {
		var a DraftAttempt
		if err := rows.Scan(&a.ID, &a.DraftID, &a.AttemptNumber, &a.CerberusSession, &a.Status, &a.Prompt, &a.Result, &a.ErrorMessage, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func UpdateDraftAttempt(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdateDraftAttemptParams) (DraftAttempt, error) {
	set := []string{"updated_at = NOW()"}
	args := []any{}
	n := 1
	if p.AttemptNumber != nil {
		set = append(set, "attempt_number = $"+itoa(n))
		args = append(args, *p.AttemptNumber)
		n++
	}
	if p.CerberusSession != nil {
		set = append(set, "cerberus_session = $"+itoa(n))
		args = append(args, *p.CerberusSession)
		n++
	}
	if p.Status != nil {
		set = append(set, "status = $"+itoa(n))
		args = append(args, *p.Status)
		n++
	}
	if p.Prompt != nil {
		set = append(set, "prompt = $"+itoa(n))
		args = append(args, *p.Prompt)
		n++
	}
	if p.Result != nil {
		set = append(set, "result = $"+itoa(n))
		args = append(args, *p.Result)
		n++
	}
	if p.ErrorMessage != nil {
		set = append(set, "error_message = $"+itoa(n))
		args = append(args, *p.ErrorMessage)
		n++
	}
	args = append(args, id)
	q := `UPDATE draft_attempts SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) +
		` RETURNING id, draft_id, attempt_number, cerberus_session, status, prompt, result, error_message, created_at, updated_at`
	var a DraftAttempt
	err := pool.QueryRow(ctx, q, args...).Scan(&a.ID, &a.DraftID, &a.AttemptNumber, &a.CerberusSession, &a.Status, &a.Prompt, &a.Result, &a.ErrorMessage, &a.CreatedAt, &a.UpdatedAt)
	if err == pgx.ErrNoRows {
		return a, ErrNotFound
	}
	return a, err
}

func CreateDraftAttemptEvent(ctx context.Context, pool *pgxpool.Pool, draftID int64, attemptID *int64, eventType string, payload json.RawMessage) (DraftAttemptEvent, error) {
	var e DraftAttemptEvent
	err := pool.QueryRow(ctx,
		`INSERT INTO draft_attempt_events (draft_id, attempt_id, event_type, payload) VALUES ($1, $2, $3, $4)
		 RETURNING id, draft_id, attempt_id, event_type, payload, created_at`,
		draftID, attemptID, eventType, payload,
	).Scan(&e.ID, &e.DraftID, &e.AttemptID, &e.EventType, &e.Payload, &e.CreatedAt)
	return e, err
}

func ListDraftAttemptEventsByDraft(ctx context.Context, pool *pgxpool.Pool, draftID int64) ([]DraftAttemptEvent, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, draft_id, attempt_id, event_type, payload, created_at FROM draft_attempt_events WHERE draft_id = $1 ORDER BY id`,
		draftID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDraftAttemptEvents(rows)
}

func ListDraftAttemptEventsByAttempt(ctx context.Context, pool *pgxpool.Pool, attemptID int64) ([]DraftAttemptEvent, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, draft_id, attempt_id, event_type, payload, created_at FROM draft_attempt_events WHERE attempt_id = $1 ORDER BY id`,
		attemptID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDraftAttemptEvents(rows)
}

func scanDraftAttemptEvents(rows pgx.Rows) ([]DraftAttemptEvent, error) {
	var out []DraftAttemptEvent
	for rows.Next() {
		var e DraftAttemptEvent
		if err := rows.Scan(&e.ID, &e.DraftID, &e.AttemptID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func CreateDraftDecision(ctx context.Context, pool *pgxpool.Pool, draftID int64, prompt string, options json.RawMessage) (DraftDecision, error) {
	var d DraftDecision
	err := pool.QueryRow(ctx,
		`INSERT INTO draft_decisions (draft_id, prompt, options) VALUES ($1, $2, $3)
		 RETURNING id, draft_id, prompt, options, decision, rationale, status, created_at, updated_at`,
		draftID, prompt, options,
	).Scan(&d.ID, &d.DraftID, &d.Prompt, &d.Options, &d.Decision, &d.Rationale, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func GetDraftDecision(ctx context.Context, pool *pgxpool.Pool, id int64) (DraftDecision, error) {
	var d DraftDecision
	err := pool.QueryRow(ctx,
		`SELECT id, draft_id, prompt, options, decision, rationale, status, created_at, updated_at FROM draft_decisions WHERE id = $1`, id,
	).Scan(&d.ID, &d.DraftID, &d.Prompt, &d.Options, &d.Decision, &d.Rationale, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if err == pgx.ErrNoRows {
		return d, ErrNotFound
	}
	return d, err
}

func ListDraftDecisionsByDraft(ctx context.Context, pool *pgxpool.Pool, draftID int64) ([]DraftDecision, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, draft_id, prompt, options, decision, rationale, status, created_at, updated_at FROM draft_decisions WHERE draft_id = $1 ORDER BY id`,
		draftID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DraftDecision
	for rows.Next() {
		var d DraftDecision
		if err := rows.Scan(&d.ID, &d.DraftID, &d.Prompt, &d.Options, &d.Decision, &d.Rationale, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func UpdateDraftDecision(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdateDraftDecisionParams) (DraftDecision, error) {
	set := []string{"updated_at = NOW()"}
	args := []any{}
	n := 1
	if p.Prompt != nil {
		set = append(set, "prompt = $"+itoa(n))
		args = append(args, *p.Prompt)
		n++
	}
	if p.Options != nil {
		set = append(set, "options = $"+itoa(n))
		args = append(args, p.Options)
		n++
	}
	if p.Decision != nil {
		set = append(set, "decision = $"+itoa(n))
		args = append(args, *p.Decision)
		n++
	}
	if p.Rationale != nil {
		set = append(set, "rationale = $"+itoa(n))
		args = append(args, *p.Rationale)
		n++
	}
	if p.Status != nil {
		set = append(set, "status = $"+itoa(n))
		args = append(args, *p.Status)
		n++
	}
	args = append(args, id)
	q := `UPDATE draft_decisions SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) +
		` RETURNING id, draft_id, prompt, options, decision, rationale, status, created_at, updated_at`
	var d DraftDecision
	err := pool.QueryRow(ctx, q, args...).Scan(&d.ID, &d.DraftID, &d.Prompt, &d.Options, &d.Decision, &d.Rationale, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if err == pgx.ErrNoRows {
		return d, ErrNotFound
	}
	return d, err
}
