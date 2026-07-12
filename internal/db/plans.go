package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UpdatePlanParams struct {
	Status    *string
	ProjectID *int64
	Title     *string
	Summary   *string
	Content   *string
}

type UpdatePlanStepParams struct {
	Status        *string
	Text          *string
	ParallelGroup *int
}

func CreatePlan(ctx context.Context, pool *pgxpool.Pool, projectID int64, title, summary, content string) (Plan, error) {
	var p Plan
	err := pool.QueryRow(ctx,
		`INSERT INTO plans (project_id, repo_name, title, summary, content, status)
		 SELECT id, name, $2, $3, $4, 'pending' FROM projects WHERE id = $1
		 RETURNING id, project_id, repo_name, title, summary, content, status, created_at, updated_at`,
		projectID, title, summary, content,
	).Scan(&p.ID, &p.ProjectID, &p.RepoName, &p.Title, &p.Summary, &p.Content, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return p, ErrNotFound
	}
	return p, err
}

func GetPlan(ctx context.Context, pool *pgxpool.Pool, id int64) (Plan, error) {
	var p Plan
	err := pool.QueryRow(ctx,
		`SELECT p.id, p.project_id, p.repo_name, p.title, p.summary, p.content,
		 COALESCE((SELECT w.status FROM plan_workflows pw JOIN workflows w ON w.id = pw.workflow_id WHERE pw.plan_id = p.id ORDER BY w.id DESC LIMIT 1), p.status),
		 p.created_at, p.updated_at FROM plans p WHERE p.id = $1`, id,
	).Scan(&p.ID, &p.ProjectID, &p.RepoName, &p.Title, &p.Summary, &p.Content, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return p, ErrNotFound
	}
	return p, err
}

func ListPlans(ctx context.Context, pool *pgxpool.Pool) ([]Plan, error) {
	rows, err := pool.Query(ctx, `SELECT p.id, p.project_id, p.repo_name, p.title, p.summary, p.content,
		COALESCE((SELECT w.status FROM plan_workflows pw JOIN workflows w ON w.id = pw.workflow_id WHERE pw.plan_id = p.id ORDER BY w.id DESC LIMIT 1), p.status),
		p.created_at, p.updated_at FROM plans p ORDER BY p.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.RepoName, &p.Title, &p.Summary, &p.Content, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func UpdatePlan(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdatePlanParams) (Plan, error) {
	set := []string{}
	args := []any{}
	n := 1
	if p.Status != nil {
		set = append(set, "status = $"+itoa(n))
		args = append(args, *p.Status)
		n++
	}
	if p.ProjectID != nil {
		set = append(set, "project_id = $"+itoa(n))
		args = append(args, *p.ProjectID)
		n++
	}
	if p.Title != nil {
		set = append(set, "title = $"+itoa(n))
		args = append(args, *p.Title)
		n++
	}
	if p.Summary != nil {
		set = append(set, "summary = $"+itoa(n))
		args = append(args, *p.Summary)
		n++
	}
	if p.Content != nil {
		set = append(set, "content = $"+itoa(n))
		args = append(args, *p.Content)
		n++
	}
	if len(set) == 0 {
		return GetPlan(ctx, pool, id)
	}
	set = append(set, "updated_at = NOW()")
	args = append(args, id)
	q := `UPDATE plans SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) +
		` RETURNING id, project_id, repo_name, title, summary, content, status, created_at, updated_at`
	var out Plan
	err := pool.QueryRow(ctx, q, args...).Scan(&out.ID, &out.ProjectID, &out.RepoName, &out.Title, &out.Summary, &out.Content, &out.Status, &out.CreatedAt, &out.UpdatedAt)
	if err == pgx.ErrNoRows {
		return out, ErrNotFound
	}
	return out, err
}

func GetPlanByWorkflow(ctx context.Context, pool *pgxpool.Pool, workflowID int64) (Plan, error) {
	var p Plan
	err := pool.QueryRow(ctx, `SELECT p.id, p.project_id, p.repo_name, p.title, p.summary, p.content, w.status, p.created_at, p.updated_at
		FROM plan_workflows pw JOIN plans p ON p.id = pw.plan_id JOIN workflows w ON w.id = pw.workflow_id
		WHERE pw.workflow_id = $1`, workflowID).
		Scan(&p.ID, &p.ProjectID, &p.RepoName, &p.Title, &p.Summary, &p.Content, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return p, ErrNotFound
	}
	return p, err
}

