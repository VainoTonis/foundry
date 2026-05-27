# Open Questions

Tags: #questions #intent

## Intent System

- Should Foundry generate specs directly from [[Intent]], or should users explicitly approve compiled intent first?
- Should every accepted [[Decision]] update this wiki, or only decisions marked durable?
- Should [[Conversation]] history be a raw source for intent updates?
- How much structure should intent pages have before it becomes annoying to maintain?
- Current acceptance writes a single `workflow-updates/workflow-<id>.md` file in the project namespace; should memory updates remain append-only workflow notes, or should Foundry eventually generate reviewable diffs against existing intent pages (`internal/api/handlers.go`, `internal/memory/memory.go`)?

## Product Experience

- What should be the primary view: backlog, active run, or intent map?
- Should [[Activity]] include chat-derived events, or only machine/workflow events?
- How should Foundry show uncertainty without making the UI feel broken?

## Automation

- When should agents lint the intent wiki for stale or contradictory claims?
- Should Foundry support an explicit `intent ingest` operation?
- Should intent pages eventually be indexed into Postgres for search and graph views?
- The 1.0 plan describes Cerberus updating the memory repo and reviewing a diff, while current code asks Cerberus for proposal markdown and Foundry writes the accepted file. Is the long-term memory pass a diff-producing agent run, a proposal generator plus deterministic writer, or both (`intent/Foundry 1.0 Plan.md`, `internal/api/handlers.go`)?
- Phase feedback is currently synthesized from verdict, touched files, commit, and notes. Should Cerberus/agents produce richer structured phase feedback directly, or is synthesized feedback sufficient for 1.0 memory proposals (`internal/workflow/runner.go`, `migrations/009_phase_feedback.up.sql`)?
- The Cerberus client carries mutable repo/profile state while workflows, spec drafts, and memory proposals can all set it before launching commands. Should Foundry make Cerberus execution configuration immutable per command/session before enabling real concurrent runs (`internal/cerberus/cerberus.go`, `internal/workflow/runner.go`, `internal/api/handlers.go`)?
- Workflow phase rows are created only when a workflow has none; later spec edits do not resynchronize existing phases. Should running/paused workflows intentionally keep phase snapshots, or should the UI expose a deliberate “rebuild phases from spec” action (`internal/spec/spec.go`, `internal/workflow/runner.go`, `internal/db/queries.go`)?
