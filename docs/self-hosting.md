# Self-hosting Foundry 0.9

Foundry has three runtime dependencies: PostgreSQL, the Foundry server, and Cerberus.

## PostgreSQL

The repository compose file is intentionally minimal:

```sh
docker compose up -d postgres
```

It exposes PostgreSQL on `localhost:5432` with database/user/password `foundry`.

## Foundry server

Run from source:

```sh
go run ./cmd/server /path/to/config.yaml
```

Or build the image:

```sh
docker build -t foundry:0.9 .
```

If containerizing the server, mount:

- `config.yaml`
- the target repositories under the same paths referenced by project `repo_path`
- the private memory repo at `memory_repo_path`
- any credentials Cerberus needs

The bundled image contains the Foundry binary, migrations, web assets, default config, CA certificates, and git. It does not install Cerberus.

## Network notes

Foundry passes `http://localhost:<server_port>/api/cerberus/events` to Cerberus as the callback URL. This works when Cerberus can reach the Foundry server through the same localhost namespace. If Foundry or Cerberus runs in separate containers/hosts, expose routing so that callback URL is reachable or run them together.

## Persistence

Persist PostgreSQL data and keep target repos plus the memory repo on durable storage. Foundry stores workflow state in PostgreSQL; Cerberus applies accepted phase commits to the target git repo by cherry-picking from the Cerberus session branch.
