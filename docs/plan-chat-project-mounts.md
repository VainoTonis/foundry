# Plan: Dynamic Project Mounts for Chat Sessions

## Goal

Let a user attach/detach foundry projects to a chat session at any time.
Each attached project's `repo_path` gets mounted read-only into the cerberus
container so the agent can browse it with pi's built-in tools (`read`, `ls`,
`bash`, `grep`, `find`). Mount changes kill and restart the live cerberus
session; history is replayed from `chat_messages`.

## Prerequisites

- Cerberus `TurnInput.ExtraMounts []Mount` field must exist (see
  `cerberus/docs/plan-chat-project-mounts.md`).
- Foundry's `cerberus.TurnInput` struct must include the same field.

---

## 1. Migration — `chat_session_projects` junction table

File: `migrations/016_chat_session_projects.up.sql`

```sql
CREATE TABLE chat_session_projects (
    session_id  BIGINT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    project_id  BIGINT NOT NULL REFERENCES projects(id)      ON DELETE CASCADE,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (session_id, project_id)
);

CREATE INDEX ON chat_session_projects(session_id);
```

Down file: `016_chat_session_projects.down.sql`

```sql
DROP TABLE chat_session_projects;
```

---

## 2. DB layer — `internal/db/chat_projects.go`

New file. Functions:

```go
func AttachProjectToSession(ctx, pool, sessionID, projectID int64) error
func DetachProjectFromSession(ctx, pool, sessionID, projectID int64) error
func ListSessionProjects(ctx, pool, sessionID int64) ([]Project, error)
```

`ListSessionProjects` joins `chat_session_projects` with `projects` and returns
full `Project` rows (id, name, repo_path).

---

## 3. `cerberus.TurnInput` — add `ExtraMounts`

File: `internal/cerberus/cerberus.go`

```go
type Mount struct {
    Host      string `json:"host"`
    Container string `json:"container"`
    ReadOnly  bool   `json:"read_only,omitempty"`
}

type TurnInput struct {
    // existing fields unchanged ...
    ExtraMounts []Mount `json:"extra_mounts,omitempty"`
}
```

---

## 4. Chat service — mount wiring and reset logic

File: `internal/chat/service.go`

### `sendTurn` — build mounts from attached projects

Before calling `cerb.Turn`, load attached projects and build `ExtraMounts`:

```go
projects, _ := db.ListSessionProjects(ctx, s.pool, sess.ID)
for _, p := range projects {
    slug := slugify(p.Name)
    input.ExtraMounts = append(input.ExtraMounts, cerberus.Mount{
        Host:      p.RepoPath,
        Container: "/mnt/projects/" + slug,
        ReadOnly:  true,
    })
}
```

`slugify`: lowercase, replace non-alphanum with `-`.

### `AttachProject(ctx, sessionID, projectID int64) error`

1. Validate session exists and is not `streaming`
2. `db.AttachProjectToSession`
3. If session has an active cerberus container (`CerberusUUID != ""`):
   - `cerb.Clean(ctx, sess.CerberusSession)` — kills container
   - `db.ClearChatSessionUUID(ctx, pool, sessionID)` — new DB func, sets
     `cerberus_uuid = ''`
4. Return. Next message send triggers a fresh cerberus start with history
   replay (existing path in `sendTurn` already handles `UUID == ""`).

### `DetachProject(ctx, sessionID, projectID int64) error`

Same as `AttachProject` but calls `db.DetachProjectFromSession` instead.

### New DB func needed

```go
func ClearChatSessionUUID(ctx, pool, id int64) error
// UPDATE chat_sessions SET cerberus_uuid = '', updated_at = NOW() WHERE id = $1
```

Add to `internal/db/chat.go`.

---

## 5. API — `internal/httpapi/chat.go`

Add two new suffixes in `HandleChatSession`:

```
POST   /api/chat/sessions/{id}/projects          body: {project_id: N}
DELETE /api/chat/sessions/{id}/projects/{pid}
```

Both call the corresponding service methods and return `204 No Content`.

Also add:

```
GET /api/chat/sessions/{id}/projects
```

Returns the list of attached projects (id, name, repo_path). Used by the UI
to render the project picker state.

---

## 6. UI

### Chat statusbar

Add a project section to `.chat-statusbar`. Shows attached project names as
chips. A `+` button opens a dropdown of available projects (fetched from
`/api/projects`). Clicking an attached project chip removes it.

Chips: `<span class="chip chip-active">project-name <button>×</button></span>`

### Picker flow

1. User clicks `+` → fetch `/api/projects`, render list of unattached projects
2. Click project → `POST /api/chat/sessions/{id}/projects`
3. On success → refresh statusbar (htmx or manual fetch)
4. Click `×` on chip → `DELETE /api/chat/sessions/{id}/projects/{pid}`
5. On success → refresh statusbar

No page reload needed. The cerberus reset is transparent — next message the
user sends will just take slightly longer as history replays.

### Informing the agent

The system prompt (via `ProfileFile.Instructions` or cerberus `instructions`
field in `config.json`) should mention the mount paths. Options:

**A — Static instruction in profile/config**: "Attached projects are mounted
at `/mnt/projects/<name>`. Use `ls`, `read`, `bash` to explore them."

**B — Dynamic instruction built per-turn**: `sendTurn` builds an
`instructions` string listing the actual mounted paths and injects it into
`TurnInput` (cerberus already has `json:"instructions"` on `ProfileFile`).

Option B is better for usability. Implement in `sendTurn`:

```go
if len(input.ExtraMounts) > 0 {
    lines := []string{"Attached project dirs (read-only):"}
    for _, m := range input.ExtraMounts {
        lines = append(lines, "  "+m.Container)
    }
    input.Instructions = strings.Join(lines, "\n")
}
```

---

## 7. What stays the same

- Session suspend/resume — no change, mounts are re-derived from DB on each
  `sendTurn` call so they survive suspension too
- History replay — already works, this piggybacks on it
- Profile system — mounts are orthogonal, profiles still control model/image/env

---

## Implementation order

1. Cerberus changes (prerequisite)
2. Migration
3. `db.ClearChatSessionUUID` + `db.chat_projects.go`
4. `cerberus.TurnInput` update in foundry
5. `chat/service.go` — `AttachProject`, `DetachProject`, mount wiring in `sendTurn`
6. `httpapi/chat.go` — new endpoints
7. UI — statusbar chips + picker
