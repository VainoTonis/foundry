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
	"github.com/jackc/pgx/v5/pgxpool"
)

const agentSessionSelectColumns = `id, session, source_session_id, kind, origin,
	repository_id, phase_id, repo_path, model, parent_session,
	started_at, ended_at,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd,
	tool_call_count, turn_count, next_seq`

func scanAgentSession(row pgx.Row) (AgentSession, error) {
	var s AgentSession
	err := row.Scan(
		&s.ID, &s.Session, &s.SourceSessionID, &s.Kind, &s.Origin,
		&s.RepositoryID, &s.PhaseID, &s.RepoPath, &s.Model, &s.ParentSession,
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
	Session         string
	SourceSessionID string
	Origin          string
	Kind            string
	RepositoryID    *int64
	PhaseID         *int64
	RepoPath        *string
	Model           *string
	ParentSession   *string
	StartedAt       *time.Time
}

func EnsureAgentSession(ctx context.Context, pool *pgxpool.Pool, p EnsureAgentSessionParams) (AgentSession, error) {
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

	row := pool.QueryRow(ctx,
		`INSERT INTO agent_sessions (
			session, source_session_id, kind, origin,
			repository_id, phase_id, repo_path, model, parent_session, started_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($10, now()))
		ON CONFLICT (session) DO UPDATE SET
			kind = CASE WHEN agent_sessions.kind = 'unknown' THEN EXCLUDED.kind ELSE agent_sessions.kind END,
			repository_id = COALESCE(agent_sessions.repository_id, EXCLUDED.repository_id),
			phase_id = COALESCE(agent_sessions.phase_id, EXCLUDED.phase_id),
			repo_path = COALESCE(agent_sessions.repo_path, EXCLUDED.repo_path),
			model = COALESCE(agent_sessions.model, EXCLUDED.model),
			parent_session = COALESCE(agent_sessions.parent_session, EXCLUDED.parent_session)
		RETURNING `+agentSessionSelectColumns,
		p.Session, p.SourceSessionID, kind, p.Origin,
		p.RepositoryID, p.PhaseID, p.RepoPath, p.Model, p.ParentSession, p.StartedAt,
	)
	return scanAgentSession(row)
}

func GetAgentSessionBySession(ctx context.Context, pool *pgxpool.Pool, session string) (AgentSession, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+agentSessionSelectColumns+` FROM agent_sessions WHERE session = $1`,
		session,
	)
	return scanAgentSession(row)
}

