package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Result is the structured output from the review LLM.
type Result struct {
	Verdict         string   `json:"verdict"` // "pass" | "fail"
	Notes           string   `json:"notes"`
	Decisions       []string `json:"decisions"`
	Rationale       string   `json:"rationale"`
	AdjustedPrompt  string   `json:"adjusted_prompt"`
	DecisionSummary string   `json:"decision_summary"`
	FilesTouched    []string `json:"files_touched"`
}

// Config holds reviewer configuration.
type Config struct {
	// BaseURL is the OpenAI-compatible API base (e.g. "https://api.openai.com/v1")
	BaseURL string
	APIKey  string
	Model   string // e.g. "claude-haiku-4-5"
}

// Reviewer calls an OpenAI-compatible chat endpoint to review a phase diff.
type Reviewer struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) *Reviewer {
	return &Reviewer{cfg: cfg, client: &http.Client{}}
}

const systemPrompt = `You are a code review agent for a spec-driven development system.
You will receive a phase goal and a git diff. Output ONLY valid JSON matching this schema:
{
  "verdict": "pass" | "fail",
  "notes": "short explanation",
  "decisions": ["key choice 1", "key choice 2"],
  "rationale": "why this approach",
  "adjusted_prompt": "if fail: revised prompt for retry; else empty string",
  "decision_summary": "one paragraph summary of what happened",
  "files_touched": ["path/to/file.go"]
}`

// Review runs the LLM review for a phase.
// track is "poc" or "polish". testOutput is optional (used for polish track).
func (r *Reviewer) Review(ctx context.Context, goal, diff, track, testOutput string) (Result, error) {
	userMsg := buildUserMessage(goal, diff, track, testOutput)

	reqBody, err := json.Marshal(map[string]any{
		"model": r.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userMsg},
		},
		"temperature": 0,
	})
	if err != nil {
		return Result{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(r.cfg.BaseURL, "/")+"/chat/completions",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return Result{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body map[string]any
		json.NewDecoder(resp.Body).Decode(&body)
		return Result{}, fmt.Errorf("reviewer API status %d: %v", resp.StatusCode, body)
	}

	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return Result{}, fmt.Errorf("decode response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return Result{}, fmt.Errorf("no choices in response")
	}

	content := apiResp.Choices[0].Message.Content
	// strip possible markdown code fences
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
		}
		if len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "```") {
			lines = lines[:len(lines)-1]
		}
		content = strings.Join(lines, "\n")
	}

	var result Result
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return Result{}, fmt.Errorf("parse verdict JSON: %w — raw: %s", err, content)
	}
	return result, nil
}

func buildUserMessage(goal, diff, track, testOutput string) string {
	var b strings.Builder
	b.WriteString("## Phase goal\n\n")
	b.WriteString(goal)
	b.WriteString("\n\n## Diff\n\n```diff\n")
	b.WriteString(diff)
	b.WriteString("\n```\n")
	if track == "polish" && testOutput != "" {
		b.WriteString("\n## Test output\n\n```\n")
		b.WriteString(testOutput)
		b.WriteString("\n```\n")
	}
	if track == "poc" {
		b.WriteString("\n## Review bar\nPoC track: verdict is pass if the diff plausibly accomplishes the goal. Structure and tests are secondary.")
	} else {
		b.WriteString("\n## Review bar\nPolish track: pass only if diff accomplishes the goal, tests cover the public surface, no tests against unexported symbols.")
	}
	return b.String()
}
