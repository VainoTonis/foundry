package webui

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.New("ui").Funcs(template.FuncMap{
	"date":     func(t time.Time) string { return t.Format("2006-01-02") },
	"datetime": func(t time.Time) string { return t.Format("2006-01-02 15:04:05") },
	"ptime": func(t *time.Time) string {
		if t == nil {
			return "—"
		}
		return t.Format("2006-01-02 15:04:05")
	},
	"money": func(f *float64) string {
		if f == nil {
			return "—"
		}
		return fmt.Sprintf("$%.4f", *f)
	},
	"strptr": func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	},
	"json": func(v any) string {
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	},
	"cleanSessionURL": func(session string) string {
		return "/api/cerberus/sessions/" + session + "/clean"
	},
	"phaseProgress":    phaseProgress,
	"phaseFillClass":   phaseFillClass,
	"phaseStatusLabel": phaseStatusLabel,
	"diffSummary":      buildDiffSummary,
	"diffRows":         buildDiffRows,
	"logRows":          buildLogRows,
}).ParseFS(templateFS, "templates/*.html"))

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

// Diff display helpers
type diffSummary struct {
	Path      string
	Summary   string
	Added     int
	Removed   int
	Conflicts int
	DOMHooks  int
}

type diffRow struct{ Kind, Marker, Text string }

type logRow struct {
	ID                  int64
	Time, Source, Event string
	State               string
}

func buildDiffSummary(raw string) diffSummary {
	s := diffSummary{Path: "unknown file", Summary: "No changed lines", DOMHooks: 0}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "+++ b/") && s.Path == "unknown file" {
			s.Path = strings.TrimPrefix(strings.TrimSpace(line), "+++ b/")
		} else if strings.HasPrefix(line, "diff --git ") && s.Path == "unknown file" {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				s.Path = strings.TrimPrefix(parts[3], "b/")
			}
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			s.Added++
			if diffLineTouchesDOMHook(line) {
				s.DOMHooks++
			}
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			s.Removed++
			if diffLineTouchesDOMHook(line) {
				s.DOMHooks++
			}
		}
		if strings.HasPrefix(trimmed, "<<<<<<<") || strings.HasPrefix(trimmed, "=======") || strings.HasPrefix(trimmed, ">>>>>>>") {
			s.Conflicts++
		}
	}
	if s.Added > 0 || s.Removed > 0 {
		s.Summary = fmt.Sprintf("%d changed lines", s.Added+s.Removed)
	}
	return s
}

func buildDiffRows(raw string) []diffRow {
	lines := strings.Split(raw, "\n")
	rows := make([]diffRow, 0, len(lines))
	for _, line := range lines {
		row := diffRow{Kind: "context", Marker: " ", Text: line}
		switch {
		case strings.HasPrefix(line, "@@"):
			row.Kind, row.Marker = "hunk", "@"
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			row.Kind, row.Marker, row.Text = "add", "+", strings.TrimPrefix(line, "+")
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			row.Kind, row.Marker, row.Text = "del", "-", strings.TrimPrefix(line, "-")
		case strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			row.Kind, row.Marker = "meta", "•"
		}
		rows = append(rows, row)
	}
	return rows
}

func diffLineTouchesDOMHook(line string) bool {
	lower := strings.ToLower(line)
	for _, token := range []string{"data-", "id=", "hx-", "aria-", "class="} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func buildLogRows(logs []db.PhaseLog) []logRow {
	rows := make([]logRow, 0, len(logs))
	for _, l := range logs {
		source, event := splitLogSource(l.Line)
		rows = append(rows, logRow{ID: l.ID, Time: l.Ts.Format("2006-01-02 15:04:05"), Source: source, Event: event, State: classifyLogState(l.Line)})
	}
	return rows
}

func splitLogSource(line string) (string, string) {
	text := strings.TrimSpace(line)
	if text == "" {
		return "system", "—"
	}
	if strings.HasPrefix(text, "[") {
		if end := strings.Index(text, "]"); end > 1 && end < 32 {
			return strings.ToLower(strings.TrimSpace(text[1:end])), strings.TrimSpace(text[end+1:])
		}
	}
	if idx := strings.Index(text, ":"); idx > 0 && idx < 24 {
		prefix := strings.TrimSpace(text[:idx])
		if !strings.Contains(prefix, " ") {
			return strings.ToLower(prefix), strings.TrimSpace(text[idx+1:])
		}
	}
	if strings.Contains(strings.ToLower(text), "system") {
		return "system", text
	}
	return "agent", text
}

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

type shellData struct{ Page, Fragment string }

func (s *Handler) renderShell(w http.ResponseWriter, page, fragment string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "shell", shellData{Page: page, Fragment: fragment}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
