package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/db"
)

type EventType string

const (
	EventSessionStart EventType = "session_start"
	EventToolUse      EventType = "tool_use"
	EventToolResult   EventType = "tool_result"
	EventMessageEnd   EventType = "message_end"
	EventFinalMessage EventType = "final_message"
	EventSessionEnd   EventType = "session_end"
	EventDelta        EventType = "delta"
)

type SessionKind string

const SessionKindUnknown SessionKind = "unknown"

const MaxContentBytes = 256 * 1024

type Session struct {
	Session         string
	SourceSessionID string
	Origin          string
	Kind            SessionKind
}

type Attribution struct {
	RepositoryID  *int64
	PhaseID       *int64
	RepoPath      *string
	Model         *string
	ParentSession *string
}

type Tool struct {
	ToolCallID *string
	ToolName   string
	Input      string
	Result     string
	IsError    *bool
	DurationMs *int64
}

type Message struct {
	Role    string
	Content string
}

type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
}

func (u Usage) isZero() bool {
	return u == Usage{}
}

type Event struct {
	Type        EventType
	Session     Session
	Attribution Attribution
	Tool        Tool
	Message     Message
	Usage       Usage
	Timestamp   time.Time
}

func validateEvent(ev Event) error {
	if ev.Session.Session == "" {
		return fmt.Errorf("telemetry: session is required")
	}
	switch ev.Type {
	case EventSessionStart:
		if ev.Session.SourceSessionID == "" {
			return fmt.Errorf("telemetry: source_session_id is required for session_start")
		}
		if ev.Session.Origin == "" {
			return fmt.Errorf("telemetry: origin is required for session_start")
		}
	case EventToolUse, EventToolResult:
		if ev.Tool.ToolName == "" {
			return fmt.Errorf("telemetry: tool_name is required for %s", ev.Type)
		}
	case EventFinalMessage:
		if ev.Message.Role == "" {
			return fmt.Errorf("telemetry: role is required for final_message")
		}
	case EventMessageEnd, EventSessionEnd:
	case EventDelta:
		// A single generated response measured 109x more delta chunks
		// than final messages, so only aggregated content is ingested.
		return fmt.Errorf("telemetry: delta events are not ingested")
	default:
		return fmt.Errorf("telemetry: unknown event type %q", ev.Type)
	}
	return nil
}

func classify(ev Event) (store bool, err error) {
	if err := validateEvent(ev); err != nil {
		return false, err
	}
	if ev.Type == EventMessageEnd && ev.Usage.isZero() {
		return false, nil
	}
	return true, nil
}

func truncateContent(s string) (out string, truncated bool, sha *string, originalBytes *int64) {
	if len(s) <= MaxContentBytes {
		return s, false, nil, nil
	}
	cut := MaxContentBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	sum := sha256.Sum256([]byte(s))
	hash := hex.EncodeToString(sum[:])
	n := int64(len(s))
	return s[:cut], true, &hash, &n
}

