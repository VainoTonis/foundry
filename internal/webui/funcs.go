package webui

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
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

func templateMarkdown(s string) template.HTML {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var b strings.Builder
	var para []string
	inCode := false
	listKind := ""

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		b.WriteString("<p>")
		b.WriteString(markdownInline(strings.Join(para, " ")))
		b.WriteString("</p>")
		para = nil
	}
	flushList := func() {
		if listKind == "" {
			return
		}
		b.WriteString("</")
		b.WriteString(listKind)
		b.WriteString(">")
		listKind = ""
	}
	openList := func(kind string) {
		if listKind == kind {
			return
		}
		flushList()
		b.WriteString("<")
		b.WriteString(kind)
		b.WriteString(">")
		listKind = kind
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				b.WriteString("</code></pre>")
				inCode = false
			} else {
				flushPara()
				flushList()
				b.WriteString("<pre><code>")
				inCode = true
			}
			continue
		}
		if inCode {
			b.WriteString(html.EscapeString(line))
			b.WriteByte('\n')
			continue
		}
		if trimmed == "" {
			flushPara()
			flushList()
			continue
		}
		if level, text, ok := markdownHeading(trimmed); ok {
			flushPara()
			flushList()
			fmt.Fprintf(&b, "<h%d>%s</h%d>", level, markdownInline(text), level)
			continue
		}
		if text, ok := markdownUnorderedItem(trimmed); ok {
			flushPara()
			openList("ul")
			b.WriteString("<li>")
			b.WriteString(markdownInline(text))
			b.WriteString("</li>")
			continue
		}
		if text, ok := markdownOrderedItem(trimmed); ok {
			flushPara()
			openList("ol")
			b.WriteString("<li>")
			b.WriteString(markdownInline(text))
			b.WriteString("</li>")
			continue
		}
		if text, ok := markdownLabelItem(trimmed); ok {
			flushPara()
			openList("ul")
			b.WriteString("<li>")
			b.WriteString(markdownInline(text))
			b.WriteString("</li>")
			continue
		}
		flushList()
		para = append(para, trimmed)
	}
	flushPara()
	flushList()
	if inCode {
		b.WriteString("</code></pre>")
	}
	return template.HTML(b.String())
}

func markdownUnorderedItem(s string) (string, bool) {
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") {
		return strings.TrimSpace(s[2:]), true
	}
	return "", false
}

func markdownOrderedItem(s string) (string, bool) {
	dot := strings.IndexByte(s, '.')
	if dot <= 0 || dot+1 >= len(s) || s[dot+1] != ' ' {
		return "", false
	}
	for _, ch := range s[:dot] {
		if ch < '0' || ch > '9' {
			return "", false
		}
	}
	return strings.TrimSpace(s[dot+2:]), true
}

func markdownLabelItem(s string) (string, bool) {
	if !strings.HasPrefix(s, "**") {
		return "", false
	}
	close := strings.Index(s[2:], "**")
	if close < 0 {
		return "", false
	}
	label := s[2 : 2+close]
	if !strings.HasSuffix(label, ":") {
		return "", false
	}
	return s, true
}

func markdownHeading(s string) (int, string, bool) {
	level := 0
	for level < len(s) && level < 4 && s[level] == '#' {
		level++
	}
	if level == 0 || level >= len(s) || s[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(s[level+1:]), true
}

func markdownInline(s string) string {
	var b strings.Builder
	for {
		code := strings.IndexByte(s, '`')
		bold := strings.Index(s, "**")
		if code < 0 && bold < 0 {
			b.WriteString(html.EscapeString(s))
			return b.String()
		}
		if code >= 0 && (bold < 0 || code < bold) {
			end := strings.IndexByte(s[code+1:], '`')
			if end < 0 {
				b.WriteString(html.EscapeString(s))
				return b.String()
			}
			b.WriteString(html.EscapeString(s[:code]))
			b.WriteString("<code>")
			b.WriteString(html.EscapeString(s[code+1 : code+1+end]))
			b.WriteString("</code>")
			s = s[code+1+end+1:]
			continue
		}
		end := strings.Index(s[bold+2:], "**")
		if end < 0 {
			b.WriteString(html.EscapeString(s))
			return b.String()
		}
		b.WriteString(html.EscapeString(s[:bold]))
		b.WriteString("<strong>")
		b.WriteString(html.EscapeString(s[bold+2 : bold+2+end]))
		b.WriteString("</strong>")
		s = s[bold+2+end+2:]
	}
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
