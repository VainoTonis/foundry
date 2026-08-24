package db

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KnownCerberusSessionPageParams describes the stable, bounded ordering used by
// the sessions UI. Session is the tie breaker because it is the identity used
// to deduplicate rows which can occur in more than one persisted source.
type KnownCerberusSessionPageParams struct {
	Limit         int
	BeforeAt      *time.Time
	BeforeSession string
	Lifecycle     string
	RepositoryID  *int64
}

const knownCerberusSessionsQuery = `
WITH raw AS (
	SELECT p.cerberus_session AS session, 'workflow_phase'::text AS type, p.status AS foundry_status,
	       pr.id AS repository_id, pr.name AS repository_name, COALESCE(pr.local_path, '') AS repository_local_path,
	       s.id AS spec_id, s.title AS spec_title, w.id AS workflow_id, p.id AS phase_id,
	       p.name AS phase_name, NULL::bigint AS draft_id, ''::text AS draft_title,
	       COALESCE(p.finished_at, p.started_at, w.finished_at, w.created_at) AS last_updated_at,
	       p.finished_at, (p.status IN ('done', 'failed')) AS safe_to_clean,
	       CASE WHEN p.status IN ('done', 'failed') THEN '' ELSE 'workflow phase is not terminal' END AS unsafe_reason,
	       1 AS source_priority, p.id AS source_id
	FROM phases p
	JOIN workflows w ON w.id = p.workflow_id
	JOIN specs s ON s.id = w.spec_id
	JOIN repositories pr ON pr.id = s.repository_id
	WHERE p.cerberus_session IS NOT NULL AND p.cerberus_session <> ''
	UNION ALL
	SELECT d.cerberus_session, 'spec_draft'::text, d.status,
	       pr.id, COALESCE(pr.name, ''), COALESCE(pr.local_path, ''),
	       NULL::bigint, ''::text, NULL::bigint, NULL::bigint, ''::text,
	       d.id, d.title, d.updated_at, NULL::timestamptz,
	       (d.status IN ('frozen', 'error')),
	       CASE WHEN d.status IN ('frozen', 'error') THEN '' ELSE 'spec draft is active' END,
	       2, d.id
	FROM spec_drafts d
	LEFT JOIN repositories pr ON pr.id = d.repository_id
	WHERE d.cerberus_session <> ''
	UNION ALL
	SELECT e.session, 'external'::text, e.status,
	       NULL::bigint, ''::text, ''::text, NULL::bigint, ''::text,
	       NULL::bigint, NULL::bigint, ''::text, NULL::bigint, ''::text,
	       e.last_seen_at, NULL::timestamptz, (e.status = 'done'),
	       CASE WHEN e.status = 'done' THEN '' ELSE 'external session is not done' END,
	       3, e.id
	FROM external_cerberus_sessions e
), canonical AS (
	SELECT *, row_number() OVER (
		PARTITION BY session ORDER BY last_updated_at DESC, source_priority, source_id DESC
	) AS identity_rank
	FROM raw
), filtered AS (
	SELECT * FROM canonical
	WHERE identity_rank = 1
	  AND ($1::bigint IS NULL OR repository_id = $1)
	  AND ($2 = '' OR ($2 = 'closed' AND safe_to_clean) OR ($2 = 'active' AND NOT safe_to_clean))
	  AND ($3::timestamptz IS NULL OR (last_updated_at, session) < ($3, $4))
)
SELECT session, type, foundry_status, repository_id, repository_name, repository_local_path,
       spec_id, spec_title, workflow_id, phase_id, phase_name, draft_id, draft_title,
       last_updated_at, finished_at, safe_to_clean, unsafe_reason
FROM filtered
ORDER BY last_updated_at DESC, session DESC
LIMIT $5`

// ListKnownCerberusSessionsPage merges the three persisted attribution sources
// in PostgreSQL, deduplicates before limiting, and applies filters and the
// keyset boundary before returning rows.
func ListKnownCerberusSessionsPage(ctx context.Context, pool *pgxpool.Pool, p KnownCerberusSessionPageParams) ([]KnownCerberusSession, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return listKnownCerberusSessions(ctx, pool, p, limit)
}

// ListKnownCerberusSessions preserves the historical complete-list API used by
// cleanup and direct lookup paths. Unlike the old implementation it is one
// deterministic, deduplicating query rather than three unbounded queries.
func ListKnownCerberusSessions(ctx context.Context, pool *pgxpool.Pool) ([]KnownCerberusSession, error) {
	return listKnownCerberusSessions(ctx, pool, KnownCerberusSessionPageParams{}, math.MaxInt32)
}

func listKnownCerberusSessions(ctx context.Context, pool *pgxpool.Pool, p KnownCerberusSessionPageParams, limit int) ([]KnownCerberusSession, error) {
	beforeSession := p.BeforeSession
	if beforeSession == "" {
		// With a nil timestamp this value is ignored. With a timestamp-only
		// cursor it includes every identity tied at that timestamp.
		beforeSession = "\U0010ffff"
	}
	rows, err := pool.Query(ctx, knownCerberusSessionsQuery,
		p.RepositoryID, p.Lifecycle, p.BeforeAt, beforeSession, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnownCerberusSession{}
	for rows.Next() {
		k, err := scanKnownCerberusSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func scanKnownCerberusSession(row pgx.Row) (KnownCerberusSession, error) {
	var k KnownCerberusSession
	err := row.Scan(&k.Session, &k.Type, &k.FoundryStatus,
		&k.RepositoryID, &k.RepositoryName, &k.RepositoryLocalPath,
		&k.SpecID, &k.SpecTitle, &k.WorkflowID, &k.PhaseID, &k.PhaseName,
		&k.DraftID, &k.DraftTitle, &k.LastUpdatedAt, &k.FinishedAt,
		&k.SafeToClean, &k.UnsafeReason)
	return k, err
}
