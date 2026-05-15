# 008: Real-time Streaming — Cerberus Events to Browser

## Problem

Foundry shells out to cerberus and captures all stdout into a buffer after the process
exits. This has three consequences:

1. **Multiple agent messages collapse into one.** If the agent reads files then responds,
   the text from both messages gets concatenated with raw JSON tool events mixed in.
   `stripCerberusOutput` can't distinguish message boundaries.

2. **No real-time feedback.** The spec builder UI polls every 3 seconds. The user stares
   at "AI is thinking…" for 60+ seconds with no idea what's happening.

3. **Lost structure.** Token usage, tool calls, and cost data are available in the pi
   event stream but cerberus discards them before foundry sees anything.

## What changed in cerberus

Cerberus now has two new flags on `start`, `chat`, `message`, and `rerun`:

- `--output jsonl` — writes one JSON event per line to stdout instead of terminal-formatted text
- `--callback <url>` — POSTs each event as JSON to a URL as it happens

Both can be used simultaneously. The event types (defined in `cerberus/internal/event/event.go`):

```
session_start  — {session_id}
text_delta     — {content}           incremental text from the agent
message_end    — {usage}             token counts + cost for one LLM call
turn_complete  — {status, exit_code} emitted by the caller after docker exits
log            — {content}           non-JSON output from the container
raw            — {content}           unrecognized pi JSON events (tool use, etc.)
```

Every event has `type`, `session`, and `ts` fields.

## Solution

### 1. Foundry receives cerberus events via callback

Add `POST /api/cerberus/events` endpoint. When foundry starts a cerberus session
(workflow phases or spec builder), it passes `--callback http://localhost:<port>/api/cerberus/events`.

The handler:
- Looks up the session name from the event's `session` field
- Routes to the right phase or spec draft
- Writes the event to a new `events` JSONB column or a dedicated events table
- Forwards the event to any connected SSE clients

### 2. Foundry stops parsing cerberus stdout

Replace the current `cerberus.Client` methods (`Chat`, `Message`) that capture stdout
and run `stripCerberusOutput`. Instead:

- `Chat` / `Message` still shell out to cerberus, but with `--callback` and `--output jsonl`
- Foundry no longer reads stdout for message content — the callback delivers events
- The process exit code is the only thing read from the subprocess

The `stripCerberusOutput` function and the `c.output()` pattern are deleted.

### 3. Spec builder gets real-time SSE

Replace the 3-second polling in `renderDraftChat` with an SSE connection:

```
GET /api/spec-drafts/:id/stream
```

Server side: when events arrive via the callback endpoint for this draft's session,
forward them as SSE `data:` lines.

Client side: `EventSource` connects on draft open. On `text_delta` events, append
tokens to the current assistant message bubble in real-time. On `message_end`, mark
the message as complete. On `turn_complete`, re-enable the input box.

### 4. Messages stored with proper boundaries

Currently `appendMessage` stores one flat string per assistant turn. With the event
stream, foundry sees `message_end` events that mark where one LLM message ends and
the next begins (tool use happens between messages).

Change the message storage: each `message_end` triggers a new message entry. Accumulate
`text_delta` content until `message_end`, then store the complete message. Multiple
assistant messages per turn are stored as separate entries.

### 5. Workflow phase logs use the same pipeline

The phase log SSE (`/api/phases/:id/logs/stream`) currently polls the `phase_logs`
table every 2 seconds. With the callback, cerberus events for workflow phases arrive
in real-time too. Wire the same callback endpoint to phase log streaming.

This means `text_delta` events show the agent's thinking live in the phase detail view,
not just raw container logs.

## Data model changes

Option A — events column on existing tables:
```sql
ALTER TABLE spec_drafts ADD COLUMN events JSONB DEFAULT '[]';
-- events are the raw stream, messages are the assembled result
```

Option B — dedicated events table (better for high-frequency writes):
```sql
CREATE TABLE cerberus_events (
  id BIGSERIAL PRIMARY KEY,
  session TEXT NOT NULL,
  event_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_cerberus_events_session ON cerberus_events(session);
```

Option B is preferred — avoids JSONB append contention and lets SSE do
`SELECT ... WHERE session = $1 AND id > $2 ORDER BY id` for catch-up.

## Implementation order

1. **Migration** — create `cerberus_events` table
2. **Callback endpoint** — `POST /api/cerberus/events` writes to table
3. **SSE endpoint** — `GET /api/events/stream?session=X` reads from table + forwards new
4. **Spec builder** — switch `cerberus.Client.Chat/Message` to use `--callback`, remove `stripCerberusOutput`
5. **Spec builder UI** — replace polling with SSE, render `text_delta` events live
6. **Message assembly** — accumulate deltas, split on `message_end`, store proper messages
7. **Workflow phases** — wire phase runner to use `--callback`, connect phase log SSE

## Files touched

```
internal/cerberus/cerberus.go   — remove stripCerberusOutput, add --callback/--output flags to commands
internal/api/handlers.go        — add callback endpoint, SSE endpoint, update spec draft handlers
internal/db/queries.go          — add cerberus_events table queries
migrations/004_cerberus_events.up.sql
web/app.js                      — replace polling with EventSource, render deltas live
```

## What this does NOT cover

- Remote cerberus (events over the network) — callback already works for this but
  foundry assumes localhost today. Future spec.
- Workflow cost tracking from events — the `message_end` usage data could replace
  the current `cost_usd` tracking on phases. Deferred.
- Tool use visibility in the UI — `raw` events contain tool calls but we don't
  parse or display them yet.
