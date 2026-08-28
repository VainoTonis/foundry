// Allocated monotonic sequences (see AllocateAgentSessionSeq) may have
// gaps after failed writes; only strictly-increasing order is guaranteed.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const agentSessionSelectColumns = `id, session, source_session_id, kind, origin,
	repository_id, phase_id, repo_path, model, parent_session,
	schema_version, last_event_at, close_reason, lifecycle_state,
	start_event_seen, end_event_seen, parent_source_session_id,
	started_at, ended_at,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd,
	tool_call_count, turn_count, next_seq`

func scanAgentSession(row pgx.Row) (AgentSession, error) {
	var s AgentSession
	err := row.Scan(
		&s.ID, &s.Session, &s.SourceSessionID, &s.Kind, &s.Origin,
		&s.RepositoryID, &s.PhaseID, &s.RepoPath, &s.Model, &s.ParentSession,
		&s.SchemaVersion, &s.LastEventAt, &s.CloseReason, &s.LifecycleState,
		&s.StartEventSeen, &s.EndEventSeen, &s.ParentSourceSessionID,
		&s.StartedAt, &s.EndedAt,
		&s.InputTokens, &s.OutputTokens, &s.CacheReadTokens, &s.CacheWriteTokens, &s.CostUSD,
		&s.ToolCallCount, &s.TurnCount, &s.NextSeq,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentSession{}, ErrNotFound
	}
	return s, err
}

type EnsureAgentSessionParams struct {
	Session               string
	SourceSessionID       string
	Origin                string
	Kind                  string
	RepositoryID          *int64
	PhaseID               *int64
	RepoPath              *string
	Model                 *string
	ParentSession         *string
	ParentSourceSessionID *string
	SchemaVersion         string
	LifecycleState        string
	StartEventSeen        bool
	EventAt               *time.Time
	AttributionMethod     string
	AttributionConfidence *float64
	StartedAt             *time.Time
}

func EnsureAgentSession(ctx context.Context, pool querier, p EnsureAgentSessionParams) (AgentSession, error) {
	if p.Session == "" {
		return AgentSession{}, fmt.Errorf("db: EnsureAgentSession: Session is required")
	}
	if p.SourceSessionID == "" {
		return AgentSession{}, fmt.Errorf("db: EnsureAgentSession: SourceSessionID is required")
	}
	if p.Origin == "" {
		return AgentSession{}, fmt.Errorf("db: EnsureAgentSession: Origin is required")
	}
	kind := p.Kind
	if kind == "" {
		kind = "unknown"
	}

	schemaVersion := p.SchemaVersion
	if schemaVersion == "" {
		schemaVersion = "unknown"
	}
	lifecycleState := p.LifecycleState
	if lifecycleState == "" {
		lifecycleState = "unknown"
	}
	var s AgentSession
	err := pool.QueryRow(ctx,
		`INSERT INTO agent_sessions (
			session, source_session_id, kind, origin,
			repository_id, phase_id, repo_path, model, parent_session,
			parent_source_session_id, schema_version, lifecycle_state,
			start_event_seen, started_at, last_event_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
			COALESCE($9, (SELECT session FROM agent_sessions WHERE source_session_id = $10)),
			$10, $11, $12, $13, COALESCE($14, now()), COALESCE($15, $14, now()))
		ON CONFLICT (session) DO UPDATE SET
			kind = CASE WHEN agent_sessions.kind = 'unknown' THEN EXCLUDED.kind ELSE agent_sessions.kind END,
			source_session_id = CASE WHEN agent_sessions.source_session_id = agent_sessions.session THEN EXCLUDED.source_session_id ELSE agent_sessions.source_session_id END,
			origin = CASE WHEN agent_sessions.origin = 'unknown' THEN EXCLUDED.origin ELSE agent_sessions.origin END,
			repository_id = COALESCE(agent_sessions.repository_id, EXCLUDED.repository_id),
			phase_id = COALESCE(agent_sessions.phase_id, EXCLUDED.phase_id),
			repo_path = COALESCE(agent_sessions.repo_path, EXCLUDED.repo_path),
			model = COALESCE(agent_sessions.model, EXCLUDED.model),
			parent_source_session_id = COALESCE(agent_sessions.parent_source_session_id, EXCLUDED.parent_source_session_id),
			parent_session = COALESCE(agent_sessions.parent_session, EXCLUDED.parent_session,
				(SELECT session FROM agent_sessions WHERE source_session_id = EXCLUDED.parent_source_session_id)),
			schema_version = CASE WHEN agent_sessions.schema_version = 'unknown' THEN EXCLUDED.schema_version ELSE agent_sessions.schema_version END,
			lifecycle_state = CASE WHEN EXCLUDED.lifecycle_state = 'active' THEN 'active' ELSE agent_sessions.lifecycle_state END,
			start_event_seen = agent_sessions.start_event_seen OR EXCLUDED.start_event_seen,
			ended_at = CASE WHEN EXCLUDED.lifecycle_state = 'active' THEN NULL ELSE agent_sessions.ended_at END,
			last_event_at = GREATEST(agent_sessions.last_event_at, EXCLUDED.last_event_at)
		RETURNING `+agentSessionSelectColumns,
		p.Session, p.SourceSessionID, kind, p.Origin,
		p.RepositoryID, p.PhaseID, p.RepoPath, p.Model, p.ParentSession,
		p.ParentSourceSessionID, schemaVersion, lifecycleState, p.StartEventSeen, p.StartedAt, p.EventAt,
	).Scan(
		&s.ID, &s.Session, &s.SourceSessionID, &s.Kind, &s.Origin,
		&s.RepositoryID, &s.PhaseID, &s.RepoPath, &s.Model, &s.ParentSession,
		&s.SchemaVersion, &s.LastEventAt, &s.CloseReason, &s.LifecycleState,
		&s.StartEventSeen, &s.EndEventSeen, &s.ParentSourceSessionID,
		&s.StartedAt, &s.EndedAt, &s.InputTokens, &s.OutputTokens, &s.CacheReadTokens,
		&s.CacheWriteTokens, &s.CostUSD, &s.ToolCallCount, &s.TurnCount, &s.NextSeq,
	)
	if err != nil {
		return AgentSession{}, err
	}
	// Resolve children that arrived before this parent was known. Never
	// replace an already-resolved parent relationship.
	if s.SourceSessionID != "" && s.SourceSessionID != s.Session {
		if _, err = pool.Exec(ctx, `UPDATE agent_sessions
			SET parent_session = $1
			WHERE parent_source_session_id = $2 AND parent_session IS NULL`,
			s.Session, s.SourceSessionID); err != nil {
			return AgentSession{}, err
		}
	}
	if p.RepositoryID != nil {
		method := p.AttributionMethod
		if method == "" {
			method = "unknown"
		}
		_, err = pool.Exec(ctx, `INSERT INTO agent_session_repositories
			(agent_session_id, repository_id, attribution_method, attribution_confidence)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (agent_session_id, repository_id) DO UPDATE SET
				attribution_method = EXCLUDED.attribution_method,
				attribution_confidence = EXCLUDED.attribution_confidence
			WHERE COALESCE(EXCLUDED.attribution_confidence, -1) > COALESCE(agent_session_repositories.attribution_confidence, -1)
			   OR (COALESCE(EXCLUDED.attribution_confidence, -1) = COALESCE(agent_session_repositories.attribution_confidence, -1)
			       AND agent_session_repositories.attribution_method = 'unknown' AND EXCLUDED.attribution_method <> 'unknown')`,
			s.ID, *p.RepositoryID, method, p.AttributionConfidence)
	}
	return s, err
}

func GetAgentSessionBySession(ctx context.Context, pool querier, session string) (AgentSession, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+agentSessionSelectColumns+` FROM agent_sessions WHERE session = $1`,
		session,
	)
	return scanAgentSession(row)
}

// GetAgentSessionBySourceSessionID looks up an agent session by its
// producer-assigned source_session_id, as opposed to GetAgentSessionBySession
// which keys on the internal display session name. It returns ErrNotFound
// when no such source_session_id has been recorded yet -- an expected,
// normal outcome when a child session's session_start event arrives before
// its parent's, not an error condition.
func GetAgentSessionBySourceSessionID(ctx context.Context, pool querier, sourceSessionID string) (AgentSession, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+agentSessionSelectColumns+` FROM agent_sessions WHERE source_session_id = $1`,
		sourceSessionID,
	)
	return scanAgentSession(row)
}

func AllocateAgentSessionSeq(ctx context.Context, pool querier, agentSessionID int64) (int64, error) {
	var seq int64
	err := pool.QueryRow(ctx,
		`UPDATE agent_sessions SET next_seq = next_seq + 1 WHERE id = $1 RETURNING next_seq - 1`,
		agentSessionID,
	).Scan(&seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return seq, err
}

const agentTurnSelectColumns = `id, agent_session_id, seq, turn_index, source_message_id,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd,
	model, provider, thinking_level, stop_reason, ts`

func scanAgentTurn(row pgx.Row) (AgentTurn, error) {
	var t AgentTurn
	err := row.Scan(
		&t.ID, &t.AgentSessionID, &t.Seq, &t.TurnIndex, &t.SourceMessageID,
		&t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens, &t.CostUSD,
		&t.Model, &t.Provider, &t.ThinkingLevel, &t.StopReason, &t.Ts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentTurn{}, ErrNotFound
	}
	return t, err
}

type InsertAgentTurnParams struct {
	AgentSessionID   int64
	Seq              int64
	TurnIndex        *int64
	SourceMessageID  *string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
	Model            string
	Provider         string
	ThinkingLevel    string
	StopReason       string
	Ts               *time.Time
}

func InsertAgentTurn(ctx context.Context, pool querier, p InsertAgentTurnParams) (AgentTurn, error) {
	row := pool.QueryRow(ctx,
		`INSERT INTO agent_turns (
			agent_session_id, seq, turn_index, source_message_id,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd,
			model, provider, thinking_level, stop_reason, ts
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE(NULLIF($10, ''), 'unknown'),
			COALESCE(NULLIF($11, ''), 'unknown'), COALESCE(NULLIF($12, ''), 'unknown'),
			COALESCE(NULLIF($13, ''), 'unknown'), COALESCE($14, now()))
		RETURNING `+agentTurnSelectColumns,
		p.AgentSessionID, p.Seq, p.TurnIndex, p.SourceMessageID,
		p.InputTokens, p.OutputTokens, p.CacheReadTokens, p.CacheWriteTokens, p.CostUSD,
		p.Model, p.Provider, p.ThinkingLevel, p.StopReason, p.Ts,
	)
	t, err := scanAgentTurn(row)
	if isForeignKeyViolation(err) {
		return AgentTurn{}, ErrNotFound
	}
	return t, err
}

const agentToolCallSelectColumns = `id, agent_session_id, seq, result_seq,
	tool_call_id, tool_name,
	tool_input, tool_input_redacted, tool_input_omitted,
	tool_input_truncated, tool_input_sha256, tool_input_original_bytes,
	tool_result, tool_result_redacted, tool_result_omitted,
	tool_result_truncated, tool_result_sha256, tool_result_original_bytes,
	is_error, duration_ms, created_at, finished_at`

func scanAgentToolCall(row pgx.Row) (AgentToolCall, error) {
	var c AgentToolCall
	err := row.Scan(
		&c.ID, &c.AgentSessionID, &c.Seq, &c.ResultSeq,
		&c.ToolCallID, &c.ToolName,
		&c.ToolInput, &c.ToolInputRedacted, &c.ToolInputOmitted,
		&c.ToolInputTruncated, &c.ToolInputSHA256, &c.ToolInputOriginalBytes,
		&c.ToolResult, &c.ToolResultRedacted, &c.ToolResultOmitted,
		&c.ToolResultTruncated, &c.ToolResultSHA256, &c.ToolResultOriginalBytes,
		&c.IsError, &c.DurationMs, &c.CreatedAt, &c.FinishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentToolCall{}, ErrNotFound
	}
	return c, err
}

type InsertAgentToolCallParams struct {
	AgentSessionID         int64
	Seq                    int64
	ToolCallID             *string
	ToolName               string
	ToolInput              *string
	ToolInputRedacted      bool
	ToolInputOmitted       bool
	ToolInputTruncated     bool
	ToolInputSHA256        *string
	ToolInputOriginalBytes *int64
	CreatedAt              *time.Time
}

func InsertAgentToolCall(ctx context.Context, pool querier, p InsertAgentToolCallParams) (AgentToolCall, error) {
	if p.ToolName == "" {
		return AgentToolCall{}, fmt.Errorf("db: InsertAgentToolCall: ToolName is required")
	}
	row := pool.QueryRow(ctx,
		`INSERT INTO agent_tool_calls (
			agent_session_id, seq, tool_call_id, tool_name,
			tool_input, tool_input_redacted, tool_input_omitted,
			tool_input_truncated, tool_input_sha256, tool_input_original_bytes, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, COALESCE($11, now()))
		RETURNING `+agentToolCallSelectColumns,
		p.AgentSessionID, p.Seq, p.ToolCallID, p.ToolName,
		p.ToolInput, p.ToolInputRedacted, p.ToolInputOmitted,
		p.ToolInputTruncated, p.ToolInputSHA256, p.ToolInputOriginalBytes, p.CreatedAt,
	)
	c, err := scanAgentToolCall(row)
	if isForeignKeyViolation(err) {
		return AgentToolCall{}, ErrNotFound
	}
	return c, err
}

type AttachAgentToolResultParams struct {
	AgentSessionID      int64
	ToolCallID          *string
	ToolName            string
	ResultSeq           int64
	Result              *string
	ResultRedacted      bool
	ResultOmitted       bool
	ResultTruncated     bool
	ResultSHA256        *string
	ResultOriginalBytes *int64
	IsError             *bool
	DurationMs          *int64
	FinishedAt          *time.Time
}

func AttachAgentToolResult(ctx context.Context, pool querier, p AttachAgentToolResultParams) (AgentToolCall, error) {
	if p.ToolName == "" {
		return AgentToolCall{}, fmt.Errorf("db: AttachAgentToolResult: ToolName is required")
	}

	const setClause = `
		result_seq = $1,
		tool_result = $2,
		tool_result_redacted = $3,
		tool_result_omitted = $4,
		tool_result_truncated = $5,
		tool_result_sha256 = $6,
		tool_result_original_bytes = $7,
		is_error = $8,
		duration_ms = $9,
		finished_at = COALESCE($10, now())`

	if p.ToolCallID != nil && *p.ToolCallID != "" {
		row := pool.QueryRow(ctx,
			`UPDATE agent_tool_calls SET`+setClause+`
			 WHERE agent_session_id = $11 AND tool_call_id = $12 AND finished_at IS NULL
			 RETURNING `+agentToolCallSelectColumns,
			p.ResultSeq, p.Result, p.ResultRedacted, p.ResultOmitted,
			p.ResultTruncated, p.ResultSHA256, p.ResultOriginalBytes,
			p.IsError, p.DurationMs, p.FinishedAt,
			p.AgentSessionID, *p.ToolCallID,
		)
		return scanAgentToolCall(row)
	}

	row := pool.QueryRow(ctx,
		`UPDATE agent_tool_calls SET`+setClause+`
		 WHERE id = (
			SELECT id FROM agent_tool_calls
			WHERE agent_session_id = $11 AND tool_name = $12 AND finished_at IS NULL
			ORDER BY seq DESC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		 )
		 RETURNING `+agentToolCallSelectColumns,
		p.ResultSeq, p.Result, p.ResultRedacted, p.ResultOmitted,
		p.ResultTruncated, p.ResultSHA256, p.ResultOriginalBytes,
		p.IsError, p.DurationMs, p.FinishedAt,
		p.AgentSessionID, p.ToolName,
	)
	return scanAgentToolCall(row)
}

const agentMessageSelectColumns = `id, agent_session_id, seq, role,
	source_message_id, turn_index, input_source, is_final,
	content, content_redacted, content_truncated, content_sha256, content_original_bytes, created_at`

func scanAgentMessage(row pgx.Row) (AgentMessage, error) {
	var m AgentMessage
	err := row.Scan(
		&m.ID, &m.AgentSessionID, &m.Seq, &m.Role,
		&m.SourceMessageID, &m.TurnIndex, &m.InputSource, &m.IsFinal,
		&m.Content, &m.ContentRedacted, &m.ContentTruncated, &m.ContentSHA256, &m.ContentOriginalBytes, &m.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentMessage{}, ErrNotFound
	}
	return m, err
}

type InsertAgentMessageParams struct {
	AgentSessionID       int64
	Seq                  int64
	Role                 string
	SourceMessageID      *string
	TurnIndex            *int64
	InputSource          string
	IsFinal              bool
	Content              *string
	ContentRedacted      bool
	ContentTruncated     bool
	ContentSHA256        *string
	ContentOriginalBytes *int64
	CreatedAt            *time.Time
}

func InsertAgentMessage(ctx context.Context, pool querier, p InsertAgentMessageParams) (AgentMessage, error) {
	if p.Role == "" {
		return AgentMessage{}, fmt.Errorf("db: InsertAgentMessage: Role is required")
	}
	row := pool.QueryRow(ctx,
		`INSERT INTO agent_messages (
			agent_session_id, seq, role, source_message_id, turn_index, input_source, is_final,
			content, content_redacted, content_truncated, content_sha256, content_original_bytes, created_at
		) VALUES ($1, $2, $3, $4, $5, COALESCE(NULLIF($6, ''), 'unknown'), $7,
			$8, $9, $10, $11, $12, COALESCE($13, now()))
		RETURNING `+agentMessageSelectColumns,
		p.AgentSessionID, p.Seq, p.Role, p.SourceMessageID, p.TurnIndex, p.InputSource, p.IsFinal,
		p.Content, p.ContentRedacted, p.ContentTruncated, p.ContentSHA256, p.ContentOriginalBytes, p.CreatedAt,
	)
	m, err := scanAgentMessage(row)
	if isForeignKeyViolation(err) {
		return AgentMessage{}, ErrNotFound
	}
	return m, err
}

type AgentSessionUsageDelta struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
	ToolCallCount    int64
	TurnCount        int64
}

func AddAgentSessionUsage(ctx context.Context, pool querier, agentSessionID int64, delta AgentSessionUsageDelta) (AgentSession, error) {
	row := pool.QueryRow(ctx,
		`UPDATE agent_sessions SET
			input_tokens = input_tokens + $2,
			output_tokens = output_tokens + $3,
			cache_read_tokens = cache_read_tokens + $4,
			cache_write_tokens = cache_write_tokens + $5,
			cost_usd = cost_usd + $6,
			tool_call_count = tool_call_count + $7,
			turn_count = turn_count + $8
		 WHERE id = $1
		 RETURNING `+agentSessionSelectColumns,
		agentSessionID,
		delta.InputTokens, delta.OutputTokens, delta.CacheReadTokens, delta.CacheWriteTokens,
		delta.CostUSD, delta.ToolCallCount, delta.TurnCount,
	)
	return scanAgentSession(row)
}

func CloseAgentSession(ctx context.Context, pool querier, agentSessionID int64, endedAt *time.Time, closeReasons ...string) (AgentSession, error) {
	closeReason := "unknown"
	if len(closeReasons) > 0 && closeReasons[0] != "" {
		closeReason = closeReasons[0]
	}
	row := pool.QueryRow(ctx,
		`UPDATE agent_sessions SET ended_at = COALESCE(agent_sessions.ended_at, $2, now()),
			last_event_at = GREATEST(agent_sessions.last_event_at, COALESCE($2, now())),
			close_reason = CASE WHEN $3 <> 'unknown' OR agent_sessions.close_reason = 'unknown'
				THEN $3 ELSE agent_sessions.close_reason END,
			lifecycle_state = 'closed', end_event_seen = true
		 WHERE id = $1
		 RETURNING `+agentSessionSelectColumns,
		agentSessionID, endedAt, closeReason,
	)
	return scanAgentSession(row)
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// ListAgentSessionsByPhase returns all agent sessions attached to a phase,
// ordered deterministically by start time and then id. It returns an empty
// slice (not an error) when the phase has no sessions.
func ListAgentSessionsByPhase(ctx context.Context, pool querier, phaseID int64) ([]AgentSession, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+agentSessionSelectColumns+`
		 FROM agent_sessions
		 WHERE phase_id = $1
		 ORDER BY started_at, id`,
		phaseID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentSession{}
	for rows.Next() {
		s, err := scanAgentSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AgentSessionPageParams bounds and filters the recent-session query. The
// (last_event_at,id) cursor matches agent_sessions_recent_idx, avoiding an
// unbounded scan as historical telemetry accumulates.
type AgentSessionPageParams struct {
	Limit    int
	BeforeAt *time.Time
	// BeforeSession is the deterministic cross-source tie breaker used by
	// the merged sessions list. BeforeID remains supported for callers that
	// paginate telemetry by its native (last_event_at,id) ordering.
	BeforeSession string
	BeforeID      *int64
	Lifecycle     string
	RepositoryID  *int64
	// ExcludeAttributed makes the telemetry source return only identities
	// not owned by a persisted phase, draft, or external session. This lets
	// a merged paginator assign each identity to exactly one source.
	ExcludeAttributed bool
}

func ListAgentSessionsPage(ctx context.Context, pool querier, p AgentSessionPageParams) ([]AgentSession, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := pool.Query(ctx,
		`SELECT `+agentSessionSelectColumns+`
		 FROM agent_sessions s
		 WHERE ($1 = ''
		        OR ($1 = 'closed' AND (s.lifecycle_state = 'closed' OR s.ended_at IS NOT NULL))
		        OR ($1 = 'stale' AND s.lifecycle_state <> 'closed' AND s.ended_at IS NULL
		            AND s.last_event_at < now() - interval '15 minutes')
		        OR ($1 = 'active' AND s.lifecycle_state <> 'closed' AND s.ended_at IS NULL
		            AND s.last_event_at >= now() - interval '15 minutes'))
		   AND ($2::bigint IS NULL OR s.repository_id = $2 OR EXISTS (
		       SELECT 1 FROM agent_session_repositories ar
		       WHERE ar.agent_session_id = s.id AND ar.repository_id = $2))
		   AND ($3::timestamptz IS NULL OR
		        ($4 <> '' AND (s.last_event_at, s.session) < ($3, $4)) OR
		        ($4 = '' AND (s.last_event_at, s.id) < ($3, COALESCE($5, 9223372036854775807))))
		   AND (NOT $6 OR NOT EXISTS (
		       SELECT 1 FROM phases p
		       JOIN workflows w ON w.id = p.workflow_id
		       JOIN specs sp ON sp.id = w.spec_id
		       WHERE p.cerberus_session = s.session AND ($2::bigint IS NULL OR sp.repository_id = $2)
		       UNION ALL SELECT 1 FROM spec_drafts d
		       WHERE d.cerberus_session = s.session AND ($2::bigint IS NULL OR d.repository_id = $2)
		       UNION ALL SELECT 1 FROM external_cerberus_sessions e
		       WHERE e.session = s.session AND $2::bigint IS NULL))
		 ORDER BY s.last_event_at DESC,
		          CASE WHEN $4 <> '' THEN s.session ELSE '' END DESC,
		          s.id DESC
		 LIMIT $7`,
		p.Lifecycle, p.RepositoryID, p.BeforeAt, p.BeforeSession, p.BeforeID,
		p.ExcludeAttributed, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentSession{}
	for rows.Next() {
		s, err := scanAgentSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListAgentSessionsBySessionNames loads telemetry attached to an already
// bounded persisted-source page. It must not be used as an unbounded export.
func ListAgentSessionsBySessionNames(ctx context.Context, pool querier, sessions []string) ([]AgentSession, error) {
	if len(sessions) == 0 {
		return []AgentSession{}, nil
	}
	if len(sessions) > 100 {
		return nil, fmt.Errorf("db: ListAgentSessionsBySessionNames: at most 100 sessions are allowed")
	}
	rows, err := pool.Query(ctx,
		`SELECT `+agentSessionSelectColumns+`
		 FROM agent_sessions
		 WHERE session = ANY($1::text[])
		 ORDER BY last_event_at DESC, session DESC`, sessions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentSession{}
	for rows.Next() {
		s, err := scanAgentSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListAgentSessions preserves the complete historical export API. Interactive
// list views should use ListAgentSessionsPage instead.
func ListAgentSessions(ctx context.Context, pool querier) ([]AgentSession, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+agentSessionSelectColumns+`
		 FROM agent_sessions
		 ORDER BY started_at, id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentSession{}
	for rows.Next() {
		s, err := scanAgentSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListAgentTurnsBySession returns all turns for an agent session, ordered
// deterministically by seq. It returns an empty slice (not an error) when
// the session has no turns.
func ListAgentTurnsBySession(ctx context.Context, pool querier, agentSessionID int64) ([]AgentTurn, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+agentTurnSelectColumns+`
		 FROM agent_turns
		 WHERE agent_session_id = $1
		 ORDER BY seq`,
		agentSessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentTurn{}
	for rows.Next() {
		t, err := scanAgentTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListAgentToolCallsBySession returns all tool calls for an agent session,
// ordered deterministically by seq. It returns an empty slice (not an
// error) when the session has no tool calls.
func ListAgentToolCallsBySession(ctx context.Context, pool querier, agentSessionID int64) ([]AgentToolCall, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+agentToolCallSelectColumns+`
		 FROM agent_tool_calls
		 WHERE agent_session_id = $1
		 ORDER BY seq`,
		agentSessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentToolCall{}
	for rows.Next() {
		c, err := scanAgentToolCall(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListAgentMessagesBySession returns all messages for an agent session,
// ordered deterministically by seq. It returns an empty slice (not an
// error) when the session has no messages.
func ListAgentMessagesBySession(ctx context.Context, pool querier, agentSessionID int64) ([]AgentMessage, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+agentMessageSelectColumns+`
		 FROM agent_messages
		 WHERE agent_session_id = $1
		 ORDER BY seq`,
		agentSessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentMessage{}
	for rows.Next() {
		m, err := scanAgentMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
