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

## Future-Friendly Constraints

- Intent wiki should stay plain Markdown so Obsidian and git work naturally.
- Structured storage may index intent later, but should not replace Markdown as the first source of truth yet.
- New automation should preserve provenance: what source caused what update.
