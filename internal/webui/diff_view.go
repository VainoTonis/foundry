package webui

import (
	"fmt"
	"strings"
)

// Diff display types and helpers

type diffSummary struct {
	Path      string
	Summary   string
	Added     int
	Removed   int
	Conflicts int
	DOMHooks  int
}

type diffRow struct {
	Kind   string
	Marker string
	Text   string
}

// buildDiffSummary parses a unified diff string and extracts summary statistics
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

// buildDiffRows parses a unified diff string into display rows with line classification
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

// diffLineTouchesDOMHook checks if a diff line contains DOM-relevant attributes or identifiers
func diffLineTouchesDOMHook(line string) bool {
	lower := strings.ToLower(line)
	for _, token := range []string{"data-", "id=", "hx-", "aria-", "class="} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}
