package cerberus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// TurnMessage mirrors cerberus config.Message for history replay.
type TurnMessage struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Role     string `json:"role"`
	Content  string `json:"content"`
}

// Mount describes a host path to bind-mount into the cerberus container.
type Mount struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	ReadOnly  bool   `json:"read_only,omitempty"`
}

// TurnInput is the JSON payload sent to `cerberus turn` on stdin.
type TurnInput struct {
	Name         string        `json:"name,omitempty"`
	UUID         string        `json:"uuid,omitempty"`
	NoRepo       bool          `json:"no_repo,omitempty"`
	Agent        string        `json:"agent,omitempty"`
	Model        string        `json:"model,omitempty"`
	Image        string        `json:"image,omitempty"`
	Message      string        `json:"message"`
	History      []TurnMessage `json:"history,omitempty"`
	CallbackURL  string        `json:"callback_url,omitempty"`
	ProfileFile  string        `json:"profile_file,omitempty"`
	Instructions string        `json:"instructions,omitempty"`
	ExtraMounts  []Mount       `json:"extra_mounts,omitempty"`
}

// TurnOutput is the JSON response from `cerberus turn` on stdout.
type TurnOutput struct {
	Status       string  `json:"status"`
	UUID         string  `json:"uuid"`
	SessionID    string  `json:"session_id,omitempty"`
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// ErrSessionNotFound is returned by Turn when cerberus reports the session uuid is gone.
const ErrSessionNotFound = "session not found"

// ErrSessionAlreadyExists is returned by Turn when UUID is empty but the session name already exists.
const ErrSessionAlreadyExists = "already exists"

// Client wraps the cerberus binary.
type Client struct {
	bin      string // path to cerberus binary
	image    string // optional: container image; empty = cerberus default
	model    string // optional: model override; empty = cerberus default
	profile  string // optional: path to cerberus profile file; empty = no profile
	repoPath string // working directory for all cerberus commands (target project repo)
}

// PerRepoView provides an immutable, per-repo command API that does not mutate shared Client state.
type PerRepoView struct {
	client   *Client
	repoPath string
}

func New(bin, image, model, profile string) *Client {
	return &Client{bin: bin, image: image, model: model, profile: profile}
}

// WithRepo returns an immutable, per-repo command API for the given repo path.
// All commands executed via the returned PerRepoView use the specified repo path
// without mutating the shared Client state.
func (c *Client) WithRepo(repoPath string) *PerRepoView {
	return &PerRepoView{client: c, repoPath: repoPath}
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

// Turn executes a single conversation turn via `cerberus turn` (JSON stdin/stdout).
// Set NoRepo: true on input for context-free chat sessions (no git worktree).
// If TurnOutput.Status is "error" and TurnOutput.Error is ErrSessionNotFound,
// the caller should retry with UUID="" and History populated from DB.
func (c *Client) Turn(ctx context.Context, input TurnInput) (TurnOutput, error) {
	if input.Model == "" && c.model != "" {
		input.Model = c.model
	}
	if input.Image == "" && c.image != "" {
		input.Image = c.image
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return TurnOutput{}, fmt.Errorf("marshal turn input: %w", err)
	}
	cmd := exec.CommandContext(ctx, c.bin, "turn")
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return TurnOutput{}, fmt.Errorf("cerberus turn: %w%s", err, formatCommandOutput(stdout.String(), stderr.String()))
	}
	var out TurnOutput
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &out); err != nil {
		return TurnOutput{}, fmt.Errorf("cerberus turn: parse response: %w\nstdout: %s", err, stdout.String())
	}
	return out, nil
}

func (c *Client) run(ctx context.Context, args ...string) error {
	return c.runWith(ctx, c.repoPath, args...)
}

func (c *Client) runWith(ctx context.Context, repoPath string, args ...string) error {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cerberus %s: %w%s", strings.Join(args, " "), err, formatCommandOutput(stdout.String(), stderr.String()))
	}
	return nil
}

