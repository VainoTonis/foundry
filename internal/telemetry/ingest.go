package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/db"
)

// ErrDuplicateEvent indicates a receipt-identified event was already
// recorded by a prior delivery. Ingest returns it to signal an
// idempotent skip rather than a failure: the event's data was not
// written on this delivery because it was already written by an
// earlier one. Callers that only care about success/failure may treat
// it as a non-error; callers that report per-event outcomes (e.g. a
// batch ingest endpoint) can use errors.Is to distinguish it from a
// rejection.
var ErrDuplicateEvent = errors.New("telemetry: duplicate event")

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

// SemanticSchemaVersion is the currently supported producer semantic schema.
// An omitted version remains supported for legacy producers.
const SemanticSchemaVersion = "1"

type Session struct {
	Session               string
	SourceSessionID       string
	Origin                string
	Kind                  SessionKind
	SchemaVersion         string
	CloseReason           string
	ParentSourceSessionID *string
}

type Attribution struct {
	RepositoryID          *int64
	PhaseID               *int64
	RepoPath              *string
	Model                 *string
	ParentSession         *string
	AttributionMethod     string
	AttributionConfidence *float64
}

type Tool struct {
	ToolCallID     *string
	ToolName       string
	Input          string
	Result         string
	IsError        *bool
	DurationMs     *int64
	InputRedacted  bool
	InputOmitted   bool
	ResultRedacted bool
	ResultOmitted  bool
}

type Message struct {
	Role            string
	Content         string
	ContentRedacted bool
	SourceMessageID *string
	TurnIndex       *int64
	InputSource     string
	IsFinal         bool
}

type Turn struct {
	TurnIndex       *int64
	SourceMessageID *string
	Model           string
	Provider        string
	ThinkingLevel   string
	StopReason      string
}

type Privacy struct {
	Redacted bool
	Omitted  bool
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
	Turn        Turn
	Privacy     Privacy
	Timestamp   time.Time

	// ProducerID identifies the telemetry producer (e.g. a specific agent
	// process) and EventID is a producer-assigned identifier unique within
	// that producer. Together they form a stable event identity used to
	// detect duplicate deliveries via the telemetry receipt ledger. Both
	// must be set together, or both left empty to opt out of duplicate
	// detection.
	ProducerID string
	EventID    string

	// ClientSeq is a monotonically increasing sequence number assigned by
	// the producer, scoped to ProducerID. It is recorded alongside the
	// receipt for ordering/gap diagnostics but is not itself used to
	// reject duplicates; EventID uniqueness is authoritative for that.
	ClientSeq int64
}

