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
	bin     string // path to cerberus binary
	image   string // optional: container image; empty = cerberus default
	model   string // optional: model override; empty = cerberus default
	profile string // optional: path to cerberus profile file; empty = no profile
}

func New(bin, image, model, profile string) *Client {
	return &Client{bin: bin, image: image, model: model, profile: profile}
}

// SetProfile overrides the profile file path for the next session start.
func (c *Client) SetProfile(path string) {
	c.profile = path
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
	if c.profile != "" {
		args = append(args, "--profile-file", c.profile)
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
// When callbackURL is set, events are POSTed there and stdout is not parsed for message content.
// When callbackURL is empty, falls back to stdout parsing (legacy).
func (c *Client) Chat(ctx context.Context, session, prompt, callbackURL string) error {
	args := []string{"chat", "--name", session, "--prompt", specBuilderSystemPrompt + "\n\n" + prompt}
	if c.image != "" {
		args = append(args, "--image", c.image)
	}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	if c.profile != "" {
		args = append(args, "--profile-file", c.profile)
	}
	if callbackURL != "" {
		args = append(args, "--callback", callbackURL)
	}
	return c.run(ctx, args...)
}

// specBuilderSystemPrompt replaces pi's default code-agent system prompt for spec builder sessions.
const specBuilderSystemPrompt = `You are a spec writer. Your only job is to help the user write a Foundry spec — a markdown document that describes what should be built and how it breaks into phases.

You have read access to the filesystem. The project code is mounted at /workspace — always read files from there, never from host paths. Do NOT write or modify any files. Do NOT run build commands or tests.

Respond conversationally. Ask clarifying questions when needed. When you have enough information, produce the spec.`

// Message sends a follow-up message in an existing interactive session.
// When callbackURL is set, events are POSTed there and stdout is not parsed.
// cerberus message --name <session> --message <msg>
func (c *Client) Message(ctx context.Context, session, msg, callbackURL string) error {
	args := []string{"message", "--name", session, "--message", msg}
	if callbackURL != "" {
		args = append(args, "--callback", callbackURL)
	}
	return c.run(ctx, args...)
}

// Close commits any changes and cleans up an interactive session.
// cerberus close --name <session>
func (c *Client) Close(ctx context.Context, session string) error {
	return c.run(ctx, "close", "--name", session)
}
