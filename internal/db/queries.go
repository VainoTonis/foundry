package db

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Project struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	RepoPath  string    `json:"repo_path"`
	CreatedAt time.Time `json:"created_at"`
}

type Spec struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Track     string    `json:"track"`
	Status    string    `json:"status"`
	ProjectID int64     `json:"project_id"`
	Tags      []byte    `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Workflow struct {
	ID         int64      `json:"id"`
	SpecID     int64      `json:"spec_id"`
	Track      string     `json:"track"`
	Status     string     `json:"status"`
	MaxCostUSD *float64   `json:"max_cost_usd"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

type Phase struct {
	ID                int64      `json:"id"`
	WorkflowID        int64      `json:"workflow_id"`
	Position          int        `json:"position"`
	Name              string     `json:"name"`
	Goal              string     `json:"goal"`
	PromptSent        *string    `json:"prompt_sent"`
	Status            string     `json:"status"`
	RetryCount        int        `json:"retry_count"`
	TimeoutSeconds    int        `json:"timeout_seconds"`
	CerberusSession   *string    `json:"cerberus_session"`
	CerberusCommit    *string    `json:"cerberus_commit"`
	CostUSD           *float64   `json:"cost_usd"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	ReviewVerdict     *string    `json:"review_verdict"`
	ReviewNotes       *string    `json:"review_notes"`
	AdjustedPrompt    *string    `json:"adjusted_prompt"`
	DecisionSummary   *string    `json:"decision_summary"`
	DecisionRationale *string    `json:"decision_rationale"`
	FilesTouched      []byte     `json:"files_touched"`
}

type PhaseLog struct {
	ID      int64     `json:"id"`
	PhaseID int64     `json:"phase_id"`
	Line    string    `json:"line"`
	Ts      time.Time `json:"ts"`
}

// --- Projects ---

func CreateProject(ctx context.Context, pool *pgxpool.Pool, name, repoPath string) (Project, error) {
	var p Project
	err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, repo_path) VALUES ($1, $2) RETURNING id, name, repo_path, created_at`,
		name, repoPath,
	).Scan(&p.ID, &p.Name, &p.RepoPath, &p.CreatedAt)
	return p, err
}

func ListProjects(ctx context.Context, pool *pgxpool.Pool) ([]Project, error) {
	rows, err := pool.Query(ctx, `SELECT id, name, repo_path, created_at FROM projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.RepoPath, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func GetProject(ctx context.Context, pool *pgxpool.Pool, id int64) (Project, error) {
	var p Project
	err := pool.QueryRow(ctx,
		`SELECT id, name, repo_path, created_at FROM projects WHERE id = $1`, id,
	).Scan(&p.ID, &p.Name, &p.RepoPath, &p.CreatedAt)
	if err == pgx.ErrNoRows {
		return p, ErrNotFound
	}
	return p, err
}

// --- Specs ---

func CreateSpec(ctx context.Context, pool *pgxpool.Pool, projectID int64, title, content string, tags []byte) (Spec, error) {
	var s Spec
	err := pool.QueryRow(ctx,
		`INSERT INTO specs (project_id, title, content, tags)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, title, content, track, status, project_id, tags, created_at, updated_at`,
		projectID, title, content, tags,
	).Scan(&s.ID, &s.Title, &s.Content, &s.Track, &s.Status, &s.ProjectID, &s.Tags, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

type ListSpecsFilter struct {
	Status    string
	ProjectID int64
}

func ListSpecs(ctx context.Context, pool *pgxpool.Pool, f ListSpecsFilter) ([]Spec, error) {
	q := `SELECT id, title, content, track, status, project_id, tags, created_at, updated_at FROM specs WHERE 1=1`
	args := []any{}
	n := 1
	if f.Status != "" {
		q += ` AND status = $` + itoa(n)
		args = append(args, f.Status)
		n++
	}
	if f.ProjectID != 0 {
		q += ` AND project_id = $` + itoa(n)
		args = append(args, f.ProjectID)
		n++
	}
	q += ` ORDER BY id`
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Spec
	for rows.Next() {
		var s Spec
		if err := rows.Scan(&s.ID, &s.Title, &s.Content, &s.Track, &s.Status, &s.ProjectID, &s.Tags, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetSpec(ctx context.Context, pool *pgxpool.Pool, id int64) (Spec, error) {
	var s Spec
	err := pool.QueryRow(ctx,
		`SELECT id, title, content, track, status, project_id, tags, created_at, updated_at FROM specs WHERE id = $1`, id,
	).Scan(&s.ID, &s.Title, &s.Content, &s.Track, &s.Status, &s.ProjectID, &s.Tags, &s.CreatedAt, &s.UpdatedAt)
	if err == pgx.ErrNoRows {
		return s, ErrNotFound
	}
	return s, err
}

type UpdateSpecParams struct {
	Title   *string
	Content *string
	Tags    []byte
	Track   *string
	Status  *string
}

func UpdateSpec(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdateSpecParams) (Spec, error) {
	set := []string{"updated_at = NOW()"}
	args := []any{}
	n := 1
	if p.Title != nil {
		set = append(set, "title = $"+itoa(n))
		args = append(args, *p.Title)
		n++
	}
	if p.Content != nil {
		set = append(set, "content = $"+itoa(n))
		args = append(args, *p.Content)
		n++
	}
	if p.Tags != nil {
		set = append(set, "tags = $"+itoa(n))
		args = append(args, p.Tags)
		n++
	}
	if p.Track != nil {
		set = append(set, "track = $"+itoa(n))
		args = append(args, *p.Track)
		n++
	}
	if p.Status != nil {
		set = append(set, "status = $"+itoa(n))
		args = append(args, *p.Status)
		n++
	}
	args = append(args, id)
	q := `UPDATE specs SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) +
		` RETURNING id, title, content, track, status, project_id, tags, created_at, updated_at`
	var s Spec
	err := pool.QueryRow(ctx, q, args...).Scan(
		&s.ID, &s.Title, &s.Content, &s.Track, &s.Status, &s.ProjectID, &s.Tags, &s.CreatedAt, &s.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return s, ErrNotFound
	}
	return s, err
}

