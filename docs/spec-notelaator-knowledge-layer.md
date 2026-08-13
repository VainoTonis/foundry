# Spec A — Notelaator knowledge layer for agents

## Goal

Let every agent (host orchestrator and sandboxed cerberus subagents) **read** the
notelaator vault through one bounded CLI, and **file structured grievances** about
what they read, without any agent ever writing to the vault directly. Draining
those grievances into vault commits is a separate, serialized activity.

## Non-goals

- No web research / net access for agents. Sandbox egress stays as-is
  (`cerberus/proxy/squid.conf`: LLM providers only, `http_access deny all`).
- No embeddings, vector DB, or local "librarian" model. 90 notes; frontmatter
  topics + grep is deterministic and faster.
- No MCP server. CLI + read-only mount works in one-shot containers.
- No new binary. Extend `frontmatter-radar`, one extra artifact in the agent image.
- No foundry auth layer → **Spec B**. Declared dependency, not built here.
- No scheduler, no automated curator run → **Spec C**. Interim drain is manual.
- No changes to the existing `/feedback` prompt or `feedback` table (session
  quality scoring). Separate concern, separate table.

## Key decisions

1. **Agents never write to the vault.** N concurrent cerberus sessions each have
   their own worktree/branch; letting them commit notes (or append to a jsonl in
   the vault) produces git conflicts on every run. Writes happen in exactly one
   serialized place.
2. **Grievances live in postgres, not the vault.** DB handles concurrent writes.
   New table `knowledge_feedback`, distinct from the existing `feedback` table —
   different subject (a note vs a run), different fields, different consumer,
   different lifecycle.
3. **Read layer is a CLI, not a raw mount.** Bounded output, maturity labels on
   every hit, zero write surface, identical invocation on host and in container.
4. **`maturity` is the authorization model**, reused from vault `AGENTS.md`:
   `raw` notes are patchable, `curated` notes are proposal-only. Enforced by hook,
   not prose.
5. **Container writes wait for Spec B.** Reaching foundry from the sandbox
   requires allowlisting `foundry` in squid, which exposes the whole unauthed API
   to every container. Slice 1 is host-only feedback.
6. **Evidence is mandatory** on every grievance, rejected at API level if empty.
   Stops vibes-based feedback and gives the drain step something to dedupe on.
7. **Drain is manual in v1.** Owner runs a janitor pass in a host pi session
   against `GET /api/knowledge-feedback?status=open`. Automating it (dedupe rules,
   cerberus curator session, timer) is Spec C — recurring jobs is a foundry-wide
   capability, not a vault feature.

## Risks

- **Feedback spam** from agents misreading notes. Mitigated by required
  `evidence`, and per-origin `rejected` counts so a bad model/profile is visible.
- **Queue rot** — with a manual drain, open rows pile up and stale annotations
  mislead agents. Mitigated by surfacing open counts in `query`, so the pain is
  visible; hard mitigation lands with Spec C.
- **Query returns too much** as the vault grows. Hard output cap now; revisit
  ranking around ~1000 notes.

---

## Slice 1 — read layer + host feedback (no auth, no scheduler)

### 1. Extend `frontmatter-radar/internal/scanner`

Currently parses `topics` only (92 lines). Add `maturity`, `type`, `status` to
`Frontmatter`, and return per-note records (path, topics, maturity, type, status)
instead of only a topic→paths map. Keep the existing `topics` command output
byte-identical — seeker/scribe docs depend on it.

Acceptance: `frontmatter-radar topics` output unchanged; new scanner API exposes
maturity for every `.md` under the vault root.

### 2. New commands: `query`, `read`

```
frontmatter-radar query "context rot"      # topic match + body grep
frontmatter-radar read 20_Knowledge/go.md --section "Current Understanding"
```

`query` output per hit: path, `[maturity]`, topics, ~15-line excerpt, wikilink.
Total output hard-capped (~2k tokens); truncation states how many hits were
dropped. Zero hits prints an explicit empty result — empty is a valid answer
(matches seeker rules).

`read` prints a whole note or one `##` section. No write commands in the binary.

Acceptance: query on a term with no notes exits 0 with explicit "no notes found";
query on `go` returns hits with correct maturity labels; output never exceeds cap.

### 3. Migration — `knowledge_feedback`

```sql
CREATE TABLE knowledge_feedback (
    id          BIGSERIAL PRIMARY KEY,
    kind        TEXT NOT NULL,      -- stale|wrong|thin|conflict|gap|confirm
    note_path   TEXT,               -- NULL only for kind='gap'
    topic       TEXT,
    evidence    TEXT NOT NULL,      -- what proves it (file, observed behavior)
    suggestion  TEXT,               -- agent's draft replacement
    origin      TEXT NOT NULL,      -- host session id, or cerberus session (Slice 2)
    status      TEXT NOT NULL DEFAULT 'open', -- open|accepted|rejected|duplicate|applied
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON knowledge_feedback(status);
CREATE INDEX ON knowledge_feedback(note_path);
```

