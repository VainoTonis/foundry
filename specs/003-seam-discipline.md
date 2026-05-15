# 003: Seam Discipline — Cerberus Abstraction Boundary

## Problem

`internal/workflow/runner.go` currently knows cerberus implementation details:

- Session naming convention (`foundry-<spec_id>-p<N>`) lives in `internal/cerberus`
  but the runner passes spec ID and position to construct it.
- Profile file writing (JSON to /tmp) is done by the runner, not the cerberus client.
- Log polling cadence (2s ticker) is hardcoded in the runner.
- Diff retrieval and review output parsing happen in the runner.

If cerberus is ever replaced (or a second execution engine is added), these details
are scattered across the runner instead of contained behind the cerberus interface.

## Principle

From openclaw: plugins talk to core only through `plugin-sdk/*`. The analog here
is that `internal/workflow` talks to cerberus only through `internal/cerberus.Client`.
The runner should not know about sessions, Docker, file paths, or log formats.

## Changes

### 1. Move session naming into the cerberus client

Currently:
```go
// internal/cerberus/cerberus.go
func SessionName(specID int64, position int) string
```

The runner calls `cerberus.SessionName(sp.ID, phase.Position)` and passes the
result around. Instead, the client should manage session identity internally.

Change: `Client.Start` accepts a `RunRequest` struct, returns a `RunHandle` that
wraps the session. The runner never sees the session name string.

```go
type RunRequest struct {
    Prompt   string
    Timeout  time.Duration
    Profile  *Profile  // replaces writeProfileFile in runner
}

type RunHandle struct {
    session string  // unexported
}

func (c *Client) Start(ctx context.Context, req RunRequest) (*RunHandle, error)
func (c *Client) Logs(ctx context.Context, h *RunHandle, since string) (string, error)
func (c *Client) Diff(ctx context.Context, h *RunHandle) (string, error)
func (c *Client) Review(ctx context.Context, h *RunHandle) (string, error)
func (c *Client) Clean(ctx context.Context, h *RunHandle) error
```

### 2. Move profile file management into the cerberus client

`writeProfileFile` and `removeProfileFile` in runner.go write JSON to /tmp.
This is cerberus-specific (the profile file format matches cerberus's expected
input). Move it into the Client — the runner passes a `Profile` struct, the
client handles serialization and file lifecycle.

### 3. Move log polling into the cerberus client

The 2s ticker loop in `execPhase` is cerberus-specific. Replace with:

```go
func (c *Client) StreamLogs(ctx context.Context, h *RunHandle, callback func(line string)) error
```

The client owns the polling interval. The runner provides a callback that writes
to phase_logs. If a future engine supports real streaming (websocket, SSE), only
the client changes.

### 4. Move file extraction into the cerberus client

`extractFilesJSON` in runner.go parses cerberus review output format. Move it
into the client:

```go
func (c *Client) FilesTouched(ctx context.Context, h *RunHandle) ([]string, error)
```

### After these changes

`runner.go` becomes:

```
handle, err := r.cerb.Start(ctx, cerberus.RunRequest{...})
r.cerb.StreamLogs(ctx, handle, func(line string) { db.InsertPhaseLog(...) })
diff, _ := r.cerb.Diff(ctx, handle)
files, _ := r.cerb.FilesTouched(ctx, handle)
// run gates
// run review
r.cerb.Clean(ctx, handle)
```

No session names, no file paths, no log format parsing, no profile serialization.

### What this does NOT do

- Does not add a second execution engine. This is prep work.
- Does not change the cerberus binary interface or command-line flags.
- Does not introduce an interface/abstraction for "execution engines" yet.
  The `cerberus.Client` is concrete. An interface comes when there's a second
  implementation, not before.

### Build order

1. Add `RunRequest`, `RunHandle`, `Profile` types to `internal/cerberus/cerberus.go`
2. Move `SessionName` to unexported, used internally by `Start`
3. Move `writeProfileFile`/`removeProfileFile` into Client methods
4. Add `StreamLogs` method, move ticker logic from runner
5. Add `FilesTouched` method, move `extractFilesJSON` from runner
6. Update `runner.go` — replace direct session/file/log calls with Client methods
7. Delete orphaned helpers from runner.go
