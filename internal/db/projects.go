package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UpdateProjectParams struct {
	Name     *string
	RepoPath *string
}

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

func UpdateProject(ctx context.Context, pool *pgxpool.Pool, id int64, p UpdateProjectParams) (Project, error) {
	set := []string{}
	args := []any{}
	n := 1
	if p.Name != nil {
		set = append(set, "name = $"+itoa(n))
		args = append(args, *p.Name)
		n++
	}
	if p.RepoPath != nil {
		set = append(set, "repo_path = $"+itoa(n))
		args = append(args, *p.RepoPath)
		n++
	}
	if len(set) == 0 {
		return GetProject(ctx, pool, id)
	}
	args = append(args, id)
	q := `UPDATE projects SET ` + joinComma(set) + ` WHERE id = $` + itoa(n) +
		` RETURNING id, name, repo_path, created_at`
	var out Project
	err := pool.QueryRow(ctx, q, args...).Scan(&out.ID, &out.Name, &out.RepoPath, &out.CreatedAt)
	if err == pgx.ErrNoRows {
		return out, ErrNotFound
	}
	return out, err
}

func DeleteProject(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	tag, err := pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