func LinkPlanWorkflow(ctx context.Context, pool *pgxpool.Pool, planID, workflowID int64) error {
	_, err := pool.Exec(ctx, `INSERT INTO plan_workflows (plan_id, workflow_id) VALUES ($1, $2)`, planID, workflowID)
	return err
}

// ---- plan_steps ----

func CreatePlanStep(ctx context.Context, pool *pgxpool.Pool, planID int64, position int, text string, parallelGroup *int) (PlanStep, error) {
	var s PlanStep
	err := pool.QueryRow(ctx,
		`INSERT INTO plan_steps (plan_id, position, text, status, parallel_group) VALUES ($1, $2, $3, 'pending', $4) RETURNING id, plan_id, position, text, status, created_at, updated_at, parallel_group`,
		planID, position, text, parallelGroup,
	).Scan(&s.ID, &s.PlanID, &s.Position, &s.Text, &s.Status, &s.CreatedAt, &s.UpdatedAt, &s.ParallelGroup)
	return s, err
}

func UpdatePlanStep(ctx context.Context, pool *pgxpool.Pool, planID, id int64, p UpdatePlanStepParams) (PlanStep, error) {
	set := []string{}
	args := []any{}
	n := 1
	if p.Status != nil {
		set = append(set, "status = $"+itoa(n))
		args = append(args, *p.Status)
		n++
	}
	if p.Text != nil {
		set = append(set, "text = $"+itoa(n))
		args = append(args, *p.Text)
		n++
	}
	if p.ParallelGroup != nil {
		set = append(set, "parallel_group = $"+itoa(n))
		args = append(args, *p.ParallelGroup)
		n++
	}
	if len(set) == 0 {
		return GetPlanStepByID(ctx, pool, planID, id)
	}
	set = append(set, "updated_at = NOW()")
	args = append(args, id, planID)
	q := `UPDATE plan_steps SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) + ` AND plan_id = $` + itoa(n+1) +
		` RETURNING id, plan_id, position, text, status, created_at, updated_at, parallel_group`
	var out PlanStep
	err := pool.QueryRow(ctx, q, args...).Scan(&out.ID, &out.PlanID, &out.Position, &out.Text, &out.Status, &out.CreatedAt, &out.UpdatedAt, &out.ParallelGroup)
	if err == pgx.ErrNoRows {
		return out, ErrNotFound
	}
	return out, err
}

func GetPlanStep(ctx context.Context, pool *pgxpool.Pool, id int64) (PlanStep, error) {
	var s PlanStep
	err := pool.QueryRow(ctx,
		`SELECT id, plan_id, position, text, status, created_at, updated_at, parallel_group FROM plan_steps WHERE id = $1`, id,
	).Scan(&s.ID, &s.PlanID, &s.Position, &s.Text, &s.Status, &s.CreatedAt, &s.UpdatedAt, &s.ParallelGroup)
	if err == pgx.ErrNoRows {
		return s, ErrNotFound
	}
	return s, err
}

func GetPlanStepByID(ctx context.Context, pool *pgxpool.Pool, planID, stepID int64) (PlanStep, error) {
	var s PlanStep
	err := pool.QueryRow(ctx,
		`SELECT id, plan_id, position, text, status, created_at, updated_at, parallel_group FROM plan_steps WHERE id = $1 AND plan_id = $2`, stepID, planID,
	).Scan(&s.ID, &s.PlanID, &s.Position, &s.Text, &s.Status, &s.CreatedAt, &s.UpdatedAt, &s.ParallelGroup)
	if err == pgx.ErrNoRows {
		return s, ErrNotFound
	}
	return s, err
}

func ListPlanSteps(ctx context.Context, pool *pgxpool.Pool, planID int64) ([]PlanStep, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, plan_id, position, text, status, created_at, updated_at, parallel_group FROM plan_steps WHERE plan_id = $1 ORDER BY position`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanStep
	for rows.Next() {
		var s PlanStep
		if err := rows.Scan(&s.ID, &s.PlanID, &s.Position, &s.Text, &s.Status, &s.CreatedAt, &s.UpdatedAt, &s.ParallelGroup); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