func validateEvent(ev Event) error {
	if ev.Session.Session == "" {
		return fmt.Errorf("telemetry: session is required")
	}
	if ev.Session.SchemaVersion != "" && ev.Session.SchemaVersion != SemanticSchemaVersion {
		return fmt.Errorf("telemetry: unsupported schema_version %q", ev.Session.SchemaVersion)
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

// Ingest records a telemetry event. When the event carries a
// ProducerID/EventID identity, claiming the receipt and applying the
// event's side effect (and any usage rollup) happen in a single
// PostgreSQL transaction: if the side effect fails or the process
// crashes before commit, the receipt claim is rolled back too, so a
// retry of the same event is not treated as a duplicate and reprocesses
// it. Without a receipt identity, the event's writes are still wrapped
// in one transaction so multi-statement side effects (e.g. an insert
// plus a usage rollup) commit or roll back together.
func Ingest(ctx context.Context, pool *pgxpool.Pool, ev Event) error {
	store, err := classify(ev)
	if err != nil {
		return err
	}
	// A zero-usage message_end has no turn row to persist, but it is still
	// accepted activity and must touch/reopen its session.
	if !store && ev.Type != EventMessageEnd {
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("telemetry: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if ev.ProducerID != "" || ev.EventID != "" {
		if ev.ProducerID == "" || ev.EventID == "" {
			return fmt.Errorf("telemetry: producer_id and event_id must both be set together")
		}
		_, inserted, err := db.InsertTelemetryReceipt(ctx, tx, db.InsertTelemetryReceiptParams{
			ProducerID: ev.ProducerID,
			EventID:    ev.EventID,
			ClientSeq:  ev.ClientSeq,
			EventType:  string(ev.Type),
			Session:    ev.Session.Session,
			ReceivedAt: optionalTime(ev.Timestamp),
		})
		if err != nil {
			return fmt.Errorf("telemetry: record receipt: %w", err)
		}
		if !inserted {
			// Duplicate delivery of an already-recorded event identity;
			// skip reprocessing so it cannot create duplicate side effects.
			// Nothing was written in this transaction, so the deferred
			// rollback is a no-op.
			return ErrDuplicateEvent
		}
	}

	if err := applyEvent(ctx, tx, ev); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("telemetry: commit: %w", err)
	}
	return nil
}

// ensureSessionForActivity guarantees an agent_sessions row exists for an
// activity event (tool use/result, message end, final message) even if the
// originating session_start event was never received or was lost. It
// derives the minimal required session metadata from whatever the
// activity event itself carries, falling back to the session identifier
// and an "unknown" origin/kind when nothing better is available. This
// mirrors the upsert-on-conflict semantics of a normal session_start, so
// a later, legitimate session_start still fills in any richer metadata.
func ensureSessionForActivity(ctx context.Context, pool db.Querier, ev Event) (db.AgentSession, error) {
	sourceSessionID := ev.Session.SourceSessionID
	if sourceSessionID == "" {
		sourceSessionID = ev.Session.Session
	}
	origin := ev.Session.Origin
	if origin == "" {
		origin = "unknown"
	}
	return db.EnsureAgentSession(ctx, pool, db.EnsureAgentSessionParams{
		Session:               ev.Session.Session,
		SourceSessionID:       sourceSessionID,
		Origin:                origin,
		Kind:                  string(ev.Session.Kind),
		RepositoryID:          ev.Attribution.RepositoryID,
		PhaseID:               ev.Attribution.PhaseID,
		RepoPath:              ev.Attribution.RepoPath,
		Model:                 ev.Attribution.Model,
		ParentSession:         ev.Attribution.ParentSession,
		ParentSourceSessionID: ev.Session.ParentSourceSessionID,
		SchemaVersion:         ev.Session.SchemaVersion,
		LifecycleState:        "active",
		EventAt:               optionalTime(ev.Timestamp),
		AttributionMethod:     ev.Attribution.AttributionMethod,
		AttributionConfidence: ev.Attribution.AttributionConfidence,
		StartedAt:             optionalTime(ev.Timestamp),
	})
}

// applyEvent performs the event-specific write and any associated usage
// rollup. It runs against the querier (a transaction) passed in by
// Ingest, so all statements it issues share that transaction's fate.
func applyEvent(ctx context.Context, pool db.Querier, ev Event) error {
	switch ev.Type {
	case EventSessionStart:
		_, err := db.EnsureAgentSession(ctx, pool, db.EnsureAgentSessionParams{
			Session:               ev.Session.Session,
			SourceSessionID:       ev.Session.SourceSessionID,
			Origin:                ev.Session.Origin,
			Kind:                  string(ev.Session.Kind),
			RepositoryID:          ev.Attribution.RepositoryID,
			PhaseID:               ev.Attribution.PhaseID,
			RepoPath:              ev.Attribution.RepoPath,
			Model:                 ev.Attribution.Model,
			ParentSession:         ev.Attribution.ParentSession,
			ParentSourceSessionID: ev.Session.ParentSourceSessionID,
			SchemaVersion:         ev.Session.SchemaVersion,
			LifecycleState:        "active",
			StartEventSeen:        true,
			EventAt:               optionalTime(ev.Timestamp),
			AttributionMethod:     ev.Attribution.AttributionMethod,
			AttributionConfidence: ev.Attribution.AttributionConfidence,
			StartedAt:             optionalTime(ev.Timestamp),
		})
		return err

	case EventSessionEnd:
		sess, err := db.GetAgentSessionBySession(ctx, pool, ev.Session.Session)
		if err != nil {
			return err
		}
		_, err = db.CloseAgentSession(ctx, pool, sess.ID, optionalTime(ev.Timestamp), ev.Session.CloseReason)
		return err

	case EventToolUse:
		sess, err := ensureSessionForActivity(ctx, pool, ev)
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
			ToolInputRedacted:      ev.Tool.InputRedacted,
			ToolInputOmitted:       ev.Tool.InputOmitted,
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
		sess, err := ensureSessionForActivity(ctx, pool, ev)
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
			ResultRedacted:      ev.Tool.ResultRedacted,
			ResultOmitted:       ev.Tool.ResultOmitted,
			ResultTruncated:     truncated,
			ResultSHA256:        sha,
			ResultOriginalBytes: originalBytes,
			IsError:             ev.Tool.IsError,
			DurationMs:          ev.Tool.DurationMs,
			FinishedAt:          optionalTime(ev.Timestamp),
		})
		return err

	case EventMessageEnd:
		sess, err := ensureSessionForActivity(ctx, pool, ev)
		if err != nil {
			return err
		}
		if ev.Usage.isZero() {
			return nil
		}
		seq, err := db.AllocateAgentSessionSeq(ctx, pool, sess.ID)
		if err != nil {
			return err
		}
		if _, err := db.InsertAgentTurn(ctx, pool, db.InsertAgentTurnParams{
			AgentSessionID:   sess.ID,
			Seq:              seq,
			TurnIndex:        ev.Turn.TurnIndex,
			SourceMessageID:  ev.Turn.SourceMessageID,
			InputTokens:      ev.Usage.InputTokens,
			OutputTokens:     ev.Usage.OutputTokens,
			CacheReadTokens:  ev.Usage.CacheReadTokens,
			CacheWriteTokens: ev.Usage.CacheWriteTokens,
			CostUSD:          ev.Usage.CostUSD,
			Model:            ev.Turn.Model,
			Provider:         ev.Turn.Provider,
			ThinkingLevel:    ev.Turn.ThinkingLevel,
			StopReason:       ev.Turn.StopReason,
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
		sess, err := ensureSessionForActivity(ctx, pool, ev)
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
			SourceMessageID:      ev.Message.SourceMessageID,
			TurnIndex:            ev.Message.TurnIndex,
			InputSource:          ev.Message.InputSource,
			IsFinal:              ev.Message.IsFinal,
			Content:              content,
			ContentRedacted:      ev.Message.ContentRedacted,
			ContentTruncated:     truncated,
			ContentSHA256:        sha,
			ContentOriginalBytes: originalBytes,
			CreatedAt:            optionalTime(ev.Timestamp),
		})
		return err
	}

	return nil
}