func AllocateAgentSessionSeq(ctx context.Context, pool *pgxpool.Pool, agentSessionID int64) (int64, error) {
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

const agentTurnSelectColumns = `id, agent_session_id, seq,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, ts`

func scanAgentTurn(row pgx.Row) (AgentTurn, error) {
	var t AgentTurn
	err := row.Scan(
		&t.ID, &t.AgentSessionID, &t.Seq,
		&t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens, &t.CostUSD, &t.Ts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentTurn{}, ErrNotFound
	}
	return t, err
}

type InsertAgentTurnParams struct {
	AgentSessionID   int64
	Seq              int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
	Ts               *time.Time
}

func InsertAgentTurn(ctx context.Context, pool *pgxpool.Pool, p InsertAgentTurnParams) (AgentTurn, error) {
	row := pool.QueryRow(ctx,
		`INSERT INTO agent_turns (
			agent_session_id, seq, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, ts
		) VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, now()))
		RETURNING `+agentTurnSelectColumns,
		p.AgentSessionID, p.Seq, p.InputTokens, p.OutputTokens, p.CacheReadTokens, p.CacheWriteTokens, p.CostUSD, p.Ts,
	)
	t, err := scanAgentTurn(row)
	if isForeignKeyViolation(err) {
		return AgentTurn{}, ErrNotFound
	}
	return t, err
}

const agentToolCallSelectColumns = `id, agent_session_id, seq, result_seq,
	tool_call_id, tool_name,
	tool_input, tool_input_truncated, tool_input_sha256, tool_input_original_bytes,
	tool_result, tool_result_truncated, tool_result_sha256, tool_result_original_bytes,
	is_error, duration_ms, created_at, finished_at`

func scanAgentToolCall(row pgx.Row) (AgentToolCall, error) {
	var c AgentToolCall
	err := row.Scan(
		&c.ID, &c.AgentSessionID, &c.Seq, &c.ResultSeq,
		&c.ToolCallID, &c.ToolName,
		&c.ToolInput, &c.ToolInputTruncated, &c.ToolInputSHA256, &c.ToolInputOriginalBytes,
		&c.ToolResult, &c.ToolResultTruncated, &c.ToolResultSHA256, &c.ToolResultOriginalBytes,
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
	ToolInputTruncated     bool
	ToolInputSHA256        *string
	ToolInputOriginalBytes *int64
	CreatedAt              *time.Time
}

func InsertAgentToolCall(ctx context.Context, pool *pgxpool.Pool, p InsertAgentToolCallParams) (AgentToolCall, error) {
	if p.ToolName == "" {
		return AgentToolCall{}, fmt.Errorf("db: InsertAgentToolCall: ToolName is required")
	}
	row := pool.QueryRow(ctx,
		`INSERT INTO agent_tool_calls (
			agent_session_id, seq, tool_call_id, tool_name,
			tool_input, tool_input_truncated, tool_input_sha256, tool_input_original_bytes, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, now()))
		RETURNING `+agentToolCallSelectColumns,
		p.AgentSessionID, p.Seq, p.ToolCallID, p.ToolName,
		p.ToolInput, p.ToolInputTruncated, p.ToolInputSHA256, p.ToolInputOriginalBytes, p.CreatedAt,
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
	ResultTruncated     bool
	ResultSHA256        *string
	ResultOriginalBytes *int64
	IsError             *bool
	DurationMs          *int64
	FinishedAt          *time.Time
}

func AttachAgentToolResult(ctx context.Context, pool *pgxpool.Pool, p AttachAgentToolResultParams) (AgentToolCall, error) {
	if p.ToolName == "" {
		return AgentToolCall{}, fmt.Errorf("db: AttachAgentToolResult: ToolName is required")
	}

	const setClause = `
		result_seq = $1,
		tool_result = $2,
		tool_result_truncated = $3,
		tool_result_sha256 = $4,
		tool_result_original_bytes = $5,
		is_error = $6,
		duration_ms = $7,
		finished_at = COALESCE($8, now())`

	if p.ToolCallID != nil && *p.ToolCallID != "" {
		row := pool.QueryRow(ctx,
			`UPDATE agent_tool_calls SET`+setClause+`
			 WHERE agent_session_id = $9 AND tool_call_id = $10 AND finished_at IS NULL
			 RETURNING `+agentToolCallSelectColumns,
			p.ResultSeq, p.Result, p.ResultTruncated, p.ResultSHA256, p.ResultOriginalBytes,
			p.IsError, p.DurationMs, p.FinishedAt,
			p.AgentSessionID, *p.ToolCallID,
		)
		return scanAgentToolCall(row)
	}

	row := pool.QueryRow(ctx,
		`UPDATE agent_tool_calls SET`+setClause+`
		 WHERE id = (
			SELECT id FROM agent_tool_calls
			WHERE agent_session_id = $9 AND tool_name = $10 AND finished_at IS NULL
			ORDER BY seq DESC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		 )
		 RETURNING `+agentToolCallSelectColumns,
		p.ResultSeq, p.Result, p.ResultTruncated, p.ResultSHA256, p.ResultOriginalBytes,
		p.IsError, p.DurationMs, p.FinishedAt,
		p.AgentSessionID, p.ToolName,
	)
	return scanAgentToolCall(row)
}

const agentMessageSelectColumns = `id, agent_session_id, seq, role,
	content, content_truncated, content_sha256, content_original_bytes, created_at`

func scanAgentMessage(row pgx.Row) (AgentMessage, error) {
	var m AgentMessage
	err := row.Scan(
		&m.ID, &m.AgentSessionID, &m.Seq, &m.Role,
		&m.Content, &m.ContentTruncated, &m.ContentSHA256, &m.ContentOriginalBytes, &m.CreatedAt,
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
	Content              *string
	ContentTruncated     bool
	ContentSHA256        *string
	ContentOriginalBytes *int64
	CreatedAt            *time.Time
}

func InsertAgentMessage(ctx context.Context, pool *pgxpool.Pool, p InsertAgentMessageParams) (AgentMessage, error) {
	if p.Role == "" {
		return AgentMessage{}, fmt.Errorf("db: InsertAgentMessage: Role is required")
	}
	row := pool.QueryRow(ctx,
		`INSERT INTO agent_messages (
			agent_session_id, seq, role, content, content_truncated, content_sha256, content_original_bytes, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, now()))
		RETURNING `+agentMessageSelectColumns,
		p.AgentSessionID, p.Seq, p.Role, p.Content, p.ContentTruncated, p.ContentSHA256, p.ContentOriginalBytes, p.CreatedAt,
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

func AddAgentSessionUsage(ctx context.Context, pool *pgxpool.Pool, agentSessionID int64, delta AgentSessionUsageDelta) (AgentSession, error) {
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

func CloseAgentSession(ctx context.Context, pool *pgxpool.Pool, agentSessionID int64, endedAt *time.Time) (AgentSession, error) {
	row := pool.QueryRow(ctx,
		`UPDATE agent_sessions SET ended_at = COALESCE(agent_sessions.ended_at, $2, now())
		 WHERE id = $1
		 RETURNING `+agentSessionSelectColumns,
		agentSessionID, endedAt,
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
func ListAgentSessionsByPhase(ctx context.Context, pool *pgxpool.Pool, phaseID int64) ([]AgentSession, error) {
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

// ListAgentSessions returns all agent sessions across the system, ordered
// deterministically by start time and then id. It returns an empty slice
// (not an error) when there are no agent sessions. Used to power a
// session-centric telemetry view that isn't scoped to a single phase.
func ListAgentSessions(ctx context.Context, pool *pgxpool.Pool) ([]AgentSession, error) {
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
func ListAgentTurnsBySession(ctx context.Context, pool *pgxpool.Pool, agentSessionID int64) ([]AgentTurn, error) {
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
func ListAgentToolCallsBySession(ctx context.Context, pool *pgxpool.Pool, agentSessionID int64) ([]AgentToolCall, error) {
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
func ListAgentMessagesBySession(ctx context.Context, pool *pgxpool.Pool, agentSessionID int64) ([]AgentMessage, error) {
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
