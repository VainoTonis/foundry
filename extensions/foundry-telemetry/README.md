# foundry-telemetry

A [pi](https://github.com/earendil-works/pi-mono) extension that emits
best-effort, producer-neutral telemetry from a running Pi session to a
Foundry server's `POST /api/telemetry/events` endpoint (see
`internal/httpserver/telemetry_events.go` and `internal/telemetry/ingest.go`).

## Installation

Copy or symlink this directory into a location Pi auto-discovers:

```bash
# Project-local (this repo only)
mkdir -p .pi/extensions
ln -s "$(pwd)/extensions/foundry-telemetry" .pi/extensions/foundry-telemetry

# Or globally (all projects)
ln -s "$(pwd)/extensions/foundry-telemetry" ~/.pi/agent/extensions/foundry-telemetry
```

Pi loads `index.ts` directly via `jiti` — no build step and no
`npm install`, since the extension has no runtime dependencies (see
Fidelity notes below).

Restart Pi, or run `/reload` if the extensions directory is already
watched, then run `/foundry-telemetry` to confirm it loaded.

## Configuration

| Variable | Description |
|---|---|
| `FOUNDRY_TELEMETRY_TRUSTED_ROOTS` | Required list of trusted project roots, separated by the platform path delimiter (`:` on Unix, `;` on Windows). Capture is off when unset. At `session_start`, both cwd and roots are realpath-resolved; cwd must equal or be inside a root. |
| `FOUNDRY_TELEMETRY_URL` | Full telemetry endpoint URL to POST events to. Defaults to `http://localhost:8080/api/telemetry/events`. |
| `FOUNDRY_TELEMETRY_BEARER_TOKEN` | Optional bearer credential sent in the `Authorization` header. It is never included in status output. |
| `FOUNDRY_TELEMETRY_SPOOL_PATH` | Base path for durable, source-session-owned JSONL outboxes. Defaults to `~/.pi/agent/foundry-telemetry/events.jsonl`; records live under its `.sessions/` directory. |
| `FOUNDRY_TELEMETRY_PRODUCER_ID` | Producer namespace. Defaults to `pi:<hostname>`; the Pi source session ID is appended to form the wire identity. |
| `FOUNDRY_TELEMETRY_MAX_EVENTS` | Maximum queued events (default 10,000). |
| `FOUNDRY_TELEMETRY_MAX_BYTES` | Maximum queued bytes (default 16 MiB). |
| `FOUNDRY_TELEMETRY_TOOL_BODY_DENYLIST` | Optional comma-separated, case-insensitive tool names whose input and result bodies are omitted. Tool identity and timing/error metadata remain, with explicit omission flags. |

Set `FOUNDRY_TELEMETRY_TRUSTED_ROOTS` explicitly before capture can begin.

### Secure local setup

The repository `config.yaml` explicitly permits unauthenticated telemetry only
from loopback clients. Enable capture for selected projects:

```bash
export FOUNDRY_TELEMETRY_TRUSTED_ROOTS="$HOME/git/work:$HOME/git/personal"
export FOUNDRY_TELEMETRY_URL="http://localhost:8080/api/telemetry/events"
```

For authenticated local delivery, set the same secret for Foundry and Pi:

```bash
export FOUNDRY_TELEMETRY_BEARER_TOKEN="$(openssl rand -hex 32)"
# Start Foundry and Pi from the environment containing this variable.
```

When a token is configured, Foundry requires it even from loopback clients
unless `telemetry_allow_unauthenticated` remains explicitly enabled.

### Secure remote setup

Never send the bearer token over plaintext HTTP. Expose Foundry only through
HTTPS or a trusted VPN, disable unauthenticated ingestion, and use matching
credentials on server and producer:

```yaml
# Foundry config.yaml
telemetry_allow_unauthenticated: false
telemetry_bearer_token: "replace-with-a-random-secret"
```

```bash
# Pi producer
export FOUNDRY_TELEMETRY_TRUSTED_ROOTS="$HOME/git/approved-projects"
export FOUNDRY_TELEMETRY_URL="https://foundry.example.com/api/telemetry/events"
export FOUNDRY_TELEMETRY_BEARER_TOKEN="replace-with-a-random-secret"
```

Prefer `FOUNDRY_TELEMETRY_BEARER_TOKEN` over storing the server token in YAML.
To rotate it, update/restart Foundry first, then update/reload producers; queued
events remain in their durable spools and deliver after credentials match.
Treat spool files as sensitive telemetry and restrict their filesystem access.

## What it sends

| Pi event | Telemetry event | Notes |
|---|---|---|
| `session_start` | `session_start` | Includes semantic schema version, Pi source UUID, mode, safely resolved Git root (absent outside Git), current model, and parent header path/source UUID when readable |
| `session_shutdown` | `session_end` | Includes the close reason; locally appended before the old delivery worker is stopped and awaited |
| `message_end` (assistant) | `message_end` + terminal `final_message` | `message_end` carries usage, provider/model/thinking, and stop reason for every assistant turn. Only delivered terminal text (`stop`/`length`) becomes a final outcome; intermediate tool-calling text does not. |
| `message_end` (user) | `final_message` only | Carries a stable source message ID and input provenance (`interactive`, `harness` for RPC/non-TUI, or `extension`); it is evidence but is not marked as a final outcome |
| `tool_execution_start` | `tool_use` | `tool_call_id`, `tool_name`, deterministically serialized `tool_input` |
| `tool_execution_end` | `tool_result` | Same `tool_call_id`/`tool_name`, serialized result text, `is_error`, `duration_ms` measured from the matching `tool_execution_start` |