`confirm` kind is deliberate: agent used a note and it was correct. Only positive
signal available; feeds promotion of `raw` → `curated`.

### 4. API — `POST/GET /api/knowledge-feedback`

New handler in `internal/httpapi`, registered in `internal/httpserver/core.go`
next to the existing `/api/feedback` route. Reject with 400 when `evidence` is
empty, when `kind` is unknown, or when `note_path` is empty for any kind other
than `gap`. `GET` supports `?status=open` and `?note_path=`. `PATCH` on
`/api/knowledge-feedback/{id}` sets `status` — used by the manual drain.

Acceptance: POST without evidence → 400; POST with unknown kind → 400; valid POST
→ 201 with row; GET `?status=open` returns only open rows; PATCH moves status.

### 5. CLI `feedback` subcommand (host path)

```
frontmatter-radar feedback --kind stale --note 20_Knowledge/go.md \
  --evidence "says pgx v4, foundry go.mod uses v5" \
  --suggestion "..."
```

POSTs to foundry (`--url`, default `http://localhost:8080`). Config file
`~/.config/notelaator/config.json` holds url and (later) token, so the CLI has one
code path for host and container.

Acceptance: missing `--evidence` fails locally before any HTTP call; unreachable
foundry prints the payload to stderr so feedback is not lost.

### 6. Feedback surfacing in `query`

`query` fetches open rows from `GET /api/knowledge-feedback?status=open` and
annotates hits:

```
20_Knowledge/go.md  [curated]  topics: go, backend
  ...excerpt...
  ! 2 open feedback: stale(pgx version), thin(error handling)
```

`--no-feedback` skips the call for offline use; a failed call degrades to a
warning, never an error. This is what stops agents refiling known issues and tells
them which sections to distrust.

Acceptance: with one open row on a note, that note's hit shows the annotation;
with foundry down, query still returns hits plus a warning line.

### 7. Vault read-only mount for workflow phases

Chat sessions already build `ExtraMounts` (`internal/chat/service.go:312`,
migration 016). Apply the same pattern to workflow phase sessions so phase agents
get the vault at a fixed read-only path, plus an `Instructions` line naming the
path and the `query` command. Vault is configured as a normal foundry project
(`repo_path`), flagged as the knowledge vault.

Acceptance: a phase session container can run `frontmatter-radar query` against
the mount; writes to the mount fail (read-only).

### 8. Agent image

Static-build the CLI and `COPY` it into `cerberus/docker/Dockerfile` (alongside
go/node/pi). One extra binary, no runtime deps.

Acceptance: `frontmatter-radar --version` runs inside a fresh agent container.

### 9. Vault guard

Pre-commit hook in the vault rejecting any staged diff touching a note with
`maturity: curated`, with an override env var for the owner. Enforcement, because
the `AGENTS.md` rule is prose.

Acceptance: staging an edit to a curated note fails the hook; the same edit with
the override set succeeds.

### 10. Janitor drain instructions (markdown only)

Extend vault `00_Meta/agents/janitor.md` with the manual drain procedure: list
open rows, group by `note_path` + `kind`, apply to `raw` notes, propose-only for
`curated`, `conflict` always to the owner, then `PATCH` each row's status. No code,
no scheduler — this is the human-triggered stand-in until Spec C.

Acceptance: a janitor pass on seeded feedback ends with zero rows left `open`.

---

## Follow-up specs

**Spec B — foundry auth layer.** Main API bound to loopback; separate listener on
the docker interface carrying agent routes; `session_tokens` (hashed, scoped,
revocable, use-capped) consumed in a single `UPDATE ... RETURNING`. Unlocks
Slice 2: squid-allowlist the agent listener, inject `FOUNDRY_URL`/`FOUNDRY_TOKEN`
into containers, set `knowledge_feedback.origin` from the token subject so a
session cannot forge attribution. Until B, sandboxed agents are read-only
consumers of the vault — already most of the value.

**Spec C — recurring jobs.** Generic foundry capability: job registry, interval or
timer triggers, no-overlap execution, last-run status, dispatch off the existing
1-minute ticker (`internal/httpserver/core.go:83`) rather than a new daemon. The
automated curator (dedupe → route by maturity → one review branch, cheap model,
no net) becomes its first consumer, replacing the manual janitor drain from
Slice 1 item 10.

## Build order

1–2 (CLI read layer) → 3–5 (feedback storage + host CLI) → 6 (surfacing) →
7–8 (mount + image) → 9–10 (guard + janitor drain) → Spec B → Slice 2 → Spec C.
