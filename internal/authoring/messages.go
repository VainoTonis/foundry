package authoring

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/db"
)

// AppendMessage appends a new message to the existing message array
// and returns the updated JSON bytes.
func AppendMessage(existing []byte, role, content string) []byte {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Ts      string `json:"ts"`
	}
	var msgs []msg
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &msgs)
	}
	msgs = append(msgs, msg{Role: role, Content: content, Ts: time.Now().Format(time.RFC3339)})
	b, _ := json.Marshal(msgs)
	return b
}

// AssembleAndAppendMessages assembles assistant messages from cerberus events
// and appends them to the draft's message history.
func AssembleAndAppendMessages(ctx context.Context, pool *pgxpool.Pool, session string, isTurnComplete bool) {
	if !isTurnComplete {
		return
	}

	drafts, _ := db.ListSpecDrafts(ctx, pool)
	var draft *db.SpecDraft
	for _, d := range drafts {
		if d.CerberusSession == session {
			draft = &d
			break
		}
	}
	if draft == nil {
		return
	}

	events, err := db.ListCerberusEvents(ctx, pool, session, 0)
	if err != nil {
		log.Printf("assemble messages: %v", err)
		return
	}

	var buf strings.Builder
	var assistantMsgs []string
	for _, e := range events {
		switch e.EventType {
		case "text_delta":
			var p struct {
				Content string `json:"content"`
			}
			json.Unmarshal(e.Payload, &p)
			buf.WriteString(p.Content)
		case "message_end":
			if buf.Len() > 0 {
				assistantMsgs = append(assistantMsgs, buf.String())
				buf.Reset()
			}
		case "tool_use":
			var p struct {
				ToolName  string `json:"tool_name"`
				ToolInput string `json:"tool_input"`
			}
			json.Unmarshal(e.Payload, &p)
			if p.ToolName == "update_spec" {
				var toolInput struct {
					Content string `json:"content"`
				}
				if err := json.Unmarshal([]byte(p.ToolInput), &toolInput); err == nil {
					assistantMsgs = append(assistantMsgs, toolInput.Content)
				}
			}
		}
	}
	if buf.Len() > 0 {
		assistantMsgs = append(assistantMsgs, buf.String())
	}

	msgs := draft.Messages
	for _, content := range assistantMsgs {
		msgs = AppendMessage(msgs, "assistant", content)
	}
	if len(assistantMsgs) > 0 {
		db.UpdateSpecDraft(ctx, pool, draft.ID, db.UpdateSpecDraftParams{Messages: msgs})
	}
	db.DeleteCerberusEvents(ctx, pool, session)
}
