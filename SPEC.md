# Foundry

A spec-driven, self-running development loop. Foundry is the place where ideas live
and get executed — automatically, overnight if needed — with the system capturing
decisions and learning from its own outcomes.

Foundry uses cerberus as its execution engine. Cerberus handles agents and containers.
Foundry handles everything above that: specs, phases, sequencing, review, memory.

---

## What it is

- A persistent home for specs (features, ideas, experiments)
- An automated phase runner that calls cerberus, reviews output, applies commits
- An institutional memory — every phase records what changed, why, and what was decided
- A backlog with two quality tracks: PoC (fast, dirty, prove it) and Polish (proper, tested, maintainable)

## What it is not

- Not a CI/CD pipeline
- Not a test runner (cerberus handles that if the prompt says to)
- Not a code reviewer for humans — review is for the machine, you look at results after
- Not a multi-agent parallel runner (yet)

---

## Core loop

```
idea dumped as spec
  → you start it as PoC from the UI
  → phases run via cerberus (one at a time)
  → each phase: LLM reviews diff vs goal
    → pass: commit applied, next phase starts
    → fail: retry once with adjusted prompt
    → fail again: phase failed, workflow paused
  → all phases done: workflow done
  → you promote spec to Polish track when PoC validates
  → Polish workflow runs with stricter prompt overlay
```

Runs unattended. Check results in the UI whenever.

---

## Two tracks

### PoC
Prompt overlay: "Make it work. Prove the concept. Structure and tests are secondary."
Default track for any new spec. Triggered manually from the UI.

### Polish
Prompt overlay: "Write this properly. Clean structure, explicit error handling, proper tests. This goes long-term into the codebase."
Triggered manually. You promote a spec after a PoC validates.

---

## Spec format

Specs are markdown. Stored in the DB. The title is free-form (e.g. "SPEC-001 user auth").
Specs belong to a project (a repo on disk). Tags can link a spec to other repos or domains.

```markdown
# Feature title

Global context — background, constraints, anything the agent needs to know.
This is prepended to every phase prompt.

## Phase 1: Name
What this phase should accomplish. This becomes the phase goal and the cerberus prompt body.

## Phase 2: Name
...
```

Sections starting with `## Phase N:` become phases.
Everything before the first phase = global context.

---

## Memory layer

Every phase, after cerberus runs, the review LLM produces a decision record:

- **goal** — what the phase was trying to do
- **summary** — what actually happened (from the diff)
- **decisions** — key choices made (e.g. "used pgx directly, no ORM")
- **files_touched** — list of files changed (from git diff --stat)
- **rationale** — why this approach, inferred from spec + diff
- **outcome** — pass | fail | retry

Stored on the phase row (JSONB). No special query layer in v0.0.1.
Designed to be exportable to embeddings or markdown later without schema changes.

Goal: answer "why does this endpoint exist?" by querying phases where files_touched
contains the relevant file.

---

## Data model

```sql
projects
  id, name, repo_path, created_at

specs
  id, title, content (markdown), track (poc|polish),
  status (dumpster|queued|running|done|failed|paused),
  project_id, tags (jsonb), created_at, updated_at

workflows
  id, spec_id, track (snapshot at run time), status,
  max_cost_usd,             -- nullable, defaults from config; runner pauses workflow if cumulative phase cost would exceed
  created_at, finished_at

phases
  id, workflow_id,
  position,                 -- execution order
  name, goal,
  prompt_sent,              -- exact string sent to cerberus
  status,                   -- pending|running|awaiting_review|done|failed
  retry_count,
  timeout_seconds,          -- wall-clock kill for cerberus run; defaults from config
  cerberus_session,
  cerberus_commit,
  cost_usd,
  started_at, finished_at,
  review_verdict,           -- pass|fail|null
  review_notes,             -- raw reviewer output
  adjusted_prompt,          -- generated retry prompt on fail
  decision_summary,         -- what happened, key choices
  decision_rationale,       -- why this approach
  files_touched             -- jsonb: ["internal/api/handlers.go", ...]

phase_logs
  id, phase_id, line, ts
```

---

## Cerberus integration

Foundry shells out to the `cerberus` binary. Cerberus manages containers internally.
Foundry does not care about Docker or container lifecycle.

Session naming: `foundry-<spec_id_short>-p<N>`

Commands used:
```
cerberus --name <session> start --prompt-file <tmpfile> --image <image>
cerberus --name <session> status
cerberus --name <session> logs
cerberus --name <session> review --diff
cerberus --name <session> clean
```

`start` is blocking. Foundry runs it in a goroutine. Polls logs every 2s, writes to phase_logs.
A wall-clock timer enforces `phases.timeout_seconds`; on expiry the cerberus session is killed and the phase fails as if cerberus returned non-zero.

