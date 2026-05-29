# Foundry

Foundry 1.0 is a spec-driven, self-running development loop.

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

## Quick start

Run PostgreSQL with Docker Compose, then build and run Foundry on the host:

```sh
docker compose up -d postgres
go build -o ./foundry ./cmd/server
$EDITOR config.yaml
./foundry config.yaml
```

Compose starts PostgreSQL only. Foundry runs as the host binary `./foundry`, using `config.yaml`. Open `http://localhost:8080`.

If port `8080` is already in use, stop the conflicting service or change `server_port` in `config.yaml`.

Minimum useful config:

- `db_url` points to PostgreSQL.
- `git_root` points at the directory to scan for target repos.
- `memory_repo_path` points at a private git repo for approved Markdown memory.
- `cerberus_bin` points at the Cerberus CLI, or `cerberus` is on `PATH`.

On startup, Foundry loads config, runs migrations, connects to PostgreSQL, and serves the UI/API.

## Documentation

- [Install](./docs/install.md)
- [Self-hosting](./docs/self-hosting.md)
- [Private memory repo](./docs/private-memory.md)
- [Cerberus integration](./docs/cerberus.md)
- [Troubleshooting](./docs/troubleshooting.md)

## Status

Pre-alpha. See [SPEC.md](./SPEC.md) for the full design.
