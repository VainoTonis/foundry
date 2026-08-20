package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ListKnownCerberusSessions(ctx context.Context, pool *pgxpool.Pool) ([]KnownCerberusSession, error) {
	rows, err := pool.Query(ctx, `
		SELECT p.cerberus_session, 'workflow_phase', p.status,
		       pr.id, pr.name, pr.repo_path, s.id, s.title, w.id, p.id, p.name,
		       COALESCE(p.finished_at, p.started_at, w.finished_at, w.created_at), p.finished_at
		FROM phases p
		JOIN workflows w ON w.id = p.workflow_id
		JOIN specs s ON s.id = w.spec_id
		JOIN projects pr ON pr.id = s.project_id
		WHERE p.cerberus_session IS NOT NULL AND p.cerberus_session <> ''
		ORDER BY COALESCE(p.finished_at, p.started_at, w.finished_at, w.created_at) DESC, p.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnownCerberusSession{}
	for rows.Next() {
		var k KnownCerberusSession
		var typ string
		if err := rows.Scan(&k.Session, &typ, &k.FoundryStatus, &k.RepositoryID, &k.RepositoryName, &k.RepositoryLocalPath, &k.SpecID, &k.SpecTitle, &k.WorkflowID, &k.PhaseID, &k.PhaseName, &k.LastUpdatedAt, &k.FinishedAt); err != nil {
			return nil, err
		}
		k.Type = typ
		if k.FoundryStatus == "done" || k.FoundryStatus == "failed" {
			k.SafeToClean = true
		} else {
			k.UnsafeReason = "workflow phase is not terminal"
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `
		SELECT d.cerberus_session, 'spec_draft', d.status,
		       pr.id, COALESCE(pr.name, ''), COALESCE(pr.repo_path, ''), d.id, d.title, d.updated_at
		FROM spec_drafts d
		LEFT JOIN projects pr ON pr.id = d.project_id
		WHERE d.cerberus_session <> ''
		ORDER BY d.updated_at DESC, d.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k KnownCerberusSession
		var typ string
		if err := rows.Scan(&k.Session, &typ, &k.FoundryStatus, &k.RepositoryID, &k.RepositoryName, &k.RepositoryLocalPath, &k.DraftID, &k.DraftTitle, &k.LastUpdatedAt); err != nil {
			return nil, err
		}
		k.Type = typ
		if IsSpecDraftSafeToCleanStatus(k.FoundryStatus) {
			k.SafeToClean = true
		} else {
			k.UnsafeReason = "spec draft is active"
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `
		SELECT session, 'external', status, last_seen_at
		FROM external_cerberus_sessions
		ORDER BY last_seen_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k KnownCerberusSession
		var typ string
		if err := rows.Scan(&k.Session, &typ, &k.FoundryStatus, &k.LastUpdatedAt); err != nil {
			return nil, err
		}
		k.Type = typ
		if k.FoundryStatus == "done" {
			k.SafeToClean = true
		} else {
			k.UnsafeReason = "external session is not done"
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