Before each phase starts, the runner checks `SUM(cost_usd)` for the workflow against `workflows.max_cost_usd`.
If the next phase would plausibly exceed the budget, the workflow is paused before the phase begins (no cerberus session opened).

On phase done:
1. Phase → awaiting_review
2. Review LLM runs: diff + goal → verdict + decision record
3. Pass: cherry-pick commit to repo base branch → cerberus clean → next phase
4. Fail: generate adjusted prompt → retry (retry_count max 1)
5. Fail again: phase → failed, workflow → paused

---

## Review model

Uses opencode (same image as cerberus agents). Cheap/fast model (e.g. haiku).
Output is structured JSON: verdict, notes, decisions, rationale, adjusted_prompt.
Model and image configurable, defaults to same cerberus image.

The reviewer prompt is chosen from `workflows.track`:

- **PoC**: diff + phase goal → verdict. Bar is "does this plausibly accomplish the goal."
- **Polish**: diff + phase goal + test output (from cerberus) → verdict. Bar is "diff accomplishes the goal, tests cover the public surface, no direct tests against unexported functions." Private functions are expected to be exercised through their public callers; a test that imports and calls an unexported symbol is a fail.

---

## API

```
POST   /api/projects
GET    /api/projects
GET    /api/projects/:id

POST   /api/specs                      {project_id, title, content, tags}
GET    /api/specs                      ?status=dumpster&project_id=...
GET    /api/specs/:id
PATCH  /api/specs/:id                  edit content, title, tags
POST   /api/specs/:id/promote          poc → polish

POST   /api/workflows                  {spec_id}  manual trigger
GET    /api/workflows/:id
GET    /api/workflows/:id/phases
POST   /api/workflows/:id/resume       re-runs the failed phase from scratch (fresh prompt, retry_count reset).
                                       To change the prompt, edit the spec first.

GET    /api/phases/:id
GET    /api/phases/:id/logs
GET    /api/phases/:id/logs/stream     SSE
GET    /api/phases/:id/diff
POST   /api/phases/:id/approve         manual override (skip auto-review)
POST   /api/phases/:id/reject          manual pause
```

---

## Frontend

Vanilla JS, no framework. Mobile-friendly CSS. Three views:

1. **Backlog** — spec list grouped by status (dumpster / queued / running / done).
   Create spec, see track, quick-launch.
2. **Spec detail** — edit spec content, see all workflow runs, promote to polish.
3. **Workflow detail** — phase list with status chips, live log stream (SSE),
   diff viewer, review verdict + decision record, manual approve/reject.

---

## Configuration (config.yaml)

```yaml
db_url: "postgres://foundry:foundry@localhost:5432/foundry?sslmode=disable"
cerberus_bin: "cerberus"
cerberus_image: "your-dev-image"
server_port: 8080
max_concurrent_workflows: 1            # keep it simple for now
default_workflow_budget_usd: 5.00      # used for workflows.max_cost_usd when not set per-workflow
default_phase_timeout_seconds: 1800    # used for phases.timeout_seconds when not set per-phase
```

---

## Tech stack

- Go 1.22+, stdlib net/http (no gin/echo)
- pgx/v5 for postgres, no ORM
- golang-migrate for migrations
- Vanilla JS, no bundler, no framework
- Docker Compose for local postgres (app runs native in dev)

---

## Project structure

```
foundry/
  cmd/server/main.go
  internal/
    api/        HTTP handlers
    db/         pgx queries
    cerberus/   exec wrapper
    spec/       markdown parser (phases, context extraction)
    review/     LLM review client
    workflow/   runner, phase state machine
    config/     config.yaml loader
  migrations/   001_init.sql ...
  web/          index.html, app.js, style.css
  config.yaml
  docker-compose.yml
  Dockerfile
  go.mod
  SPEC.md       (this file)
```

---

## Build order (for cerberus sessions)

1. `migrations/` — SQL schema
2. `internal/db/` — pgx queries matching schema
3. `internal/cerberus/` — exec wrapper (start, status, logs, review, apply, clean)
4. `internal/spec/` — markdown parser
5. `internal/review/` — LLM review client
6. `internal/workflow/` — linear runner, state machine, runner goroutine
7. `internal/api/` — HTTP handlers
8. `cmd/server/` — main: config load, db init, routes, listen
9. `web/` — HTML/JS/CSS
10. `docker-compose.yml` + `Dockerfile`

---

## Future (not v0.0.1)

- Grafana metrics endpoint (workflow counts, cost, phase duration)
- Remote cerberus (orchestrator on separate machine)
- Parallel phases within one workflow
- NL query over decision memory ("why does X work this way?")
- Spec templates (pre-filled PoC vs polish scaffolding)
- GitHub/GitLab PR creation on workflow done
- Notification hooks (webhook, push)
