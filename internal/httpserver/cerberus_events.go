package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/telemetry"
)

// stewardSessionNamePrefix is the fixed prefix used by
// internal/review.stewardSessionName when naming a Cerberus session
// launched for a Steward plan review: "foundry-steward-<planID>-<fingerprint>-<timestamp>".
const stewardSessionNamePrefix = "foundry-steward-"

// stewardSessionPlanID defensively parses the plan id out of a Cerberus
// session name following the Steward naming convention
// (foundry-steward-<planID>-<fingerprint>-<timestamp>). It returns
// ok=false, never panicking or guessing, if session does not have the
// expected prefix, does not have enough '-'-separated segments, or the
// planID segment is not a valid non-negative integer.
func stewardSessionPlanID(session string) (planID int64, ok bool) {
	if !strings.HasPrefix(session, stewardSessionNamePrefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(session, stewardSessionNamePrefix)
	parts := strings.Split(rest, "-")
	if len(parts) < 3 {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

const cerberusTextFlushBytes = 3 * 1024

type cerberusTextBuffer struct {
	content string
}

type compactCerberusEvent struct {
	Type    string          `json:"type"`
	Session string          `json:"session"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Content string          `json:"content,omitempty"`
}

func (s *Server) handleCompactCerberusEvent(ctx context.Context, raw []byte) error {
	var evt compactCerberusEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	if evt.Session == "" || evt.Type == "" {
		return errors.New("session and type required")
	}

	switch evt.Type {
	case "text_delta":
		content := extractCerberusText(raw, evt)
		if content == "" {
			return nil
		}
		if _, err := db.GetPhaseByCerberusSession(ctx, s.pool, evt.Session); err == nil {
			s.bufferCerberusText(evt.Session, content)
			return nil
		} else if !errors.Is(err, db.ErrNotFound) {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"content": content})
		return s.storeAndPublishCerberusEvent(ctx, evt.Session, evt.Type, payload)

	case "session_start":
		s.ingestCerberusTelemetry(ctx, raw, evt)
		return nil

	case "tool_result", "run_complete":
		s.ingestCerberusTelemetry(ctx, raw, evt)
		return nil

	case "message_end", "turn_complete":
		if evt.Type == "message_end" {
			s.ingestCerberusTelemetry(ctx, raw, evt)
			s.applyManagedMessageEndCost(ctx, raw, evt)
		}
		if err := s.flushCerberusText(ctx, evt.Session); err != nil {
			return fmt.Errorf("store event: %w", err)
		}
		if ph, err := db.GetPhaseByCerberusSession(ctx, s.pool, evt.Session); err == nil {
			if evt.Type == "turn_complete" {
				_ = s.storeAndPublishPhaseLog(ctx, ph.WorkflowID, ph.ID, "[turn complete]")
			}
			return nil
		} else if !errors.Is(err, db.ErrNotFound) {
			return err
		}
		if err := s.storeAndPublishCerberusEvent(ctx, evt.Session, evt.Type, json.RawMessage(`{}`)); err != nil {
			return fmt.Errorf("store event: %w", err)
		}
		if evt.Type == "message_end" || evt.Type == "turn_complete" {
			if _, chatErr := db.GetChatSessionByCerberusSession(ctx, s.pool, evt.Session); chatErr == nil {
				s.chatSvc.AssembleMessages(ctx, evt.Session)
			} else {
				s.assembleAndAppend(ctx, evt.Session, true)
			}
		}
		return nil

	case "tool_use":
		s.ingestCerberusTelemetry(ctx, raw, evt)
		payload, ok := compactToolUsePayload(raw)
		if !ok {
			return nil
		}
		if err := s.flushCerberusText(ctx, evt.Session); err != nil {
			return fmt.Errorf("store event: %w", err)
		}
		if err := s.storeAndPublishCerberusEvent(ctx, evt.Session, evt.Type, payload); err != nil {
			return fmt.Errorf("store event: %w", err)
		}
		return nil

	default:
		// Drop high-volume/noisy event types such as raw and most log events.
		return nil
	}
}

func (s *Server) bufferCerberusText(session, content string) {
	var shouldFlush bool
	s.cerbEventsMu.Lock()
	buf := s.cerbBuffers[session]
	if buf == nil {
		buf = &cerberusTextBuffer{}
		s.cerbBuffers[session] = buf
	}
	buf.content += content
	if len(buf.content) >= cerberusTextFlushBytes {
		shouldFlush = true
	}
	s.cerbEventsMu.Unlock()
	if shouldFlush {
		_ = s.flushCerberusText(context.Background(), session)
	}
}

func (s *Server) flushCerberusText(ctx context.Context, session string) error {
	s.cerbEventsMu.Lock()
	buf := s.cerbBuffers[session]
	if buf == nil || buf.content == "" {
		s.cerbEventsMu.Unlock()
		return nil
	}
	content := buf.content
	delete(s.cerbBuffers, session)
	s.cerbEventsMu.Unlock()

	if ph, err := db.GetPhaseByCerberusSession(ctx, s.pool, session); err == nil {
		line := strings.TrimSpace(content)
		if line == "" {
			return nil
		}
		return s.storeAndPublishPhaseLog(ctx, ph.WorkflowID, ph.ID, line)
	}

	payload, _ := json.Marshal(map[string]string{"content": content})
	return s.storeAndPublishCerberusEvent(ctx, session, "text_delta", payload)
}

func (s *Server) storeAndPublishPhaseLog(ctx context.Context, workflowID, phaseID int64, line string) error {
	if err := db.InsertPhaseLog(ctx, s.pool, phaseID, line); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{
		"event":    "log",
		"phase_id": phaseID,
		"line":     line,
		"ts":       time.Now().Format(time.RFC3339),
	})
	s.eventHub.Publish(fmt.Sprintf("wf:%d", workflowID), data)
	return nil
}

// registerExternalCerberusSessionIfUnknown records session as an external
// (non-foundry-managed) Cerberus session when it does not correspond to any
// known workflow phase, spec draft, or chat session. It never returns an
// error to the caller: registration failures must not affect event ingest.
func (s *Server) registerExternalCerberusSessionIfUnknown(ctx context.Context, session, status string) {
	if _, err := db.GetPhaseByCerberusSession(ctx, s.pool, session); err == nil {
		return
	} else if !errors.Is(err, db.ErrNotFound) {
		return
	}
	if _, err := db.GetSpecDraftByCerberusSession(ctx, s.pool, session); err == nil {
		return
	} else if !errors.Is(err, db.ErrNotFound) {
		return
	}
	if _, err := db.GetChatSessionByCerberusSession(ctx, s.pool, session); err == nil {
		return
	} else if !errors.Is(err, db.ErrNotFound) {
		return
	}
	_, _ = db.UpsertExternalCerberusSession(ctx, s.pool, session, "", status)
}

func (s *Server) storeAndPublishCerberusEvent(ctx context.Context, session, eventType string, payload json.RawMessage) error {
	externalStatus := "active"
	if eventType == "turn_complete" {
		externalStatus = "done"
	}
	s.registerExternalCerberusSessionIfUnknown(ctx, session, externalStatus)

	dbEvt, err := db.InsertCerberusEvent(ctx, s.pool, session, eventType, payload)
	if err != nil {
		return err
	}
	if chatSess, err := db.GetChatSessionByCerberusSession(ctx, s.pool, session); err == nil {
		_ = db.TouchChatSession(ctx, s.pool, chatSess.ID)
	}
	sseData, _ := json.Marshal(dbEvt)
	s.eventHub.Publish(session, sseData)
	return nil
}

func extractCerberusText(raw []byte, evt compactCerberusEvent) string {
	if evt.Content != "" {
		return evt.Content
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return ""
	}
	for _, key := range []string{"payload", "data", "delta"} {
		if v, ok := envelope[key]; ok {
			var p struct {
				Content string `json:"content"`
			}
			if json.Unmarshal(v, &p) == nil && p.Content != "" {
				return p.Content
			}
			var s string
			if json.Unmarshal(v, &s) == nil {
				return s
			}
		}
	}
	return ""
}

func compactToolUsePayload(raw []byte) (json.RawMessage, bool) {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil, false
	}
	toolName := stringValue(root, "tool_name", "name", "tool")
	toolInput := anyValue(root, "tool_input", "input", "arguments", "args")
	if payload, ok := root["payload"].(map[string]any); ok {
		if toolName == "" {
			toolName = stringValue(payload, "tool_name", "name", "tool")
		}
		if toolInput == nil {
			toolInput = anyValue(payload, "tool_input", "input", "arguments", "args")
		}
	}
	if toolName != "update_spec" {
		return nil, false
	}

	toolInputString := ""
	switch v := toolInput.(type) {
	case string:
		toolInputString = v
	case nil:
		toolInputString = "{}"
	default:
		b, _ := json.Marshal(v)
		toolInputString = string(b)
	}
	payload, _ := json.Marshal(map[string]string{
		"tool_name":  toolName,
		"tool_input": toolInputString,
	})
	return payload, true
}

func stringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := m[key].(string); ok {
			return s
		}
	}
	return ""
}

func anyValue(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return nil
}

type cerberusUsageFields struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

type compactCerberusFields struct {
	SessionID     string               `json:"session_id,omitempty"`
	Ts            string               `json:"ts,omitempty"`
	Kind          string               `json:"kind,omitempty"`
	Model         string               `json:"model,omitempty"`
	RepoPath      string               `json:"repo_path,omitempty"`
	ParentSession string               `json:"parent_session,omitempty"`
	ToolName      string               `json:"tool_name,omitempty"`
	ToolInput     json.RawMessage      `json:"tool_input,omitempty"`
	ToolCallID    string               `json:"tool_call_id,omitempty"`
	Content       json.RawMessage      `json:"content,omitempty"`
	IsError       *bool                `json:"is_error,omitempty"`
	DurationMs    *int64               `json:"duration_ms,omitempty"`
	Usage         *cerberusUsageFields `json:"usage,omitempty"`
}

func mergeCerberusFields(top, nested compactCerberusFields) compactCerberusFields {
	if top.SessionID == "" {
		top.SessionID = nested.SessionID
	}
	if top.Ts == "" {
		top.Ts = nested.Ts
	}
	if top.Kind == "" {
		top.Kind = nested.Kind
	}
	if top.Model == "" {
		top.Model = nested.Model
	}
	if top.RepoPath == "" {
		top.RepoPath = nested.RepoPath
	}
	if top.ParentSession == "" {
		top.ParentSession = nested.ParentSession
	}
	if top.ToolName == "" {
		top.ToolName = nested.ToolName
	}
	if len(top.ToolInput) == 0 {
		top.ToolInput = nested.ToolInput
	}
	if top.ToolCallID == "" {
		top.ToolCallID = nested.ToolCallID
	}
	if len(top.Content) == 0 {
		top.Content = nested.Content
	}
	if top.IsError == nil {
		top.IsError = nested.IsError
	}
	if top.DurationMs == nil {
		top.DurationMs = nested.DurationMs
	}
	if top.Usage == nil {
		top.Usage = nested.Usage
	}
	return top
}

func extractCerberusFields(raw []byte) compactCerberusFields {
	var top compactCerberusFields
	_ = json.Unmarshal(raw, &top)

	var envelope struct {
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &envelope) == nil && len(envelope.Payload) > 0 {
		var nested compactCerberusFields
		if json.Unmarshal(envelope.Payload, &nested) == nil {
			top = mergeCerberusFields(top, nested)
		}
	}
	return top
}

func cerberusRawToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func optionalCerberusString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func parseCerberusTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

func buildCerberusTelemetryEvent(eventType, session string, fields compactCerberusFields) (telemetry.Event, bool) {
	ts := parseCerberusTimestamp(fields.Ts)

	switch eventType {
	case "session_start":
		if fields.SessionID == "" {
			return telemetry.Event{}, false
		}
		kind := telemetry.SessionKind(fields.Kind)
		if kind == "" {
			kind = telemetry.SessionKindUnknown
		}
		return telemetry.Event{
			Type: telemetry.EventSessionStart,
			Session: telemetry.Session{
				Session:         session,
				SourceSessionID: fields.SessionID,
				Origin:          "cerberus",
				Kind:            kind,
			},
			Attribution: telemetry.Attribution{
				RepoPath:      optionalCerberusString(fields.RepoPath),
				Model:         optionalCerberusString(fields.Model),
				ParentSession: optionalCerberusString(fields.ParentSession),
			},
			Timestamp: ts,
		}, true

	case "tool_use":
		if fields.ToolName == "" {
			return telemetry.Event{}, false
		}
		return telemetry.Event{
			Type:    telemetry.EventToolUse,
			Session: telemetry.Session{Session: session},
			Tool: telemetry.Tool{
				ToolCallID: optionalCerberusString(fields.ToolCallID),
				ToolName:   fields.ToolName,
				Input:      cerberusRawToString(fields.ToolInput),
			},
			Timestamp: ts,
		}, true

	case "tool_result":
		if fields.ToolName == "" {
			return telemetry.Event{}, false
		}
		return telemetry.Event{
			Type:    telemetry.EventToolResult,
			Session: telemetry.Session{Session: session},
			Tool: telemetry.Tool{
				ToolCallID: optionalCerberusString(fields.ToolCallID),
				ToolName:   fields.ToolName,
				Result:     cerberusRawToString(fields.Content),
				IsError:    fields.IsError,
				DurationMs: fields.DurationMs,
			},
			Timestamp: ts,
		}, true

	case "message_end":
		usage := fields.Usage
		if usage == nil {
			usage = &cerberusUsageFields{}
		}
		return telemetry.Event{
			Type:    telemetry.EventMessageEnd,
			Session: telemetry.Session{Session: session},
			Usage: telemetry.Usage{
				InputTokens:      usage.InputTokens,
				OutputTokens:     usage.OutputTokens,
				CacheReadTokens:  usage.CacheReadTokens,
				CacheWriteTokens: usage.CacheWriteTokens,
				CostUSD:          usage.CostUSD,
			},
			Timestamp: ts,
		}, true

	case "run_complete":
		return telemetry.Event{
			Type:      telemetry.EventSessionEnd,
			Session:   telemetry.Session{Session: session},
			Timestamp: ts,
		}, true
	}

	return telemetry.Event{}, false
}

func (s *Server) resolveManagedCerberusAttribution(ctx context.Context, session string) (telemetry.Attribution, bool) {
	ph, err := db.GetPhaseByCerberusSession(ctx, s.pool, session)
	if err != nil {
		return telemetry.Attribution{}, false
	}
	_, _, repo, err := s.workflowRepository(ctx, ph.WorkflowID)
	if err != nil {
		return telemetry.Attribution{}, false
	}
	phaseID := ph.ID
	repoID := repo.ID
	return telemetry.Attribution{RepositoryID: &repoID, PhaseID: &phaseID, RepoPath: repo.LocalPath}, true
}

func applyManagedCerberusAttribution(current, managed telemetry.Attribution) telemetry.Attribution {
	current.RepositoryID = managed.RepositoryID
	current.PhaseID = managed.PhaseID
	current.RepoPath = managed.RepoPath
	return current
}

func managedMessageEndCostDecision(phaseID int64, phaseErr error, costUSD float64) (int64, float64, bool) {
	if phaseErr != nil {
		return 0, 0, false
	}
	if costUSD == 0 {
		return 0, 0, false
	}
	return phaseID, costUSD, true
}

func (s *Server) applyManagedMessageEndCost(ctx context.Context, raw []byte, evt compactCerberusEvent) {
	fields := extractCerberusFields(raw)
	costUSD := 0.0
	if fields.Usage != nil {
		costUSD = fields.Usage.CostUSD
	}
	ph, err := db.GetPhaseByCerberusSession(ctx, s.pool, evt.Session)
	phaseID, deltaUSD, ok := managedMessageEndCostDecision(ph.ID, err, costUSD)
	if !ok {
		return
	}
	if err := db.AddPhaseCost(ctx, s.pool, phaseID, deltaUSD); err != nil {
		log.Printf("apply phase cost: %v", err)
	}
}

func (s *Server) ingestCerberusTelemetry(ctx context.Context, raw []byte, evt compactCerberusEvent) {
	s.ingestCerberusTelemetryWith(ctx, raw, evt, telemetry.Ingest)
}

func (s *Server) ingestCerberusTelemetryWith(ctx context.Context, raw []byte, evt compactCerberusEvent, ingest func(context.Context, *pgxpool.Pool, telemetry.Event) error) {
	fields := extractCerberusFields(raw)
	tev, ok := buildCerberusTelemetryEvent(evt.Type, evt.Session, fields)
	if !ok {
		return
	}
	phaseAttributed := false
	if tev.Type == telemetry.EventSessionStart {
		if attribution, ok := s.resolveManagedCerberusAttribution(ctx, evt.Session); ok {
			tev.Attribution = applyManagedCerberusAttribution(tev.Attribution, attribution)
			phaseAttributed = true
		}
	}
	if err := ingest(ctx, s.pool, tev); err != nil && !errors.Is(err, telemetry.ErrDuplicateEvent) {
		log.Printf("cerberus telemetry ingest: %v", err)
	}
	if tev.Type == telemetry.EventSessionStart && !phaseAttributed {
		s.linkStewardSessionToPlan(ctx, evt.Session)
	}
}

// linkStewardSessionToPlan records a system_derived session_plan_links
// row for session if, and only if, session's name follows the Steward
// naming convention (foundry-steward-<planID>-<fingerprint>-<timestamp>)
// used by internal/review.stewardSessionName. It is only meaningful to
// call for session-start events, and only when the session was not
// already resolved as a phase-launched session by
// resolveManagedCerberusAttribution, so a Steward review session never
// gets misattributed to a phase and a phase-launched session is never
// double-linked. A malformed or non-numeric plan id segment is ignored
// (logged, never treated as an error and never guessed at); any
// resulting session_plan_links insert failure (e.g. the parsed plan id
// does not name a real plan) is likewise logged and swallowed, since
// this is a best-effort attribution that must never affect event
// ingest.
func (s *Server) linkStewardSessionToPlan(ctx context.Context, session string) {
	planID, ok := stewardSessionPlanID(session)
	if !ok {
		return
	}
	agentSession, err := db.GetAgentSessionBySession(ctx, s.pool, session)
	if err != nil {
		if !errors.Is(err, db.ErrNotFound) {
			log.Printf("link steward session %q to plan %d: get agent session: %v", session, planID, err)
		}
		return
	}
	if _, err := db.CreateSessionPlanLink(ctx, s.pool, db.CreateSessionPlanLinkParams{
		AgentSessionID: agentSession.ID,
		PlanID:         planID,
		Method:         db.SessionPlanLinkMethodSystemDerived,
	}); err != nil {
		log.Printf("link steward session %q to plan %d: %v", session, planID, err)
	}
}
