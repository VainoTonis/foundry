# Foundry

A spec-driven, self-running development loop.

Here is intent. Here is agent work. Here is evidence. Here is decision.

Foundry is where ideas get turned into code. You write a spec, define phases, and Foundry runs them overnight via [cerberus](https://github.com/VainoTonis/cerberus) — automatically reviewing output, applying commits, and recording every decision made along the way.

The durable project memory lives in the [intent wiki](./intent/README.md), which is plain Markdown and Obsidian-friendly.

## What it does

- **Spec backlog** — dump ideas as markdown specs, they auto-queue and run as PoC
- **Two tracks** — PoC (fast, prove it works) and Polish (proper, tested, maintainable)
- **Automated phase loop** — cerberus runs each phase, an LLM reviews the diff, commits get applied, next phase starts
- **Decision memory** — every phase records what changed, why, and which files were touched

## How it works

```
spec (markdown) → phases parsed → cerberus runs each phase in a container
→ LLM reviews diff vs goal → pass: commit applied, next phase
→ fail: retry once with adjusted prompt → fail again: paused
→ all done: workflow complete
```

Runs unattended. Check results in the UI when you're back.

## Stack

- Go backend, stdlib `net/http`
- PostgreSQL (pgx, no ORM)
- Vanilla JS frontend, no framework
- [cerberus](https://github.com/VainoTonis/cerberus) as the agent execution engine

## Status

Pre-alpha. See [SPEC.md](./SPEC.md) for the full design.
