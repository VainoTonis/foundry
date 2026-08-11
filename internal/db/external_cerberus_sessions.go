package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func UpsertExternalCerberusSession(ctx context.Context, pool *pgxpool.Pool, session, repo, status string) (ExternalCerberusSession, error) {
	var s ExternalCerberusSession
	err := pool.QueryRow(ctx,
		`INSERT INTO external_cerberus_sessions (session, repo, status)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (session) DO UPDATE SET
		     repo = EXCLUDED.repo,
		     status = EXCLUDED.status,
		     last_seen_at = now()
		 RETURNING id, session, repo, status, first_seen_at, last_seen_at`,
		session, repo, status,
	).Scan(&s.ID, &s.Session, &s.Repo, &s.Status, &s.FirstSeenAt, &s.LastSeenAt)
	return s, err
}

func ListExternalCerberusSessions(ctx context.Context, pool *pgxpool.Pool) ([]ExternalCerberusSession, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, session, repo, status, first_seen_at, last_seen_at
		 FROM external_cerberus_sessions ORDER BY last_seen_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExternalCerberusSession
	for rows.Next() {
		var s ExternalCerberusSession
		if err := rows.Scan(&s.ID, &s.Session, &s.Repo, &s.Status, &s.FirstSeenAt, &s.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func DeleteExternalCerberusSession(ctx context.Context, pool *pgxpool.Pool, session string) error {
	_, err := pool.Exec(ctx, `DELETE FROM external_cerberus_sessions WHERE session = $1`, session)
	return err
}
