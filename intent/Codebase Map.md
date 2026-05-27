# Codebase Map

Tags: #architecture #memory #codebase-map

This page maps where durable Foundry concepts live in the current repository. Use it to find the right seam before writing specs or changing code.

## Runtime entrypoint and startup flow

- `cmd/server/main.go` is the production entrypoint: load config, run SQL migrations from `migrations/`, open the `pgx` pool, create the Cerberus client, event hub, workflow runner, orphan draft recovery goroutine, API/UI server, and static asset handlers.
- Startup assumes the configured database and filesystem paths already exist; migrations are applied automatically before serving requests (`README.md`, `cmd/server/main.go`).
- The same `api.Server` serves server-rendered UI routes and `/api/` JSON/SSE routes, so UI/API behavior often share handler dependencies rather than separate apps (`internal/api/handlers.go`).

## Main backend packages and responsibilities

- `internal/api` owns HTTP routing, HTML templates, JSON handlers, SSE streams, spec builder orchestration, memory update review actions, and Cerberus callback ingestion.
- `internal/db` is the explicit SQL boundary. Data structs mirror tables and queries are handwritten; future work should update migrations and `internal/db/queries.go` together.
- `internal/spec` is intentionally small: parse markdown phase headers and compose phase prompts with default intent references and track overlays.
- `internal/workflow` owns the linear workflow state machine: create phases, build prompts, run Cerberus, collect evidence, cherry-pick accepted commits, retry once, and publish live updates.
- `internal/cerberus` is a narrow CLI wrapper. Foundry sets the target repo working directory and shells out; Cerberus owns agent/container execution.
- `internal/memory` is filesystem-backed approved memory for project namespaces, not a database query layer.
- Smaller support seams: `internal/config` loads YAML, `internal/discover` scans configured git roots, `internal/hub` broadcasts in-process SSE events, and `internal/review` is present but current runner review logic mainly uses Cerberus review output.

## Workflow/spec/phase execution path

- Specs are stored as markdown in `specs.content`; only headings matching `## Phase N: Name` become executable phases. Everything before the first phase is global context (`internal/spec/spec.go`).
- Starting a workflow snapshots the spec track into `workflows.track`, then the runner creates `phases` rows only if the workflow has none. Editing a spec after phases exist does not automatically rewrite those phase rows (`internal/workflow/runner.go`).
- Phase prompts are composed as: approved project memory, target repo root header, spec global context, default intent references, phase goal, then PoC/Polish overlay (`internal/spec/spec.go`, `internal/workflow/runner.go`, `internal/memory/memory.go`).
- Cerberus sessions are per workflow phase (`foundry-w<workflowID>-p<phaseID>`), run with `cmd.Dir` set to the target project repo, then reviewed through `cerberus review`; on pass Foundry cherry-picks the Cerberus branch commit into the target repo (`internal/cerberus/cerberus.go`, `internal/workflow/runner.go`).
- Workflow execution is linear and state is persisted in Postgres. In-process cancellation and SSE are convenience mechanisms, not the canonical state store (`internal/workflow/runner.go`, `internal/db/queries.go`).

## Memory-related code paths

- Project rows store `repo_path` for the target code repo and `memory_namespace` for the private memory repo directory. The memory repo root is process config, not per-project database state (`migrations/006_project_memory_namespace.up.sql`, `migrations/007_drop_project_memory_repo_path.up.sql`, `internal/db/queries.go`).
- Approved memory is all non-hidden `.md` files under `<memory_repo_path>/<memory_namespace>`, sorted and prepended to spec-builder and phase prompts when available (`internal/memory/memory.go`, `internal/api/handlers.go`, `internal/workflow/runner.go`).
- Memory update jobs are review artifacts tied to workflows. Accepting a job writes markdown under `<namespace>/workflow-updates/workflow-<id>.md` in the private memory repo and commits it there; until accepted, it is only database state (`migrations/008_memory_update_jobs.up.sql`, `internal/memory/memory.go`).
- `phases.phase_feedback` stores structured per-phase hints that can feed memory proposals, separate from approved Markdown memory (`migrations/009_phase_feedback.up.sql`, `internal/workflow/runner.go`).

## Database and migration shape

- `migrations/001_init.up.sql` defines the core graph: projects → specs → workflows → phases → phase_logs. Cascades mean deleting a project removes its specs, workflows, phases, and logs.
- Later migrations add spec-builder drafts (`spec_drafts`), Cerberus profiles, compact Cerberus callback events, project memory namespaces, memory update jobs, and phase feedback (`migrations/002_spec_drafts.up.sql` through `migrations/009_phase_feedback.up.sql`).
- Status and track values are constrained in SQL as well as implied in code; future specs should check migrations before inventing new lifecycle names.

## Frontend surface area

- The UI is server-rendered HTML templates embedded in `internal/api/handlers.go`, progressively enhanced by HTMX and `web/app.js`; `web/index.html` is not the main UI shell in current runtime.
- `web/app.js` handles JSON form submission, client-side redirects, workflow/draft/log EventSource streams, phase detail panel preservation, and spec-builder preview updates from `update_spec` tool events.
- Because the frontend consumes server fragments and SSE events, API/UI contract changes often require coordinated edits in templates, handlers, and `web/app.js` rather than a standalone frontend model.

## Notable seams or boundaries

- Keep the Cerberus boundary narrow: Foundry constructs prompts, chooses repo/profile/session names, persists state, and applies commits; Cerberus performs agent execution and exposes logs/diff/review.
- Phase prompts must remain executable from the target repo root. Specs and generated prompts should prefer relative paths because Cerberus runs with the project repo as its working directory.
- Approved memory is plain Markdown in a private git repo; Postgres tracks proposals and workflow evidence but is not the canonical store for accepted intent memory.
- The codebase favors explicit SQL, simple structs, and stdlib HTTP. Adding abstractions should preserve inspectable state and reviewable data flow.

## Source references

- `README.md`
- `SPEC.md`
- `cmd/server/main.go`
- `internal/api/handlers.go`
- `internal/api/cerberus_events.go`
- `internal/db/queries.go`
- `internal/spec/spec.go`
- `internal/workflow/runner.go`
- `internal/cerberus/cerberus.go`
- `internal/memory/memory.go`
- `migrations/001_init.up.sql`
- `migrations/002_spec_drafts.up.sql`
- `migrations/003_profiles.up.sql`
- `migrations/004_cerberus_events.up.sql`
- `migrations/006_project_memory_namespace.up.sql`
- `migrations/007_drop_project_memory_repo_path.up.sql`
- `migrations/008_memory_update_jobs.up.sql`
- `migrations/009_phase_feedback.up.sql`
- `web/app.js`
