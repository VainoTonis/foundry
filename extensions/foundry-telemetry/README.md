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
| `FOUNDRY_TELEMETRY_URL` | Full telemetry endpoint URL to POST events to. Defaults to `http://localhost:8080/api/telemetry/events` (a local Foundry server with the default `server_port: 8080`). |

Point `FOUNDRY_TELEMETRY_URL` at a remote Foundry instance, e.g.
`https://foundry.example.com/api/telemetry/events`, to centralize
telemetry from multiple machines.

## What it sends

| Pi event | Telemetry event | Notes |
|---|---|---|
| `session_start` | `session_start` | `session` (namespaced `pi:<uuid>`), `source_session_id` (raw Pi session UUID), `origin: "pi-coding-agent"`, `kind` (`ctx.mode`), `repo_path` (`ctx.cwd`), `model` (`provider/id`), `parent_session` (from the session header, set for `/fork`/`/clone`/`newSession({ parentSession })`) |
| `session_shutdown` | `session_end` | Sent and awaited (bounded) before Pi tears down the extension |
| `message_end` (assistant) | `message_end` + `final_message` | `message_end` carries token usage and cost; `final_message` carries deterministically serialized text evidence of the assistant's reply |
| `message_end` (user) | `final_message` only | Captures a completed, non-empty plain-text user message as evidence, in serialized event order; never produces a `message_end`/usage row and never captures deltas, reasoning, images, or tool results |
| `tool_execution_start` | `tool_use` | `tool_call_id`, `tool_name`, deterministically serialized `tool_input` |
| `tool_execution_end` | `tool_result` | Same `tool_call_id`/`tool_name`, serialized result text, `is_error`, `duration_ms` measured from the matching `tool_execution_start` |

All events include the current model and cwd attribution when available.
Command handlers, `/foundry-telemetry`, and the extension factory itself
never touch the LLM context or block tool execution.

## Status command

Run `/foundry-telemetry` at any time to see:

- The resolved endpoint URL
- The current session key
- Counts of sent / failed / in-flight requests
- The last event type and timestamp
- The last error message, if any

This is the only user-visible surface — the extension does not otherwise
notify, prompt, or interrupt.

## Verification

A mocked-`fetch` test exercises delivery ordering (session_start before
activity, session_end after a bounded drain), fail-open continuation past
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
- **Never mutates or blocks agent behavior.** Every `pi.on(...)` handler
  here is fire-and-forget for its network call (`emit()` does not `await`
  the underlying `fetch`), returns nothing, and never throws — all
  serialization and network errors are caught and only recorded in local
  counters surfaced by `/foundry-telemetry`. No handler returns a value
  that pi could act on (no blocking, no message/tool mutation).
- **Bounded network waits.** Every request carries `AbortSignal.timeout()`
  so a slow or unreachable Foundry endpoint cannot hang a request
  indefinitely. `session_shutdown` enqueues `session_end` and then races
  the drain of the whole send queue (everything still in flight,
  including `session_end` itself) against a fixed timeout, so shutdown
  never blocks on the network.
- **Serialized, in-order delivery.** All telemetry POSTs are chained onto
  a single send queue, so `fetch` is called in the same order events were
  emitted — `session_start` always precedes activity events, and
  `session_end` is only sent after every event enqueued before it (bounded
  by the shutdown drain timeout above). A failed or slow send never drops
  or reorders the events queued after it (fail-open).
- **`final_message` is text-only evidence.** Unlike tool call/result
  evidence (which serializes whatever the tool produced), the assistant
  `final_message` event only includes `type: "text"` content blocks.
  Thinking/reasoning blocks, tool-call blocks, image bytes, and any
  unrecognized block type are dropped, never serialized as a placeholder.
- **Best-effort, not exactly-once.** If the endpoint is unreachable,
  events are dropped (not queued to disk or retried). This matches the
  server's own posture: `EnsureAgentSession` is idempotent
  (`ON CONFLICT (session) DO UPDATE`), but there is no durable client-side
  outbox in this extension.
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
