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
	bin      string // path to cerberus binary
	image    string // optional: container image; empty = cerberus default
	model    string // optional: model override; empty = cerberus default
	profile  string // optional: path to cerberus profile file; empty = no profile
	repoPath string // working directory for all cerberus commands (target project repo)
}

func New(bin, image, model, profile string) *Client {
	return &Client{bin: bin, image: image, model: model, profile: profile}
}

// SetRepoPath sets the working directory for all cerberus commands.
func (c *Client) SetRepoPath(path string) {
	c.repoPath = path
}

// SetProfile overrides the profile file path for the next session start.
func (c *Client) SetProfile(path string) {
	c.profile = path
}

// Start launches a cerberus session with the given prompt. Blocking — run in a goroutine.
// When callbackURL is set, cerberus POSTs incremental JSONL events there.
// cerberus start --name <session> --prompt-file <f> [--image <image>] [--callback <url> --output jsonl]
func (c *Client) Start(ctx context.Context, session, prompt, callbackURL string) error {
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
	if callbackURL != "" {
		args = append(args, "--callback", callbackURL, "--output", "jsonl")
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

func (c *Client) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = c.repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cerberus %s: %w%s", strings.Join(args, " "), err, formatCommandOutput(stdout.String(), stderr.String()))
	}
	return nil
}

func (c *Client) output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = c.repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cerberus %s: %w%s", strings.Join(args, " "), err, formatCommandOutput(stdout.String(), stderr.String()))
	}
	return stdout.String(), nil
}

func formatCommandOutput(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	if stdout == "" && stderr == "" {
		return ""
	}
	var b strings.Builder
	if stderr != "" {
		b.WriteString("\nstderr:\n")
		b.WriteString(stderr)
	}
	if stdout != "" {
		b.WriteString("\nstdout:\n")
		b.WriteString(stdout)
	}
	return b.String()
}

// Chat starts an interactive cerberus session (first turn). Blocking — run in a goroutine.
// When callbackURL is set, cerberus POSTs incremental JSONL events there.
// When callbackURL is empty, falls back to stdout parsing (legacy).
func (c *Client) Chat(ctx context.Context, session, prompt, callbackURL string) error {
	args := []string{"chat", "--name", session, "--prompt", prompt}
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
		args = append(args, "--callback", callbackURL, "--output", "jsonl")
	}
	return c.run(ctx, args...)
}

// Message sends a follow-up message in an existing interactive session.
// When callbackURL is set, cerberus POSTs incremental JSONL events there.
// cerberus message --name <session> --message <msg>
func (c *Client) Message(ctx context.Context, session, msg, callbackURL string) error {
	args := []string{"message", "--name", session, "--message", msg}
	if callbackURL != "" {
		args = append(args, "--callback", callbackURL, "--output", "jsonl")
	}
	return c.run(ctx, args...)
}

// Generate sends a single prompt to an interactive cerberus session and returns stdout.
// The caller is responsible for cleaning the session when desired.
// cerberus chat --name <session> --prompt <prompt>
func (c *Client) Generate(ctx context.Context, session, prompt string) (string, error) {
	args := []string{"chat", "--name", session, "--prompt", prompt}
	if c.image != "" {
		args = append(args, "--image", c.image)
	}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	if c.profile != "" {
		args = append(args, "--profile-file", c.profile)
	}
	return c.output(ctx, args...)
}

// Close commits any changes and cleans up an interactive session.
// cerberus close --name <session>
func (c *Client) Close(ctx context.Context, session string) error {
	return c.run(ctx, "close", "--name", session)
}