func contentFields(s string) (*string, bool, *string, *int64) {
	if s == "" {
		return nil, false, nil, nil
	}
	out, truncated, sha, originalBytes := truncateContent(s)
	return &out, truncated, sha, originalBytes
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func Ingest(ctx context.Context, pool *pgxpool.Pool, ev Event) error {
	store, err := classify(ev)
	if err != nil {
		return err
	}
	if !store {
		return nil
	}

	switch ev.Type {
	case EventSessionStart:
		_, err := db.EnsureAgentSession(ctx, pool, db.EnsureAgentSessionParams{
			Session:         ev.Session.Session,
			SourceSessionID: ev.Session.SourceSessionID,
			Origin:          ev.Session.Origin,
			Kind:            string(ev.Session.Kind),
			RepositoryID:    ev.Attribution.RepositoryID,
			PhaseID:         ev.Attribution.PhaseID,
			RepoPath:        ev.Attribution.RepoPath,
			Model:           ev.Attribution.Model,
			ParentSession:   ev.Attribution.ParentSession,
			StartedAt:       optionalTime(ev.Timestamp),
		})
		return err

	case EventSessionEnd:
		sess, err := db.GetAgentSessionBySession(ctx, pool, ev.Session.Session)
		if err != nil {
			return err
		}
		_, err = db.CloseAgentSession(ctx, pool, sess.ID, optionalTime(ev.Timestamp))
		return err

	case EventToolUse:
		sess, err := db.GetAgentSessionBySession(ctx, pool, ev.Session.Session)
		if err != nil {
			return err
		}
		seq, err := db.AllocateAgentSessionSeq(ctx, pool, sess.ID)
		if err != nil {
			return err
		}
		content, truncated, sha, originalBytes := contentFields(ev.Tool.Input)
		if _, err := db.InsertAgentToolCall(ctx, pool, db.InsertAgentToolCallParams{
			AgentSessionID:         sess.ID,
			Seq:                    seq,
			ToolCallID:             ev.Tool.ToolCallID,
			ToolName:               ev.Tool.ToolName,
			ToolInput:              content,
			ToolInputTruncated:     truncated,
			ToolInputSHA256:        sha,
			ToolInputOriginalBytes: originalBytes,
			CreatedAt:              optionalTime(ev.Timestamp),
		}); err != nil {
			return err
		}
		_, err = db.AddAgentSessionUsage(ctx, pool, sess.ID, db.AgentSessionUsageDelta{ToolCallCount: 1})
		return err

	case EventToolResult:
		sess, err := db.GetAgentSessionBySession(ctx, pool, ev.Session.Session)
		if err != nil {
			return err
		}
		seq, err := db.AllocateAgentSessionSeq(ctx, pool, sess.ID)
		if err != nil {
			return err
		}
		content, truncated, sha, originalBytes := contentFields(ev.Tool.Result)
		_, err = db.AttachAgentToolResult(ctx, pool, db.AttachAgentToolResultParams{
			AgentSessionID:      sess.ID,
			ToolCallID:          ev.Tool.ToolCallID,
			ToolName:            ev.Tool.ToolName,
			ResultSeq:           seq,
			Result:              content,
			ResultTruncated:     truncated,
			ResultSHA256:        sha,
			ResultOriginalBytes: originalBytes,
			IsError:             ev.Tool.IsError,
			DurationMs:          ev.Tool.DurationMs,
			FinishedAt:          optionalTime(ev.Timestamp),
		})
		return err

	case EventMessageEnd:
		sess, err := db.GetAgentSessionBySession(ctx, pool, ev.Session.Session)
		if err != nil {
			return err
		}
		seq, err := db.AllocateAgentSessionSeq(ctx, pool, sess.ID)
		if err != nil {
			return err
		}
		if _, err := db.InsertAgentTurn(ctx, pool, db.InsertAgentTurnParams{
			AgentSessionID:   sess.ID,
			Seq:              seq,
			InputTokens:      ev.Usage.InputTokens,
			OutputTokens:     ev.Usage.OutputTokens,
			CacheReadTokens:  ev.Usage.CacheReadTokens,
			CacheWriteTokens: ev.Usage.CacheWriteTokens,
			CostUSD:          ev.Usage.CostUSD,
			Ts:               optionalTime(ev.Timestamp),
		}); err != nil {
			return err
		}
		_, err = db.AddAgentSessionUsage(ctx, pool, sess.ID, db.AgentSessionUsageDelta{
			InputTokens:      ev.Usage.InputTokens,
			OutputTokens:     ev.Usage.OutputTokens,
			CacheReadTokens:  ev.Usage.CacheReadTokens,
			CacheWriteTokens: ev.Usage.CacheWriteTokens,
			CostUSD:          ev.Usage.CostUSD,
			TurnCount:        1,
		})
		return err

	case EventFinalMessage:
		sess, err := db.GetAgentSessionBySession(ctx, pool, ev.Session.Session)
		if err != nil {
			return err
		}
		seq, err := db.AllocateAgentSessionSeq(ctx, pool, sess.ID)
		if err != nil {
			return err
		}
		content, truncated, sha, originalBytes := contentFields(ev.Message.Content)
		_, err = db.InsertAgentMessage(ctx, pool, db.InsertAgentMessageParams{
			AgentSessionID:       sess.ID,
			Seq:                  seq,
			Role:                 ev.Message.Role,
			Content:              content,
			ContentTruncated:     truncated,
			ContentSHA256:        sha,
			ContentOriginalBytes: originalBytes,
			CreatedAt:            optionalTime(ev.Timestamp),
		})
		return err
	}

	return nil
}