// --- Workflows ---

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

func WorkflowTotalCost(ctx context.Context, pool *pgxpool.Pool, workflowID int64) (float64, error) {
	var total float64
	err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(cost_usd), 0) FROM phases WHERE workflow_id = $1`, workflowID,
	).Scan(&total)
	return total, err
}

// --- Phases ---

func CreatePhase(ctx context.Context, pool *pgxpool.Pool, workflowID int64, position int, name, goal string, timeoutSeconds int) (Phase, error) {
	var ph Phase
	err := pool.QueryRow(ctx,
		`INSERT INTO phases (workflow_id, position, name, goal, timeout_seconds)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, workflow_id, position, name, goal, prompt_sent, status, retry_count,
		           timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		           started_at, finished_at, review_verdict, review_notes,
		           adjusted_prompt, decision_summary, decision_rationale, files_touched`,
		workflowID, position, name, goal, timeoutSeconds,
	).Scan(phaseScans(&ph)...)
	return ph, err
}

func GetPhase(ctx context.Context, pool *pgxpool.Pool, id int64) (Phase, error) {
	var ph Phase
	err := pool.QueryRow(ctx,
		`SELECT id, workflow_id, position, name, goal, prompt_sent, status, retry_count,
		        timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		        started_at, finished_at, review_verdict, review_notes,
		        adjusted_prompt, decision_summary, decision_rationale, files_touched
		 FROM phases WHERE id = $1`, id,
	).Scan(phaseScans(&ph)...)
	if err == pgx.ErrNoRows {
		return ph, ErrNotFound
	}
	return ph, err
}

func ListPhasesByWorkflow(ctx context.Context, pool *pgxpool.Pool, workflowID int64) ([]Phase, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, workflow_id, position, name, goal, prompt_sent, status, retry_count,
		        timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		        started_at, finished_at, review_verdict, review_notes,
		        adjusted_prompt, decision_summary, decision_rationale, files_touched
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
	RetryCount        *int
}

func UpdatePhase(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdatePhaseParams) (Phase, error) {
	set := []string{}
	args := []any{}
	n := 1
	maybeStr := func(field string, v *string) {
		if v != nil {
			set = append(set, field+" = $"+itoa(n))
			args = append(args, *v)
			n++
		}
	}
	maybeTime := func(field string, v *time.Time) {
		if v != nil {
			set = append(set, field+" = $"+itoa(n))
			args = append(args, *v)
			n++
		}
	}
	maybeStr("status", p.Status)
	maybeStr("prompt_sent", p.PromptSent)
	maybeStr("cerberus_session", p.CerberusSession)
	maybeStr("cerberus_commit", p.CerberusCommit)
	maybeStr("review_verdict", p.ReviewVerdict)
	maybeStr("review_notes", p.ReviewNotes)
	maybeStr("adjusted_prompt", p.AdjustedPrompt)
	maybeStr("decision_summary", p.DecisionSummary)
	maybeStr("decision_rationale", p.DecisionRationale)
	maybeTime("started_at", p.StartedAt)
	maybeTime("finished_at", p.FinishedAt)
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
		` RETURNING id, workflow_id, position, name, goal, prompt_sent, status, retry_count,
		            timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		            started_at, finished_at, review_verdict, review_notes,
		            adjusted_prompt, decision_summary, decision_rationale, files_touched`
	var ph Phase
	err := pool.QueryRow(ctx, q, args...).Scan(phaseScans(&ph)...)
	if err == pgx.ErrNoRows {
		return ph, ErrNotFound
	}
	return ph, err
}

func NextPendingPhase(ctx context.Context, pool *pgxpool.Pool, workflowID int64) (Phase, error) {
	var ph Phase
	err := pool.QueryRow(ctx,
		`SELECT id, workflow_id, position, name, goal, prompt_sent, status, retry_count,
		        timeout_seconds, cerberus_session, cerberus_commit, cost_usd,
		        started_at, finished_at, review_verdict, review_notes,
		        adjusted_prompt, decision_summary, decision_rationale, files_touched
		 FROM phases WHERE workflow_id = $1 AND status = 'pending' ORDER BY position LIMIT 1`, workflowID,
	).Scan(phaseScans(&ph)...)
	if err == pgx.ErrNoRows {
		return ph, ErrNotFound
	}
	return ph, err
}

// --- Phase logs ---

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

// helpers

func phaseScans(ph *Phase) []any {
	return []any{
		&ph.ID, &ph.WorkflowID, &ph.Position, &ph.Name, &ph.Goal,
		&ph.PromptSent, &ph.Status, &ph.RetryCount,
		&ph.TimeoutSeconds, &ph.CerberusSession, &ph.CerberusCommit, &ph.CostUSD,
		&ph.StartedAt, &ph.FinishedAt, &ph.ReviewVerdict, &ph.ReviewNotes,
		&ph.AdjustedPrompt, &ph.DecisionSummary, &ph.DecisionRationale, &ph.FilesTouched,
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func joinComma(s []string) string {
	return strings.Join(s, ", ")
}

// --- SpecDrafts ---

type SpecDraft struct {
	ID              int64      `json:"id"`
	ProjectID       *int64     `json:"project_id"`
	Title           string     `json:"title"`
	CerberusSession string     `json:"cerberus_session"`
	Messages        []byte     `json:"messages"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type UpdateSpecDraftParams struct {
	Title           *string
	Messages        []byte
	Status          *string
	CerberusSession *string
}

