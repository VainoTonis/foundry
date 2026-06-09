# Internal Cleanup Plan

This is a disposable cleanup plan. Delete or replace it once the work is complete.

Assumption: memory has already been removed. Do not preserve memory-era APIs, database compatibility, migrations, UI affordances, config fields, or tests.

This cleanup is allowed to break the app temporarily. Reducing scope and restoring understandable boundaries comes first. Compile/runtime repair happens after unwanted shape is gone.

## Goal

Reduce package overload and remove accidental structure.

The target is not perfect architecture. The target is a smaller codebase where main flows are easy to find and future features have clear places to go.

## Rules

- No new features during cleanup.
- No compatibility work for removed concepts.
- Breaking builds are acceptable between cleanup steps.
- Remove wrong code before repairing callers.
- Prefer split files before creating new packages.
- Create packages when responsibilities differ, not because names look tidy.
- Every package keeps its package-level shared types in `types.go`.
- Do not use `types.go` as a junk drawer; types in it must belong to that package's responsibility.
- Put constants and types at the top of files when present, then the main exported function or orchestration method.
- Delete zombie code instead of preserving old ideas.
- Keep phase status honest: mark a phase done only after its work is complete and the user has confirmed the changes.

## Phase 1: Finish Dead-Surface Removal

Status: Done.

Memory removal is complete. Remaining cleanup phases should assume memory-era surface is gone.

Historical checklist for what this phase removed or invalidated:

Look for:

- stale imports
- stale config fields
- stale UI labels
- stale API routes
- stale tests
- stale export fields
- stale DB query functions unused by current code
- stale migrations kept only for removed behavior
- stale model fields that exist only to support removed behavior

Do not patch around removed concepts. Delete first. Repair only product that remains.

## Phase 2: Split API Into HTTP Edge, JSON API, And Web UI

Status: Done.

Current problem: API code mixes HTTP transport, server-rendered UI, streaming mechanics, feature policy, request validation, and application operations.

Target shape:

- `internal/httpserver` wires server dependencies, routes, middleware, static assets, and shared HTTP edge concerns.
- `internal/httpapi` owns JSON API handlers: parse requests, call app code, write JSON/errors.
- `internal/webui` owns server-rendered UI: templates, view models, display formatting, render helpers.
- `internal/stream` owns live HTTP streaming mechanics if extraction is small: SSE connection lifecycle, heartbeat, catch-up writes, and event formatting.
- Application packages own decisions: workflow policy, draft authoring policy, project/spec behavior.
- External tool callbacks are normalized at HTTP edge before they touch product policy.

Use these package names unless implementation reveals a clearly better name. Avoid generic `server` as a junk drawer; if `server` exists, it must mean composition/wiring only.

Responsibility split:

- `httpserver`: route table, middleware, shared dependencies, static file mounting, top-level `http.Handler`.
- `httpapi`: JSON request/response handlers only; no HTML templates.
- `webui`: `embed.FS` templates, renderer, template funcs, view-only types.
- `stream`: small protocol package for SSE/live delivery only; no workflow/draft policy.
- feature packages: own policy and state transitions.

Do not let HTTP handlers remain where product behavior accumulates.

Expected result:

- handlers are thin
- route file reads like a table of app surfaces
- policy can be tested without `net/http`
- UI templates are not mixed into transport helpers
- HTML rendering no longer lives in giant Go raw strings
- live streaming protocol code is isolated from workflow/draft policy
- common server wiring does not become a dumping ground for unrelated helpers

## Phase 3: Move HTML To `internal/webui`

Status: Done.

Current problem: server-rendered UI and HTTP handling are too close together, especially where templates and handlers live in same large files.

Target shape:

```text
internal/webui
  renderer.go
  funcs.go
  types.go
  templates/
    shell.html
    backlog.html
    projects.html
    specs.html
    workflows.html
    phases.html
    builder.html
    settings.html
```

Rules:

- templates are `.html` files loaded with `embed.FS`
- no giant template raw strings inside Go files
- `webui.Render(w, name, data)` renders named templates
- view-only structs may live in `webui/types.go`
- `webui` does not perform DB writes
- `webui` does not start workflows or call Cerberus
- HTTP layer loads data and calls renderer

Expected result:

- backlog-related UI is easy to find
- project-related UI is easy to find
- spec-related UI is easy to find
- workflow-related UI is easy to find
- draft/builder-related UI is easy to find
- settings/profile UI is easy to find
- transport helpers are not buried among templates

## Phase 4: Split Persistence By Entity

Status: Done.

Current problem: persistence is conceptually in the right place but physically too dense.

Keep one DB package unless a stronger reason appears.

Split by durable concept:

- projects
- specs
- workflows
- phases
- logs/events
- drafts
- profiles/settings

SQL behavior does not need compatibility with removed features. Keep only current product data.

Expected result:

- each DB file has row types and queries for one concept
- package errors stay central
- query helpers stay local to DB package
- obsolete rows/queries/tests are gone

## Phase 5: Split Workflow Orchestration From Phase Execution

Status: Done.

Current problem: workflow runner owns too many details in one place.

Keep one workflow package at first unless a real second responsibility emerges.

Separate files by level:

- runner lifecycle: start, stop, main loop
- phase execution: run one phase through external agent
- prompt construction: convert spec/phase/project context into prompt
- event publishing: workflow/phase/log notifications
- profile/session cleanup: temporary files and external-session hygiene
- feedback/result formatting: structured phase result payloads

Expected result:

- top of runner file explains workflow loop
- phase execution details do not obscure loop
- command cleanup does not obscure policy
- event formatting does not obscure state transitions

## Phase 6: Make External Tool Wrapper Boring

External tool wrappers should run commands and return results. Product prompts and state rules should live elsewhere.

Move any product-specific prompt content, workflow policy, or Draft Studio behavior out of external command wrappers.

Expected result:

- wrapper methods map closely to external commands
- callers provide product-specific prompt text
- wrapper has few dependencies beyond stdlib

## Phase 7: Delete Or Rehome Legacy Review Code

After workflow/API shape is clearer, inspect review-related code.

If no current flow calls it, delete it.

If a current flow does call it, place it under responsibility it serves:

- transport review endpoint belongs near transport
- workflow review policy belongs near workflow
- external model client belongs near integration
- result parsing belongs near parsing/translation

Do not keep a package only because it may be useful later.

## Phase 8: Compile Repair

After deletions and boundary splits, repair reduced app.

Repair order:

- imports
- type definitions
- route registration
- DB queries for remaining flows
- tests that describe remaining behavior

Do not resurrect removed features to make tests pass. Delete or rewrite those tests.

## Phase 9: README Repair

Update README to match reduced product.

Remove links to missing or removed concepts. Add one pointer to stable philosophy doc.

Expected result:

- README describes what exists now
- setup instructions are still accurate
- no broken docs links remain

## Done Criteria

- build passes after cleanup settles
- tests pass for remaining behavior
- memory-era code is gone from active paths
- compatibility code for removed behavior is gone
- API has clear HTTP edge separate from policy/presentation/streaming concerns
- large overloaded files are split by responsibility
- main flow appears near top of core files
- shared types are not mixed into orchestration bodies
- README does not advertise removed scope
- `docs/internal-package-boundaries.md` remains philosophy, not stale package inventory
