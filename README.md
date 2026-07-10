# Foundry

Foundry is a spec-driven, self-running development loop.

Here is intent. Here is agent work. Here is evidence. Here is decision.

Foundry is where ideas get turned into code. You write a spec, define phases, and Foundry runs them via [cerberus](https://github.com/VainoTonis/cerberus), applying successful commits and recording what happened along the way.

## What it does

- **Spec backlog** — save markdown specs and run them as PoC or polish workflows
- **Two tracks** — PoC (fast, prove it works) and Polish (proper, tested, maintainable)
- **Automated phase loop** — cerberus runs each phase, produced commits get applied, next phase starts
- **Phase evidence** — every phase records status, logs, files touched, and failure context
- **Draft Studio** — chat with cerberus to shape a spec before saving it

## How it works

```
spec (markdown) → phases parsed → cerberus runs each phase in a container
→ diff and commit captured → pass: commit applied, next phase
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
go build -o ./foundry-server ./cmd/server
$EDITOR config.yaml
./foundry-server config.yaml
```

Compose starts PostgreSQL only. Foundry runs as the host binary `./foundry-server`, using `config.yaml`. Open `http://localhost:8080`.

If port `8080` is already in use, stop the conflicting service or change `server_port` in `config.yaml`.

Minimum useful config:

- `db_url` points to PostgreSQL.
- `git_root` points at the directory to scan for target repos.
- `cerberus_bin` points at the Cerberus CLI, or `cerberus` is on `PATH`.

On startup, Foundry loads config, runs migrations, connects to PostgreSQL, and serves the UI/API.

## Foundry CLI

The `foundry` CLI is the programmatic/agent interface to Foundry. Build it separately:

```sh
go build -o foundry ./cmd/foundry
```

The CLI communicates with a running Foundry server via HTTP (default `http://localhost:8080`).

### Plans subcommands

```sh
foundry plans create [...]     # Create a new plan
foundry plans get <id>         # Get a plan by ID
foundry plans list             # List all plans
foundry plans update <id> [...] # Update a plan
foundry plans update-step [...] # Update a plan step
```

### Projects subcommands

```sh
foundry projects create [...]  # Create a new project
foundry projects list          # List all projects
foundry projects get <id>      # Get a project by ID
foundry projects update <id> [...] # Update a project
foundry projects delete <id>   # Delete a project
foundry projects discover      # Discover projects
```

### Global options

```sh
--url string   # Foundry server URL (default "http://localhost:8080")
```

Example with custom server:

```sh
foundry --url http://localhost:9000 plans list
```

## Notes

Internal package rules live in [docs/internal-package-boundaries.md](./docs/internal-package-boundaries.md).

## Status

Pre-alpha. Internal cleanup still in progress.
