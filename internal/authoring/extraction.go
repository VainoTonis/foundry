package authoring

import (
	"encoding/json"
	"strings"
)

// ExtractFinalSpec finds the save-ready spec from draft messages by searching backwards
// from the end for the first assistant message that contains a save-ready spec.
func ExtractFinalSpec(messages []byte) string {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []msg
	if err := json.Unmarshal(messages, &msgs); err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		if spec := ExtractSaveReadyMarkdownSpec(msgs[i].Content); spec != "" {
			return spec
		}
	}
	return ""
}

// ExtractSaveReadyMarkdownSpec extracts a save-ready spec from text content.
// It tries multiple strategies: direct spec, markdown fence, FINAL SPEC marker, and title-based extraction.
func ExtractSaveReadyMarkdownSpec(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if IsSaveReadySpec(content) {
		return content
	}
	if spec := extractSpecFromMarkdownFence(content); spec != "" {
		return spec
	}
	if idx := strings.Index(content, "FINAL SPEC:"); idx != -1 {
		after := content[idx+len("FINAL SPEC:"):]
		if spec := extractSpecFromMarkdownFence(after); spec != "" {
			return spec
		}
		if spec := extractSpecFromTitle(after); spec != "" {
			return spec
		}
		return strings.TrimSpace(after)
	}
	return extractSpecFromTitle(content)
}

func extractSpecFromMarkdownFence(content string) string {
	remaining := content
	for {
		start := strings.Index(remaining, "```")
		if start == -1 {
			return ""
		}
		afterTicks := remaining[start+3:]
		lineEnd := strings.IndexByte(afterTicks, '\n')
		if lineEnd == -1 {
			return ""
		}
		info := strings.TrimSpace(afterTicks[:lineEnd])
		body := afterTicks[lineEnd+1:]
		end := strings.Index(body, "```")
		if end == -1 {
			return ""
		}
		if info == "" || strings.EqualFold(info, "markdown") || strings.EqualFold(info, "md") {
			candidate := strings.TrimSpace(body[:end])
			if IsSaveReadySpec(candidate) {
				return candidate
			}
		}
		remaining = body[end+3:]
	}
}

func extractSpecFromTitle(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			candidate := strings.TrimSpace(strings.Join(lines[i:], "\n"))
			if IsSaveReadySpec(candidate) {
				return candidate
			}
		}
	}
	return ""
}

// IsSaveReadySpec checks if content looks like a save-ready spec.
// A save-ready spec starts with a level-1 heading and has at least one Phase 1.
func IsSaveReadySpec(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "# ") && strings.Contains(content, "\n## Phase 1:")
}

// ExtractSpecTitle extracts the top-level title (# heading) from spec content.
func ExtractSpecTitle(specContent string) string {
	for _, line := range strings.Split(specContent, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "## ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}
