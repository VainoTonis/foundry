# 002: Spec Creation — Conversational Input with Hard Freeze

## Problem

Specs are currently created by posting raw markdown to `POST /api/specs` or editing
in the UI textarea. This works but means the user has to manually structure phases,
write global context, and know the `## Phase N:` format.

The plan is to add a chatbox for conversational spec creation. This introduces a new
risk: if the conversation and the spec can diverge, or if the spec can be modified
after execution starts, the system loses its contract guarantees.

## Design principles (from openclaw analysis)

1. **Separate "what the user said" from "what the agent gets."**
   Store the raw conversation. Also store the extracted structured spec. Don't
   re-derive phases from chat history at execution time.

2. **The chatbox is input; the spec is the contract.**
   All flexibility lives in the conversation. None of it leaks into execution.

3. **Bound the conversation-to-spec step.**
   At some point the conversation ends and a frozen spec exists. No mid-run edits.

4. **Don't let the chatbox become a hidden config surface.**
   If the user says "use a 30 minute timeout," parse it into an explicit field
   or reject it.

## Solution

### Conversation flow

```
user opens "New Spec" for a project
  -> chatbox UI opens
  -> user describes what they want (free-form)
  -> LLM asks clarifying questions, proposes phases
  -> user confirms or adjusts
  -> LLM emits structured spec JSON (title, global_context, phases[], allowed_paths[], tags[])
  -> UI shows preview: rendered spec in the same format as existing specs
  -> user confirms
  -> spec created in DB with status=dumpster, conversation stored separately
  -> spec is now frozen for execution purposes
```

### Data model

```sql
-- Migration 005_spec_conversations.up.sql

CREATE TABLE spec_conversations (
    id         BIGSERIAL PRIMARY KEY,
    spec_id    BIGINT REFERENCES specs(id) ON DELETE SET NULL,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    messages   JSONB  NOT NULL DEFAULT '[]',
    status     TEXT   NOT NULL DEFAULT 'active' CHECK (status IN ('active','finalized','abandoned')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON spec_conversations(spec_id);
CREATE INDEX ON spec_conversations(project_id);
```

`messages` is a JSON array:

```json
[
  {"role": "user", "content": "I need a REST API for user auth..."},
  {"role": "assistant", "content": "What database are you using?..."},
  {"role": "user", "content": "Postgres, pgx..."},
  {"role": "assistant", "content": "Here's what I'd propose...", "spec_draft": {...}}
]
```

The `spec_draft` field on assistant messages is optional. When present, the UI
renders a preview. The last message with `spec_draft` is what gets finalized.

### Finalization

When the user confirms the preview:

1. Extract `spec_draft` from the last assistant message that has one.
2. Render it to markdown (the `## Phase N:` format the parser expects).
3. Create the spec via `db.CreateSpec` — same as today.
4. Link: set `spec_conversations.spec_id`, status = `finalized`.
5. The spec content field contains the rendered markdown. The conversation
   is the audit trail, not the source of truth for execution.

### Config extraction

If the user mentions timeout, budget, or allowed_paths in conversation, the
spec-creation LLM should extract them into explicit fields in `spec_draft`:

```json
{
  "title": "User auth API",
  "global_context": "Go backend, pgx, stdlib net/http...",
  "phases": [
    {"name": "Schema and migrations", "goal": "Create user table..."},
    {"name": "Handler layer", "goal": "..."}
  ],
  "allowed_paths": ["internal/auth/", "migrations/"],
  "tags": ["auth", "api"],
  "timeout_seconds": 1200,
  "max_cost_usd": 3.00
}
```

These map to existing DB fields. If the user doesn't mention them, defaults
from config.yaml apply. The chatbox is never the source of truth for config
at runtime — it's a structured extraction step.

### Editing after creation

Editing the spec content (via PATCH /api/specs/:id) is allowed only when no
workflow is running. The API should reject edits when any workflow for the spec
has status=running. This is the "freeze" guarantee.

To change a spec with a completed workflow: edit freely, but the next workflow
run gets the new content. Old workflows keep their snapshot (prompt_sent on
each phase row).

### What this does NOT do

- Does not replace the raw markdown creation path. Power users can still
  POST /api/specs with hand-written markdown.
- Does not stream spec creation — the LLM call is a single request/response
  per turn.
- Does not version specs. Edit history comes from the conversation + git.

### Build order

1. Migration `005_spec_conversations.up.sql`
2. `internal/db/queries.go` — CRUD for spec_conversations
3. `internal/specchat/` — new package. Handles the LLM conversation for spec
   creation. Input: project context + user message + conversation history.
   Output: assistant message (possibly with spec_draft).
4. `internal/specchat/extract.go` — converts spec_draft JSON to markdown.
5. `internal/api/handlers.go` — new endpoints:
   - `POST /api/conversations` — start a new spec conversation
   - `POST /api/conversations/:id/message` — send a message, get response
   - `POST /api/conversations/:id/finalize` — create spec from last draft
   - `GET /api/conversations/:id` — get conversation with messages
6. `internal/api/handlers.go` — add freeze check to `PATCH /api/specs/:id`
7. `web/app.js` — chatbox UI, preview rendering, finalize button
