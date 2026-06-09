package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ListSpecsFilter struct {
	Status    string
	ProjectID int64
}

type UpdateSpecParams struct {
	Title   *string
	Content *string
	Tags    []byte
	Track   *string
	Status  *string
}

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

func DeleteSpec(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	_, err := pool.Exec(ctx, `DELETE FROM specs WHERE id = $1`, id)
	return err
}
