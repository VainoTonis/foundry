# Install Foundry 0.9

Foundry 0.9 is a Go web server backed by PostgreSQL. The checked-in `docker-compose.yml` starts PostgreSQL only; run the Foundry server from source or build the `Dockerfile` image yourself.

## Requirements

- Go 1.22+
- PostgreSQL 16 compatible database
- `git`
- `cerberus` CLI on `PATH` or configured by absolute path
- A target git root containing the repositories Foundry will operate on
- Optional but recommended: a separate private git repo for project memory

## Local install

```sh
git clone <foundry-repo>
cd foundry
docker compose up -d postgres
cp config.yaml config.local.yaml
$EDITOR config.local.yaml
go run ./cmd/server config.local.yaml
```

Open `http://localhost:8080` unless `server_port` was changed.

On startup Foundry loads the YAML config, runs all migrations from `migrations/`, connects to PostgreSQL, creates a Cerberus client, and serves the UI/API.

## Important config keys

- `db_url`: PostgreSQL DSN. The compose database uses `postgres://foundry:foundry@localhost:5432/foundry?sslmode=disable`.
- `server_port`: HTTP port; default is `8080`.
- `git_root`: root scanned by **Discover repos**.
- `memory_repo_path`: private memory repo path used by spec builder and approved memory.
- `cerberus_bin`: Cerberus executable; default `cerberus`.
- `cerberus_image`, `cerberus_model`: optional CLI overrides.
- `cerberus_profile`: optional name of a profile saved in Foundry settings.
- `max_concurrent_workflows`, `default_workflow_budget_usd`, `default_phase_timeout_seconds`: workflow defaults.

`review_*` keys are currently config fields only; review uses Cerberus output in this version.
