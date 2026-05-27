# Constraints

Tags: #constraints #architecture

## Current Stack

- Go backend using stdlib `net/http`.
- PostgreSQL using `pgx`.
- No ORM.
- Vanilla JS frontend today.
- Cerberus is the agent execution engine.
- Specs are markdown.

## Current Product Constraints

- Foundry is pre-alpha.
- Backend workflow model is ahead of frontend information architecture.
- UI should not become the source of truth.
- Logs, events, diffs, and decisions must remain inspectable.
- Because approved memory is loaded only from non-hidden `.md` files inside `<memory_repo_path>/<memory_namespace>`, future memory features must preserve namespace isolation and should not rely on hidden files, symlinks, or non-Markdown files as approved context (`internal/memory/memory.go`, `internal/memory/memory_test.go`).
- Because `projects` no longer store a memory repo path, specs should model memory as one configured private repo plus per-project namespaces, not per-project memory repositories (`migrations/006_project_memory_namespace.up.sql`, `migrations/007_drop_project_memory_repo_path.up.sql`).
- Because memory update acceptance writes and commits to the private memory repo while proposal/review state stays in Postgres, new automation must keep target source repos out of the memory write path (`internal/api/handlers.go`, `internal/memory/memory.go`).
- Because memory proposals deliberately omit prompt bodies, adjusted prompts, and raw review notes, future memory summaries should stay bounded to decisions, files touched, structured phase feedback, and explicit reviewer feedback unless a spec changes that privacy/noise boundary (`internal/api/handlers.go`, `internal/api/handlers_test.go`).
- Because executable phases are discovered only from `## Phase N: ...` headings, generated specs must keep that exact shape for work intended to run; extra prose or alternate heading levels are context, not phase definitions (`internal/spec/spec.go`, `internal/workflow/runner.go`).
- Because workflow SSE is a convenience layer over database state, UI changes should preserve the snapshot-on-connect/reload pattern instead of making in-memory hub delivery the only source of truth (`internal/api/handlers.go`, `internal/workflow/runner.go`, `web/app.js`).

## Future-Friendly Constraints

- Intent wiki should stay plain Markdown so Obsidian and git work naturally.
- Structured storage may index intent later, but should not replace Markdown as the first source of truth yet.
- New automation should preserve provenance: what source caused what update.
- Pending or rejected memory update jobs are not approved memory. Future spec generation and phase prompting should continue to exclude them unless a review boundary is explicitly redesigned (`migrations/008_memory_update_jobs.up.sql`, `internal/memory/memory.go`).
