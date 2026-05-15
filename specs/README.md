# Foundry Design Specs

Build specs for foundry. Each file is a self-contained design decision with
enough detail to implement directly.

These are not ideas — they describe what to build, why, and where it touches
the existing code.

## Index

- [001-track-rules](001-track-rules.md) — Mechanical enforcement for PoC vs Polish tracks
- [002-spec-creation](002-spec-creation.md) — Conversational spec creation with hard freeze boundary
- [003-seam-discipline](003-seam-discipline.md) — Keep cerberus details out of the workflow runner
- [004-prompt-cache-stability](004-prompt-cache-stability.md) — Deterministic ordering for prompt cache hits
- [005-doctor-command](005-doctor-command.md) — Explicit repair command instead of startup auto-migration
- [006-config-contract](006-config-contract.md) — Config struct as schema, strict parsing, retired key tracking
- [007-philosophy](007-philosophy.md) — Decision-making principles when the specs don't cover it
- [008-realtime-streaming](008-realtime-streaming.md) — Cerberus event callback + SSE to browser for live agent output