func CreateSpecDraft(ctx context.Context, pool *pgxpool.Pool, projectID *int64, title string) (SpecDraft, error) {
	var d SpecDraft
	err := pool.QueryRow(ctx,
		`INSERT INTO spec_drafts (project_id, title) VALUES ($1, $2)
		 RETURNING id, project_id, title, cerberus_session, messages, status, created_at, updated_at`,
		projectID, title,
	).Scan(&d.ID, &d.ProjectID, &d.Title, &d.CerberusSession, &d.Messages, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func GetSpecDraft(ctx context.Context, pool *pgxpool.Pool, id int64) (SpecDraft, error) {
	var d SpecDraft
	err := pool.QueryRow(ctx,
		`SELECT id, project_id, title, cerberus_session, messages, status, created_at, updated_at FROM spec_drafts WHERE id = $1`, id,
	).Scan(&d.ID, &d.ProjectID, &d.Title, &d.CerberusSession, &d.Messages, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if err == pgx.ErrNoRows {
		return d, ErrNotFound
	}
	return d, err
}

func ListSpecDrafts(ctx context.Context, pool *pgxpool.Pool) ([]SpecDraft, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, project_id, title, cerberus_session, messages, status, created_at, updated_at FROM spec_drafts ORDER BY id DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpecDraft
	for rows.Next() {
		var d SpecDraft
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Title, &d.CerberusSession, &d.Messages, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
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
	args = append(args, id)
	q := `UPDATE spec_drafts SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) +
		` RETURNING id, project_id, title, cerberus_session, messages, status, created_at, updated_at`
	var d SpecDraft
	err := pool.QueryRow(ctx, q, args...).Scan(
		&d.ID, &d.ProjectID, &d.Title, &d.CerberusSession, &d.Messages, &d.Status, &d.CreatedAt, &d.UpdatedAt,
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
