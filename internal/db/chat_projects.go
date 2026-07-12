package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func AttachProjectToSession(ctx context.Context, pool *pgxpool.Pool, sessionID, projectID int64) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO chat_session_projects (session_id, project_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		sessionID, projectID,
	)
	return err
}

func DetachProjectFromSession(ctx context.Context, pool *pgxpool.Pool, sessionID, projectID int64) error {
	_, err := pool.Exec(ctx,
		`DELETE FROM chat_session_projects WHERE session_id = $1 AND project_id = $2`,
		sessionID, projectID,
	)
	return err
}

func ListSessionProjects(ctx context.Context, pool *pgxpool.Pool, sessionID int64) ([]Project, error) {
	rows, err := pool.Query(ctx,
		`SELECT p.id, p.name, p.repo_path, p.created_at
		 FROM projects p
		 JOIN chat_session_projects csp ON csp.project_id = p.id
		 WHERE csp.session_id = $1
		 ORDER BY csp.added_at`,
		sessionID,
	)
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
