# Troubleshooting Foundry 0.9

## Server will not start

- Check `db_url` and that PostgreSQL is reachable.
- Run `docker compose up -d postgres` for the bundled database.
- Make sure `migrations/` is present relative to the server working directory.
- Pass the intended config path: `go run ./cmd/server config.local.yaml`.

## Discover repos says `git_root not configured`

Set `git_root` in config, for example:

```yaml
git_root: "~/git"
```

Restart the server after config changes.

## Spec builder says memory repo path is not configured

Set `memory_repo_path` to an existing private git repo and restart. Spec builder requires the memory repo.

## No approved memory is loaded

Check that the project has a `memory_namespace`, that the directory exists under `memory_repo_path`, and that it contains non-hidden `.md` files. Hidden files, hidden directories, `.git`, symlinks, and non-Markdown files are not loaded.

## Accepting memory update fails

Common causes:

- `memory_repo_path` is empty or not a git repo.
- Project `memory_namespace` is empty or invalid.
- Git user identity is not configured in the memory repo.
- The memory repo has conflicting lock/index state.

Try:

```sh
git -C <memory_repo_path> status
git -C <memory_repo_path> config user.name
git -C <memory_repo_path> config user.email
```

## Cerberus fails to start

- Verify `cerberus_bin` or `PATH`.
- Verify any configured image/model/profile values.
- Confirm Cerberus can access required credentials.
- Check the phase review notes and logs in the workflow page.

## Callback/events do not stream

Foundry uses `http://localhost:<server_port>/api/cerberus/events` as the Cerberus callback URL. If Cerberus runs outside the server's localhost namespace, make that address reachable or run both processes in the same host/container network.

## Cherry-pick fails

Foundry aborts partial cherry-picks and marks the phase failed. Inspect the target repo status and resolve conflicts manually before retrying with a new workflow/phase.

## A phase produced no diff

Foundry marks that attempt as failed and retries once with an adjusted prompt. If the retry also produces no diff, the phase fails.
