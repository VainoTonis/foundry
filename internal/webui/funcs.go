package webui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Generic template functions for formatting and display logic

func templateDate(t time.Time) string {
	return t.Format("2006-01-02")
}

func templateDateTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

func templatePTime(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Format("2006-01-02 15:04:05")
}

func templateMoney(f *float64) string {
	if f == nil {
		return "—"
	}
	return fmt.Sprintf("$%.4f", *f)
}

func templateStrPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func templateJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func templateCleanSessionURL(session string) string {
	return "/api/cerberus/sessions/" + session + "/clean"
}

// Phase-related template functions

func phaseStatusLabel(status string) string {
	if status == "" {
		return "unknown"
	}
	return strings.ReplaceAll(status, "_", " ")
}

func phaseProgress(status string) int {
	switch status {
	case "done", "failed":
		return 100
	case "running":
		return 40
	default:
		return 0
	}
}

func phaseFillClass(status string) string {
	switch status {
	case "done":
		return "phase-progress-done"
	case "running":
		return "phase-progress-running"
	case "failed":
		return "phase-progress-failed"
	case "awaiting_review":
		return "phase-progress-review"
	default:
		return "phase-progress-muted"
	}
}
