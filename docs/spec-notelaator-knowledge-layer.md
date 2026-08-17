# Spec A — Notelaator knowledge layer for host orchestrator

## Goal

Let host orchestrator read notelaator vault through one bounded CLI and file structured grievances about what it reads, without writing to vault directly. Orchestrator selects small relevant excerpts for subagents; subagents never receive vault access.

## Non-goals

- No subagent vault mount, vault CLI, or vault binary in cerberus image.
- No web research / net access for agents. Sandbox egress stays default-deny.
- No embeddings, vector DB, local librarian model, or MCP server. ~90 notes: frontmatter topics + grep enough.
- No new binary: extend `frontmatter-radar`.
- No Foundry auth / sandbox-to-Foundry API route. **Spec B scrapped.**
- No scheduler or automated curator yet. **Spec C** remains future work.
- No changes to existing session-quality `/feedback` prompt or `feedback` table. Knowledge grievances use separate table.

## Design

```text
host orchestrator
  └─ frontmatter-radar query/read against local vault
  └─ selects minimal relevant excerpts
  └─ gives excerpts in subagent task prompt
  └─ validates any subagent-proposed grievance
  └─ POST /api/knowledge-feedback

cerberus subagent
  └─ task repo + prompt-selected context only
  └─ no /vault mount, no frontmatter-radar binary, no Foundry API access
```

1. **Host retrieval is opt-in and bounded.** Vault content is never injected automatically. Host uses a specific query, reads at most relevant notes/sections, and treats text as fallible context.
2. **Subagents stay light.** They receive code and task-specific context selected by host, avoiding broad/noisy vault retrieval filling their context window.
3. **Agents never write vault.** Host janitor is sole serialized writer path; `raw` notes may be changed, `curated` notes proposal-only.
4. **Grievances live in postgres.** `knowledge_feedback` handles concurrent host submissions and has lifecycle separate from session-quality `feedback`.
5. **Evidence mandatory.** API rejects empty evidence, unknown kind, and missing note path except `gap`.
6. **Manual drain in v1.** Owner runs janitor against open rows. Spec C later adds generic recurring jobs and curator.

## Slice 1 — host read layer + feedback

### 1. Scanner

Extend `frontmatter-radar/internal/scanner` frontmatter with `maturity`, `type`, `status`; expose per-note records while keeping `topics` output byte-identical.

Acceptance: every markdown note exposes maturity; `topics` unchanged.

### 2. Host CLI

```sh
cd /home/tonis/git/personal/notelaator
frontmatter-radar query "context rot"
frontmatter-radar read 20_Knowledge/go.md --section "Current Understanding"
```

`query`: topic + body match, maturity label, topics, excerpt, wikilink, hard ~2k-token output cap, explicit zero-hit answer. `read`: whole note or one `##` section. No write command.

Acceptance: zero hits exits 0 explicitly; bounded output; host reads only selected relevant notes before passing excerpts to subagent.

### 3. Knowledge feedback storage/API

`knowledge_feedback` fields: id, kind (`stale|wrong|thin|conflict|gap|confirm`), optional note_path only for `gap`, topic, required evidence, suggestion, required origin, status, created_at. Index status and note_path.

Endpoints:

```text
POST /api/knowledge-feedback
GET  /api/knowledge-feedback?status=open&note_path=...
PATCH /api/knowledge-feedback/{id}
```

Acceptance: invalid POST → 400; valid POST → 201; filters and status updates work.

### 4. Host feedback CLI and query annotations

```sh
frontmatter-radar feedback --kind stale --note 20_Knowledge/go.md \
  --evidence "says pgx v4, foundry go.mod uses v5"
```

Host CLI posts to Foundry (`--url`, default localhost); missing evidence fails locally; failed delivery prints payload to stderr and exits non-zero. `query` fetches open feedback and annotates matching hits; `--no-feedback` disables this and offline fetch failure only warns.

Acceptance: host can submit and later see feedback on matching query result.

### 5. Manual janitor drain

`00_Meta/agents/janitor.md` procedure: list open rows, group/dedupe by note_path + kind, update `raw` notes, propose changes for `curated`, escalate `conflict`, treat `gap` as candidate note and `confirm` as promotion signal, then mark rows applied/rejected/duplicate.

Acceptance: janitor pass leaves no processed row open.

## Future

**Spec B — scrapped.** Do not expose Foundry API to sandboxed agents or add credentials, squid allowlisting, session tokens, or agent attribution transport.

**Spec C — recurring jobs.** Generic Foundry job registry, interval/timer triggers, no-overlap execution, last-run status, existing one-minute ticker. First consumer: curator that dedupes host feedback, routes by maturity, creates review branch, and replaces manual drain.