func (c *Client) output(ctx context.Context, args ...string) (string, error) {
	return c.outputWith(ctx, c.repoPath, args...)
}

func (c *Client) outputWith(ctx context.Context, repoPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = repoPath
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

// PerRepoView methods - immutable per-repo command execution

// Start launches a cerberus session with the given prompt. Blocking — run in a goroutine.
// When callbackURL is set, cerberus POSTs incremental JSONL events there.
func (v *PerRepoView) Start(ctx context.Context, session, prompt, callbackURL string) error {
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
	if v.client.image != "" {
		args = append(args, "--image", v.client.image)
	}
	if v.client.model != "" {
		args = append(args, "--model", v.client.model)
	}
	if v.client.profile != "" {
		args = append(args, "--profile-file", v.client.profile)
	}
	if callbackURL != "" {
		args = append(args, "--callback", callbackURL, "--output", "jsonl")
	}
	return v.client.runWith(ctx, v.repoPath, args...)
}

// Status returns the raw status string from cerberus.
func (v *PerRepoView) Status(ctx context.Context, session string) (string, error) {
	out, err := v.client.outputWith(ctx, v.repoPath, "status", "--name", session)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Logs returns the full log output for a session.
func (v *PerRepoView) Logs(ctx context.Context, session string) (string, error) {
	return v.client.outputWith(ctx, v.repoPath, "logs", "--name", session)
}

// Diff returns the git diff produced by the cerberus session.
func (v *PerRepoView) Diff(ctx context.Context, session string) (string, error) {
	return v.client.outputWith(ctx, v.repoPath, "review", "--name", session, "--diff")
}

// Review returns the plain review summary (files touched, commit hash).
func (v *PerRepoView) Review(ctx context.Context, session string) (string, error) {
	return v.client.outputWith(ctx, v.repoPath, "review", "--name", session)
}

// Clean removes the cerberus session.
func (v *PerRepoView) Clean(ctx context.Context, session string) error {
	return v.client.runWith(ctx, v.repoPath, "clean", "--name", session)
}

// Chat starts an interactive cerberus session (first turn). Blocking — run in a goroutine.
// When callbackURL is set, cerberus POSTs incremental JSONL events there.
func (v *PerRepoView) Chat(ctx context.Context, session, prompt, callbackURL string) error {
	args := []string{"chat", "--name", session, "--prompt", prompt}
	if v.client.image != "" {
		args = append(args, "--image", v.client.image)
	}
	if v.client.model != "" {
		args = append(args, "--model", v.client.model)
	}
	if v.client.profile != "" {
		args = append(args, "--profile-file", v.client.profile)
	}
	if callbackURL != "" {
		args = append(args, "--callback", callbackURL, "--output", "jsonl")
	}
	return v.client.runWith(ctx, v.repoPath, args...)
}

// Message sends a follow-up message in an existing interactive session.
func (v *PerRepoView) Message(ctx context.Context, session, msg, callbackURL string) error {
	args := []string{"message", "--name", session, "--message", msg}
	if callbackURL != "" {
		args = append(args, "--callback", callbackURL, "--output", "jsonl")
	}
	return v.client.runWith(ctx, v.repoPath, args...)
}

// Generate sends a single prompt to an interactive cerberus session and returns stdout.
// The caller is responsible for cleaning the session when desired.
func (v *PerRepoView) Generate(ctx context.Context, session, prompt string) (string, error) {
	args := []string{"chat", "--name", session, "--prompt", prompt}
	if v.client.image != "" {
		args = append(args, "--image", v.client.image)
	}
	if v.client.model != "" {
		args = append(args, "--model", v.client.model)
	}
	if v.client.profile != "" {
		args = append(args, "--profile-file", v.client.profile)
	}
	return v.client.outputWith(ctx, v.repoPath, args...)
}

// Close commits any changes and cleans up an interactive session.
func (v *PerRepoView) Close(ctx context.Context, session string) error {
	return v.client.runWith(ctx, v.repoPath, "close", "--name", session)
}
