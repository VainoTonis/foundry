package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UpdatePlanParams struct {
	Status        *string
	Title         *string
	Summary       *string
	Content       *string
	RepositoryIDs *[]int64
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx, so plan-loading
// helpers can run either against a pool or inside an in-progress
// transaction (e.g. right after CreatePlan inserts plan_repositories rows,
// before commit).
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Querier is the exported form of querier, for callers outside the db
// package (e.g. telemetry ingest) that need to run a sequence of db.*
// helper calls against either a *pgxpool.Pool or an in-progress pgx.Tx so
// they commit or roll back atomically together.
type Querier = querier

const planSelectColumns = `p.id, p.title, p.summary, p.content,
		COALESCE((SELECT w.status FROM plan_workflows pw JOIN workflows w ON w.id = pw.workflow_id WHERE pw.plan_id = p.id ORDER BY w.id DESC LIMIT 1), p.status),
		p.created_at, p.updated_at`

func scanPlan(row pgx.Row) (Plan, error) {
	var p Plan
	err := row.Scan(&p.ID, &p.Title, &p.Summary, &p.Content, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// loadPlanRepositories returns the ordered (position 0 first) repository
// membership for a single plan, resolving each repository_id to its
// Repository fields in the same query (no N+1 per repository).
func loadPlanRepositories(ctx context.Context, q querier, planID int64) ([]PlanRepository, error) {
	rows, err := q.Query(ctx,
		`SELECT pr.position, pr.repository_id, proj.name, proj.local_path, proj.remote_url, proj.created_at
		 FROM plan_repositories pr JOIN repositories proj ON proj.id = pr.repository_id
		 WHERE pr.plan_id = $1 ORDER BY pr.position`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanRepository
	for rows.Next() {
		var pr PlanRepository
		if err := rows.Scan(&pr.Position, &pr.RepositoryID, &pr.Repository.Name, &pr.Repository.LocalPath, &pr.Repository.RemoteURL, &pr.Repository.CreatedAt); err != nil {
			return nil, err
		}
		pr.Repository.ID = pr.RepositoryID
		out = append(out, pr)
	}
	return out, rows.Err()
}

// loadPlanRepositoriesForPlans returns the ordered repository membership
// for every plan id in planIDs, grouped by plan id, in a single query
// (avoiding an N+1 query per plan for callers like ListPlans).
func loadPlanRepositoriesForPlans(ctx context.Context, q querier, planIDs []int64) (map[int64][]PlanRepository, error) {
	out := make(map[int64][]PlanRepository, len(planIDs))
	if len(planIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx,
		`SELECT pr.plan_id, pr.position, pr.repository_id, proj.name, proj.local_path, proj.remote_url, proj.created_at
		 FROM plan_repositories pr JOIN repositories proj ON proj.id = pr.repository_id
		 WHERE pr.plan_id = ANY($1) ORDER BY pr.plan_id, pr.position`, planIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var planID int64
		var pr PlanRepository
		if err := rows.Scan(&planID, &pr.Position, &pr.RepositoryID, &pr.Repository.Name, &pr.Repository.LocalPath, &pr.Repository.RemoteURL, &pr.Repository.CreatedAt); err != nil {
			return nil, err
		}
		pr.Repository.ID = pr.RepositoryID
		out[planID] = append(out[planID], pr)
	}
	return out, rows.Err()
}

type UpdatePlanStepParams struct {
	Status        *string
	Text          *string
	ParallelGroup *int
}

// CreatePlan creates a plan owned by the given ordered, non-empty list of
// repository ids (position 0 is the primary repository) and its
// plan_repositories rows, all in a single transaction. It returns an
// error and leaves no rows persisted if repositoryIDs is empty, contains a
// duplicate id, or contains an id that does not exist in repositories.
func CreatePlan(ctx context.Context, pool *pgxpool.Pool, repositoryIDs []int64, title, summary, content string) (Plan, error) {
	if len(repositoryIDs) == 0 {
		return Plan{}, fmt.Errorf("create plan: at least one repository id is required")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	p, err := scanPlan(tx.QueryRow(ctx,
		`INSERT INTO plans (title, summary, content, status) VALUES ($1, $2, $3, 'pending')
		 RETURNING id, title, summary, content, status, created_at, updated_at`,
		title, summary, content,
	))
	if err != nil {
		return Plan{}, err
	}

	for position, repositoryID := range repositoryIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO plan_repositories (plan_id, repository_id, position) VALUES ($1, $2, $3)`,
			p.ID, repositoryID, position,
		); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				switch pgErr.Code {
				case "23503": // foreign_key_violation
					return Plan{}, fmt.Errorf("create plan: repository %d does not exist: %w", repositoryID, ErrNotFound)
				case "23505": // unique_violation
					return Plan{}, fmt.Errorf("create plan: repository %d listed more than once", repositoryID)
				}
			}
			return Plan{}, fmt.Errorf("create plan: insert plan_repositories for repository %d: %w", repositoryID, err)
		}
	}

	repos, err := loadPlanRepositories(ctx, tx, p.ID)
	if err != nil {
		return Plan{}, err
	}
	p.Repositories = repos

	if err := tx.Commit(ctx); err != nil {
		return Plan{}, err
	}
	return p, nil
}

func GetPlan(ctx context.Context, pool *pgxpool.Pool, id int64) (Plan, error) {
	p, err := scanPlan(pool.QueryRow(ctx, `SELECT `+planSelectColumns+` FROM plans p WHERE p.id = $1`, id))
	if err == pgx.ErrNoRows {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	repos, err := loadPlanRepositories(ctx, pool, p.ID)
	if err != nil {
		return p, err
	}
	p.Repositories = repos
	return p, nil
}

func ListPlans(ctx context.Context, pool *pgxpool.Pool) ([]Plan, error) {
	rows, err := pool.Query(ctx, `SELECT `+planSelectColumns+` FROM plans p ORDER BY p.id DESC`)
	if err != nil {
		return nil, err
	}
	var out []Plan
	var ids []int64
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
		ids = append(ids, p.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	repoByPlan, err := loadPlanRepositoriesForPlans(ctx, pool, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Repositories = repoByPlan[out[i].ID]
	}
	return out, nil
}

func UpdatePlan(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdatePlanParams) (Plan, error) {
	if p.RepositoryIDs != nil && len(*p.RepositoryIDs) == 0 {
		return Plan{}, fmt.Errorf("update plan: repository_ids must contain at least one repository id")
	}
	if p.RepositoryIDs != nil {
		seen := make(map[int64]bool, len(*p.RepositoryIDs))
		for _, pid := range *p.RepositoryIDs {
			if seen[pid] {
				return Plan{}, fmt.Errorf("update plan: repository %d listed more than once", pid)
			}
			seen[pid] = true
		}
	}

	set := []string{}
	args := []any{}
	n := 1
	if p.Status != nil {
		set = append(set, "status = $"+itoa(n))
		args = append(args, *p.Status)
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
	if len(set) == 0 && p.RepositoryIDs == nil {
		return GetPlan(ctx, pool, id)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var out Plan
	if len(set) > 0 {
		set = append(set, "updated_at = NOW()")
		args = append(args, id)
		q := `UPDATE plans SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) +
			` RETURNING id, title, summary, content, status, created_at, updated_at`
		out, err = scanPlan(tx.QueryRow(ctx, q, args...))
		if err == pgx.ErrNoRows {
			return out, ErrNotFound
		}
		if err != nil {
			return out, err
		}
	} else {
		out, err = scanPlan(tx.QueryRow(ctx, `SELECT `+planSelectColumns+` FROM plans p WHERE p.id = $1`, id))
		if err == pgx.ErrNoRows {
			return out, ErrNotFound
		}
		if err != nil {
			return out, err
		}
	}

	if p.RepositoryIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM plan_repositories WHERE plan_id = $1`, id); err != nil {
			return Plan{}, err
		}
		for position, repositoryID := range *p.RepositoryIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO plan_repositories (plan_id, repository_id, position) VALUES ($1, $2, $3)`,
				id, repositoryID, position,
			); err != nil {
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) {
					switch pgErr.Code {
					case "23503": // foreign_key_violation
						return Plan{}, fmt.Errorf("update plan: repository %d does not exist: %w", repositoryID, ErrNotFound)
					case "23505": // unique_violation
						return Plan{}, fmt.Errorf("update plan: repository %d listed more than once", repositoryID)
					}
				}
				return Plan{}, fmt.Errorf("update plan: insert plan_repositories for repository %d: %w", repositoryID, err)
			}
		}
	}

	repos, err := loadPlanRepositories(ctx, tx, out.ID)
	if err != nil {
		return out, err
	}
	out.Repositories = repos

	if err := tx.Commit(ctx); err != nil {
		return Plan{}, err
	}
	return out, nil
}

func GetPlanByWorkflow(ctx context.Context, pool *pgxpool.Pool, workflowID int64) (Plan, error) {
	var p Plan
	err := pool.QueryRow(ctx,
		`SELECT p.id, p.title, p.summary, p.content, w.status, p.created_at, p.updated_at
		 FROM plan_workflows pw JOIN plans p ON p.id = pw.plan_id JOIN workflows w ON w.id = pw.workflow_id
		 WHERE pw.workflow_id = $1`, workflowID).
		Scan(&p.ID, &p.Title, &p.Summary, &p.Content, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	repos, err := loadPlanRepositories(ctx, pool, p.ID)
	if err != nil {
		return p, err
	}
	p.Repositories = repos
	return p, nil
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

func GetPlanStepByPosition(ctx context.Context, pool *pgxpool.Pool, planID int64, position int) (PlanStep, error) {
	var s PlanStep
	err := pool.QueryRow(ctx,
		`SELECT id, plan_id, position, text, status, created_at, updated_at, parallel_group FROM plan_steps WHERE plan_id = $1 AND position = $2`, planID, position,
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
