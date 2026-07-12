package webui

import (
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

// Log display types and helpers

type logRow struct {
	ID                  int64
	Time, Source, Event string
	State               string
}

// buildLogRows converts database phase logs into formatted display rows
func buildLogRows(logs []db.PhaseLog) []logRow {
	rows := make([]logRow, 0, len(logs))
	for _, l := range logs {
		source, event := splitLogSource(l.Line)
		rows = append(rows, logRow{
			ID:     l.ID,
			Time:   l.Ts.Format("2006-01-02 15:04:05"),
			Source: source,
			Event:  event,
			State:  classifyLogState(l.Line),
		})
	}
	return rows
}

// splitLogSource extracts the source component and message from a log line
// It handles formats like "[source] message", "source: message", or plain text
func splitLogSource(line string) (string, string) {
	text := strings.TrimSpace(line)
	if text == "" {
		return "system", "—"
	}

	// Try bracket format: [source] message
	if strings.HasPrefix(text, "[") {
		if end := strings.Index(text, "]"); end > 1 && end < 32 {
			return strings.ToLower(strings.TrimSpace(text[1:end])), strings.TrimSpace(text[end+1:])
		}
	}

	// Try colon format: source: message
	if idx := strings.Index(text, ":"); idx > 0 && idx < 24 {
		prefix := strings.TrimSpace(text[:idx])
		if !strings.Contains(prefix, " ") {
			return strings.ToLower(prefix), strings.TrimSpace(text[idx+1:])
		}
	}

	// Check for system keyword
	if strings.Contains(strings.ToLower(text), "system") {
		return "system", text
	}

	// Default to agent
	return "agent", text
}

// classifyLogState determines the status class/state of a log line based on its content
func classifyLogState(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "blocked"):
		return "blocked"
	case strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "fail"):
		return "error"
	case strings.Contains(lower, "warn") || strings.Contains(lower, "warning"):
		return "warning"
	case strings.Contains(lower, "done") || strings.Contains(lower, "complete"):
		return "done"
	case strings.Contains(lower, "running") || strings.Contains(lower, "started"):
		return "running"
	default:
		return "normal"
	}
}
