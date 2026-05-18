package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

const (
	cerberusTextFlushAfter = 150 * time.Millisecond
	cerberusTextFlushBytes = 3 * 1024
)

type cerberusTextBuffer struct {
	content string
	timer   *time.Timer
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
		s.bufferCerberusText(evt.Session, content)
		return nil

	case "message_end", "turn_complete":
		if err := s.flushCerberusText(ctx, evt.Session); err != nil {
			return fmt.Errorf("store event: %w", err)
		}
		if ph, err := db.GetPhaseByCerberusSession(ctx, s.pool, evt.Session); err == nil {
			if evt.Type == "turn_complete" {
				_ = s.storeAndPublishPhaseLog(ctx, ph.WorkflowID, ph.ID, "[turn complete]")
			}
			return nil
		}
		if err := s.storeAndPublishCerberusEvent(ctx, evt.Session, evt.Type, json.RawMessage(`{}`)); err != nil {
			return fmt.Errorf("store event: %w", err)
		}
		if evt.Type == "turn_complete" {
			s.assembleAndAppend(ctx, evt.Session, true)
		}
		return nil

	case "tool_use":
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
		// Drop high-volume/noisy event types such as raw, tool_result, and most log events.
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
		if buf.timer != nil {
			buf.timer.Stop()
			buf.timer = nil
		}
	} else if buf.timer == nil {
		buf.timer = time.AfterFunc(cerberusTextFlushAfter, func() {
			if err := s.flushCerberusText(context.Background(), session); err != nil {
				// Best effort; later boundary flushes will retry remaining text.
				fmt.Printf("flush cerberus text: %v\n", err)
			}
		})
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
	if buf.timer != nil {
		buf.timer.Stop()
	}
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

func (s *Server) storeAndPublishCerberusEvent(ctx context.Context, session, eventType string, payload json.RawMessage) error {
	dbEvt, err := db.InsertCerberusEvent(ctx, s.pool, session, eventType, payload)
	if err != nil {
		return err
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
