package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tonis2/foundry/internal/repository"
)

// This file manages the repository context attached to a chat session.
// Externally, the domain is expressed in terms of the canonical Repository
// (internal/repository) and its RepositoryID, matching the naming used
// elsewhere for repository ownership (see Spec.RepositoryID and
// SpecDraft.RepositoryID in types.go). The physical SQL backing this
// remains the chat_session_projects join table and its project_id column,
// left unchanged so no migration is required.

// AttachRepositoryToSession attaches the Repository identified by
// repositoryID as context for the chat session identified by sessionID.
// Attaching the same Repository to the same session more than once is a
// no-op.
func AttachRepositoryToSession(ctx context.Context, pool *pgxpool.Pool, sessionID, repositoryID int64) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO chat_session_projects (session_id, project_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		sessionID, repositoryID,
	)
	return err
}

// DetachRepositoryFromSession removes the Repository identified by
// repositoryID from the chat session identified by sessionID's context.
// Detaching a Repository that is not attached is a no-op.
func DetachRepositoryFromSession(ctx context.Context, pool *pgxpool.Pool, sessionID, repositoryID int64) error {
	_, err := pool.Exec(ctx,
		`DELETE FROM chat_session_projects WHERE session_id = $1 AND project_id = $2`,
		sessionID, repositoryID,
	)
	return err
}

// ListSessionRepositories returns every canonical Repository attached to
// the chat session identified by sessionID, ordered by attachment time.
// A Repository with no LocalPath (remote-only) is returned safely with a
// nil LocalPath, exactly as ListRepositories/GetRepository do, rather
// than failing to scan a NULL repo_path column.
func ListSessionRepositories(ctx context.Context, pool *pgxpool.Pool, sessionID int64) ([]repository.Repository, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+repositorySelectColumns+`
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
	var out []repository.Repository
	for rows.Next() {
		r, err := scanRepository(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
