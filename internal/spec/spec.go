package spec

import (
	"regexp"
	"strings"
)

// Phase is a parsed phase from a spec markdown document.
type Phase struct {
	Position int    // 1-based
	Name     string // text after "## Phase N: "
	Goal     string // body of the section (trimmed)
}

// Parsed holds the result of parsing a spec document.
type Parsed struct {
	GlobalContext string
	Phases        []Phase
}

var phaseHeader = regexp.MustCompile(`(?i)^##\s+Phase\s+(\d+)\s*:\s*(.+)$`)

// Parse splits a spec markdown document into global context and phases.
// Sections starting with "## Phase N:" become phases.
// Everything before the first phase header is global context.
func Parse(content string) Parsed {
	lines := strings.Split(content, "\n")

	var globalLines []string
	type section struct {
		name  string
		pos   int
		lines []string
	}
	var sections []section
	var cur *section

	for _, line := range lines {
		if m := phaseHeader.FindStringSubmatch(line); m != nil {
			// save current section
			if cur != nil {
				sections = append(sections, *cur)
			}
			pos := 0
			for _, c := range m[1] {
				pos = pos*10 + int(c-'0')
			}
			cur = &section{name: strings.TrimSpace(m[2]), pos: pos}
			continue
		}
		if cur == nil {
			globalLines = append(globalLines, line)
		} else {
			cur.lines = append(cur.lines, line)
		}
	}
	if cur != nil {
		sections = append(sections, *cur)
	}

	p := Parsed{
		GlobalContext: strings.TrimSpace(strings.Join(globalLines, "\n")),
	}
	for _, s := range sections {
		p.Phases = append(p.Phases, Phase{
			Position: s.pos,
			Name:     s.name,
			Goal:     strings.TrimSpace(strings.Join(s.lines, "\n")),
		})
	}
	return p
}

// BuildPrompt composes the prompt sent to cerberus for a phase:
// global context (if any) prepended, then default intent references,
// then the phase goal, then the track overlay.
func BuildPrompt(globalContext, goal, trackOverlay string) string {
	var b strings.Builder
	if globalContext != "" {
		b.WriteString(globalContext)
		b.WriteString("\n\n---\n\n")
	}
	b.WriteString(DefaultIntentReferences)
	b.WriteString("\n\n---\n\n")
	b.WriteString(goal)
	if trackOverlay != "" {
		b.WriteString("\n\n---\n\n")
		b.WriteString(trackOverlay)
	}
	return b.String()
}

const DefaultIntentReferences = `## Intent References

Before making changes, read these files:

- intent/Agent Workflow.md
- intent/Product Model.md
- intent/Principles.md
- intent/Constraints.md
- intent/Open Questions.md

Use intent as guidance. Implement only the phase goal.`

const OverlayPoC = "Make it work. Prove the concept. Structure and tests are secondary."
const OverlayPolish = "Write this properly. Clean structure, explicit error handling, proper tests. This goes long-term into the codebase."
