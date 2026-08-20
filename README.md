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

### Repositories subcommands

```sh
foundry repositories create [...]  # Create a new repository (JSON on stdin: name, local_path, remote_url)
foundry repositories list          # List all repositories
foundry repositories get <id>      # Get a repository by ID
foundry repositories update <id> [...] # Update a repository (JSON on stdin; null explicitly clears a locator)
foundry repositories delete <id>   # Delete a repository
```

Discovering local Git checkouts under `git_root` and refreshing missing `remote_url` values from each checkout's `origin` remote are web UI actions on the `/repositories` page, not separate CLI subcommands.

#### Repository locator rules

A repository is identified by `local_path`, `remote_url`, or both — at least one is required and enforced by the database. A repository with only `remote_url` is remote-only: it can be attached for context (chat, spec/draft ownership, feedback) but cannot be the primary repository of a plan that gets executed, since execution needs a local checkout.

Refresh (`/repositories?refresh=1` in the UI) fills in `remote_url` only for repositories that have a `local_path` but no `remote_url` yet, by reading that checkout's local `origin` remote and normalizing it. It never overwrites an existing `remote_url`, whether set by a previous refresh or configured explicitly.

#### Plan repository membership

A plan has an ordered, non-empty list of repositories (`repository_ids` on create/update, at least one required). Position 0 is the plan's **primary** repository. Running a plan only ever executes against the primary repository, and requires it to have a `local_path`; a remote-only primary returns a conflict instead of running. The other, non-primary repositories are context only.

#### Feedback scoping and legacy feedback

New free-form and structured session feedback must be linked to one or more repositories (`repository_ids`, at least one required) and can be filtered by `repository_id`. Feedback rows that existed before this scoping was introduced are preserved as-is with `scope_status: "legacy_unscoped"` and no repository links; they are excluded by repository filtering but otherwise behave like any other feedback row. `knowledge_feedback` is unrelated and was never scoped to repositories.

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