Before any spool write, common API-key, token, password, authorization,
and PEM private-key patterns are replaced with `[REDACTED]`; affected events
carry explicit redaction flags. Denylisted tool events omit their body fields
and carry explicit omission flags while retaining tool name/call identity and
other structural metadata.

All events include the current model and cwd attribution when available.
Command handlers, `/foundry-telemetry`, and the extension factory itself
never touch the LLM context or block tool execution.

## Status command

Run `/foundry-telemetry` at any time to see:

- Whether capture is enabled, disabled, or the cwd is untrusted
- The resolved endpoint URL
- The current session key
- Counts of sent / failed batches / queued events
- Queued disk bytes and dropped events
- The last event type and timestamp
- The last error message, if any

This is the only user-visible surface — the extension does not otherwise
notify, prompt, or interrupt.

## Verification

A mocked-`fetch` test exercises deterministic concurrent source-session
spool isolation (including acknowledgement compaction and restart recovery),
durability-first hook completion, network-outage-independent hook latency,
immediate-restart recovery, delivery ordering, deterministic same-session reload overlap (including abort and prevention of stale acknowledgement/compaction), fail-open continuation past
a failing/rejecting POST, `final_message` exclusion of
thinking/reasoning, tool-call, image, and unknown content blocks, a
completed user message being captured as `final_message` evidence ahead
of a later assistant `final_message` (with no `message_end`/usage row for
the user turn and assistant usage/privacy behavior unchanged), and empty
or non-text-only user content producing no `final_message`. It has
no dependencies beyond Node's built-in TypeScript stripping:

```bash
node --experimental-strip-types extensions/foundry-telemetry/index.test.ts
```

## Fidelity notes / design constraints

- **Type-only dependency.** The only import from `@earendil-works/pi-coding-agent`
  is `import type { ExtensionAPI }`. There is no runtime dependency on any
  npm package (including Pi's own packages), so the extension works with
  zero `npm install` under `jiti`.
- **Durability-first without network delivery blocking.** Event handlers
  await their serialized local spool append, so each event is recoverable
  before the hook returns. Delivery starts in the background. On shutdown,
  `session_end` is appended first and the worker is then stopped and awaited;
  the hook does not wait for delivery to succeed. Append/serialization/network
  errors are caught and only recorded in counters surfaced by
  `/foundry-telemetry`; no handler returns a value that could mutate message
  or tool behavior.
- **Bounded network and explicit shutdown.** Every request carries
  `AbortSignal.timeout()` so a slow or unreachable Foundry endpoint cannot
  hang the delivery worker indefinitely. Shutdown also aborts the active
  request, interrupts retry backoff, rejects future starts, and waits for the
  worker to exit without performing a post-stop acknowledgement. A reloaded
  extension can therefore safely recover the same session spool.
- **Source-session ownership.** Producer identity and spool, sequence, and
  compaction paths are derived deterministically from Pi's source session ID.
  Concurrent Pi sessions therefore share neither event IDs nor mutable files;
  reopening the same source session recovers its pending records and sequence.
- **Serialized, in-order delivery.** Disk appends, batch delivery, and
  acknowledgement compaction preserve emission order across failures and
  process restarts. Recovery delivery runs in the bounded background worker.
- **`final_message` is terminal, text-only evidence.** Assistant evidence is
  emitted only for delivered terminal (`stop`/`length`) messages and includes
  only `type: "text"` blocks. Intermediate tool-calling text, reasoning,
  images, and unknown blocks are not final outcomes. User evidence retains its
  input provenance and stable Pi message identity but has `is_final: false`.
- **Durable, bounded at-least-once delivery.** Events are assigned a stable
  producer/event identity and monotonically increasing client sequence,
  appended to a bounded disk outbox, and sent in ordered batches. Failed
  batches retry with exponential backoff. Accepted and duplicate outcomes
  are removed by atomic outbox compaction; permanent server rejections are
  counted as drops. The receipt ledger makes crash-window redelivery safe.
- **Pre-spool privacy boundary.** Secret-pattern redaction and configured
  tool-body omission happen immediately before `DiskSpool.append`, so raw
  values (and hashes of them) are written neither to the durable outbox nor
  to network payloads. Events mark redacted or omitted evidence explicitly.
- **Deterministic serialization.** Tool input/result and message content
  are serialized with sorted object keys so the same logical event
  produces the same JSON bytes across runs, independent of property
  insertion order.
- **Producer-neutral wire format.** The DTO shape sent here matches
  `telemetryEventDTO` in `internal/httpserver/telemetry_events.go`
  exactly (same field names, same event `type` values), so this
  extension is one interchangeable producer among potentially several
  (Pi, other agents, CI) that can all write to the same
  `/api/telemetry/events` endpoint.
