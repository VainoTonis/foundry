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
	image string // container image passed to start
}

func New(bin, image string) *Client {
	return &Client{bin: bin, image: image}
}

// Start launches a cerberus session with the given prompt. It writes the prompt
// to a temp file and calls: cerberus --name <session> start --prompt-file <f> --image <image>
// The call is blocking; run it in a goroutine.
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

	return c.run(ctx, "--name", session, "start", "--prompt-file", f.Name(), "--image", c.image)
}

// Status returns the raw status string from cerberus.
func (c *Client) Status(ctx context.Context, session string) (string, error) {
	out, err := c.output(ctx, "--name", session, "status")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Logs returns the full log output for a session.
func (c *Client) Logs(ctx context.Context, session string) (string, error) {
	out, err := c.output(ctx, "--name", session, "logs")
	if err != nil {
		return "", err
	}
	return out, nil
}

// Diff returns the git diff produced by the cerberus session.
func (c *Client) Diff(ctx context.Context, session string) (string, error) {
	out, err := c.output(ctx, "--name", session, "review", "--diff")
	if err != nil {
		return "", err
	}
	return out, nil
}

// Commit returns the commit hash applied by cerberus (cherry-pick source).
func (c *Client) Commit(ctx context.Context, session string) (string, error) {
	out, err := c.output(ctx, "--name", session, "commit")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Clean removes the cerberus session.
func (c *Client) Clean(ctx context.Context, session string) error {
	return c.run(ctx, "--name", session, "clean")
}

// SessionName builds the canonical session name for a phase.
// specIDShort is the first 8 chars of the spec id (as string), n is the phase position.
func SessionName(specIDShort string, n int) string {
	return fmt.Sprintf("foundry-%s-p%d", specIDShort, n)
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
