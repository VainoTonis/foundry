# Product Model

Tags: #product #model

Foundry is an agent workbench and audit trail for turning specs into code changes.

It should make four things obvious:

- Here is [[Intent]].
- Here is [[Agent Work]].
- Here is [[Evidence]].
- Here is [[Decision]].

## Core Objects

[[Intent]] is durable product direction and project memory.

[[Spec]] is executable intent written as markdown.

[[Workflow]] is one run against one spec.

[[Phase]] is one bounded unit of work inside a workflow.

[[Agent Work]] is what Cerberus or another agent attempts during a phase.

[[Evidence]] is logs, diffs, tests, events, and review material.

[[Decision]] is the verdict and rationale for keeping, rejecting, retrying, or pausing work.

[[Activity]] is timestamped machine output while work happens.

[[Change]] is the concrete diff or artifact produced by work.

[[Conversation]] is temporary negotiation used to shape intent or specs.

[[Project Memory Namespace]] is the per-project directory inside the configured private memory repo. The project stores the target `repo_path` and namespace; the memory repo root is application config, not project state (`migrations/006_project_memory_namespace.up.sql`, `migrations/007_drop_project_memory_repo_path.up.sql`, `internal/db/queries.go`).

[[Memory Update Job]] is a workflow-scoped review artifact. While pending or rejected it lives in Postgres; only acceptance writes approved Markdown to the private memory repo (`migrations/008_memory_update_jobs.up.sql`, `internal/api/handlers.go`, `internal/memory/memory.go`).

[[Phase Feedback]] is structured raw signal stored on phases for memory proposals, not canonical memory (`migrations/009_phase_feedback.up.sql`, `internal/workflow/runner.go`).

## Product Shape

Backlog answers: what work exists?

Spec answers: what are we trying to build?

Run answers: what is the agent doing now?

Review answers: what changed, and is it safe?

Memory answers: what did we decide, and why?

## Implementation-backed Relationships

- Approved memory flows into both spec-builder prompts and workflow phase prompts by loading non-hidden Markdown files from the selected project's namespace. Future specs should treat pending memory update jobs as excluded from prompt context until accepted (`internal/api/handlers.go`, `internal/workflow/runner.go`, `internal/memory/memory.go`).
- A workflow gathers phase decisions, files touched, structured phase feedback, and reviewer feedback into a memory update proposal. Acceptance currently creates one `workflow-updates/workflow-<id>.md` file and commits it in the private memory repo, rather than editing arbitrary intent pages directly (`internal/api/handlers.go`, `internal/memory/memory.go`).
