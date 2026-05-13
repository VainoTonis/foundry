package cerberus

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Client wraps the cerberus binary.
type Client struct {
	bin   string // path to cerberus binary
	image string // optional: container image; empty = cerberus default
	model string // optional: model override; empty = cerberus default
}

func New(bin, image, model string) *Client {
	return &Client{bin: bin, image: image, model: model}
}

// Start launches a cerberus session with the given prompt. Blocking — run in a goroutine.
// cerberus start --name <session> --prompt-file <f> [--image <image>]
func (c *Client) Start(ctx context.Context, session, prompt string) error {
	f, err := os.CreateTemp("", "foundry-prompt-*.txt")
	if err != nil {
		return fmt.Errorf("create prompt file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(prompt); err != nil {
		f.Close()
		return fmt.Errorf("write prompt file: %w", err)
	}
	f.Close()

	args := []string{"start", "--name", session, "--prompt-file", f.Name()}
	if c.image != "" {
		args = append(args, "--image", c.image)
	}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	return c.run(ctx, args...)
}

// Status returns the raw status string from cerberus.
// cerberus status --name <session>
func (c *Client) Status(ctx context.Context, session string) (string, error) {
	out, err := c.output(ctx, "status", "--name", session)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Logs returns the full log output for a session.
// cerberus logs --name <session>
func (c *Client) Logs(ctx context.Context, session string) (string, error) {
	return c.output(ctx, "logs", "--name", session)
}

// Diff returns the git diff produced by the cerberus session.
// cerberus review --name <session> --diff
func (c *Client) Diff(ctx context.Context, session string) (string, error) {
	return c.output(ctx, "review", "--name", session, "--diff")
}

// Review returns the plain review summary (files touched, commit hash).
// cerberus review --name <session>
func (c *Client) Review(ctx context.Context, session string) (string, error) {
	return c.output(ctx, "review", "--name", session)
}

// Clean removes the cerberus session.
// cerberus clean --name <session>
func (c *Client) Clean(ctx context.Context, session string) error {
	return c.run(ctx, "clean", "--name", session)
}

// SessionName builds the canonical session name for a phase.
func SessionName(specID int64, position int) string {
	return fmt.Sprintf("foundry-%d-p%d", specID, position)
}

func (c *Client) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cerberus %s: %w — %s", strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

func (c *Client) output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cerberus %s: %w — %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

// DraftSessionName builds the canonical session name for a spec draft.
func DraftSessionName(draftID int64) string {
	return fmt.Sprintf("foundry-draft-%d", draftID)
}

// Chat starts an interactive cerberus session (first turn). Blocking — run in a goroutine.
// Returns the agent's response text from the first turn (prefix stripped).
// extraMounts: optional list of "host:container" paths mounted read-only into the container.
func (c *Client) Chat(ctx context.Context, session, prompt string, extraMounts []string) (string, error) {
	args := []string{"chat", "--name", session, "--prompt", prompt,
		"--system-prompt", specBuilderSystemPrompt}
	for _, m := range extraMounts {
		args = append(args, "--mount", m)
	}
	if c.image != "" {
		args = append(args, "--image", c.image)
	}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	raw, err := c.output(ctx, args...)
	if err != nil {
		return "", err
	}
	return stripCerberusOutput(session, raw), nil
}

// specBuilderSystemPrompt replaces pi's default code-agent system prompt for spec builder sessions.
// Tools are kept so the agent can read the project, but it must only write a spec, not code.
const specBuilderSystemPrompt = `You are a spec writer. Your only job is to help the user write a Foundry spec — a markdown document that describes what should be built and how it breaks into phases.

You have read access to the filesystem. Use it to read the project if the user points you at one — README, source files, structure — to understand context. Do NOT write or modify any files. Do NOT run build commands or tests.

Respond conversationally. Ask clarifying questions when needed. When you have enough information, produce the spec.`

// stripCerberusOutput extracts the agent response from cerberus stdout.
// cerberus prefixes each delta chunk with "[session] " but blank lines and
// continuation lines within a response have no prefix. We find the span
// between the first prefixed line and the last non-waiting prefixed line,
// return everything in that span with the prefix stripped where present.
func stripCerberusOutput(session, raw string) string {
	prefix := "[" + session + "] "
	waiting := prefix + "waiting"
	allLines := strings.Split(raw, "\n")

	// find first and last index of a prefixed non-waiting line
	first, last := -1, -1
	for i, line := range allLines {
		if strings.HasPrefix(line, prefix) && !strings.HasPrefix(line, waiting) {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 {
		return ""
	}

	// collect lines in [first, last], stripping prefix where present
	var out []string
	for _, line := range allLines[first : last+1] {
		out = append(out, strings.TrimPrefix(line, prefix))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Message sends a follow-up message in an existing interactive session.
// Returns the agent response text with session prefix and status lines stripped.
// cerberus message --name <session> --message <msg>
func (c *Client) Message(ctx context.Context, session, msg string) (string, error) {
	raw, err := c.output(ctx, "message", "--name", session, "--message", msg)
	if err != nil {
		return "", err
	}
	return stripCerberusOutput(session, raw), nil
}

// Close commits any changes and cleans up an interactive session.
// cerberus close --name <session>
func (c *Client) Close(ctx context.Context, session string) error {
	return c.run(ctx, "close", "--name", session)
}
