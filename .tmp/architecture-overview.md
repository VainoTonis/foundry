# Foundry — Architecture Overview

> Reconstructed by reading the code end to end: entry point, workflow engine,
> cerberus client, spec parser, memory, review, config, all 11 migrations, and
> the API surface. Date: 2026-06-01.

---

## What Foundry is

A **spec-driven, unattended dev loop**. You write a markdown spec, it gets parsed
into phases, and each phase is handed to [cerberus](https://github.com/VainoTonis/cerberus)
(an external agent-execution CLI) running in a container against a target git
repo. Output gets reviewed, the commit gets cherry-picked into the real repo, and
the next phase runs.

Single Go binary + Postgres + vanilla-JS UI.

---

## The loop

```
spec (markdown)
   │  spec.Parse → GlobalContext + []Phase   (split on "## Phase N: ...")
   ▼
workflow (one run of a spec on a track: poc | polish)
   │  for each phase, sequentially:
   ▼
phase
   │  spec.BuildPrompt(globalCtx, goal, trackOverlay)
   │    + memory.LoadApproved(...)  prepended
   │    + repo-root header          prepended
   ▼
cerberus.Start(session, prompt, callbackURL)   ← runs in goroutine, blocking
   │  cerberus works in its own git worktree/branch: cerberus/<session>
   │  streams JSONL events back to /api/cerberus/events
   ▼
verdict
   │  diff non-empty → "pass";  empty → "fail"     ← heuristic, see gap #1
   ▼
pass → git cherry-pick <commit> into target repo → phase done → next phase
fail → retry once with adjusted prompt → fail again → phase failed → workflow failed
```

---

## Components (the spine)

| Package | Role | Size / health |
|---|---|---|
| `cmd/server/main.go` | Boot: load config → migrate → pool → seed runtime settings → start runner → serve | Clean, 129 lines |
| `internal/workflow` | **The engine.** Phase loop, retry, cherry-pick, budget check, event publishing | 708 lines — the real brain |
| `internal/cerberus` | Thin `exec` wrapper around the `cerberus` CLI (start/status/logs/diff/review/chat/message/close) | Clean adapter |
| `internal/spec` | Markdown → phases parser + prompt builder + track overlays | Small, solid |
| `internal/api` | HTTP handlers, server-rendered UI, SSE-ish callback ingestion | **4,186 lines — the dumping ground** |
| `internal/memory` | Loads approved markdown from a separate git "memory repo" by namespace+tags; writes back workflow-update proposals | Careful (symlink / path-traversal guards) |
| `internal/db` | pgx queries, no ORM | 1,441 lines |
| `internal/review` | **Full LLM reviewer (OpenAI-compatible). Built but wired to nothing.** | See gap #1 |
| `internal/discover` | Scans `git_root` 2 levels deep for repos | Trivial |
| `internal/hub` | In-memory pub/sub for real-time UI streaming | 44 lines |

---

## Data model (Postgres, 11 migrations)

Core chain:

```
projects → specs → workflows → phases → phase_logs
```

Side tables:

- `spec_drafts` (+ `draft_attempts`, `draft_attempt_events`, `draft_decisions`)
  — an iterative spec-builder chat flow.
- `profiles` — per-run cerberus model / image / AWS env.
- `memory_update_jobs` — proposed memory writes awaiting accept/reject.
- `cerberus_events` — raw JSONL event log.
- `app_settings` — runtime-editable config, key/value.

**Spec lifecycle:** `dumpster → queued → running → done/failed/paused`.
The "backlog" is literally `status='dumpster'`.

---

## Key architectural choices

1. **Cerberus does the work in isolation; Foundry only orchestrates.**
   Each phase = a cerberus session in its own worktree/branch. Foundry never
   edits the repo directly except `git cherry-pick` to land approved commits.
   Clean separation.

2. **Config lives in the DB, not the file.**
   `config.yaml` only seeds `app_settings` on first boot
   (`SeedAppSettingIfMissing`); after that the DB row wins and the UI can edit it
   live. The yaml is a bootstrap, not the source of truth.

3. **Sequential phases, single-flight per workflow.**
   `cancels map[int64]context.CancelFunc` tracks running workflows;
   `MaxConcurrentWorkflows` exists but the runner loops phases one at a time.

4. **Two tracks are just a prompt overlay** — `OverlayPoC` vs `OverlayPolish`
   strings appended to the prompt. No structural difference in the engine.

5. **Memory is a separate git repo, namespaced per project, approved-only.**
   Agent proposes → human accepts → it gets committed to the memory repo.
   Path-traversal / symlink hardened.

---

## ⚠️ Where the vibing drifted (reconcile these first)

### Gap #1 — The README's headline feature isn't wired in
README says *"an LLM reviews the diff vs goal."* The real `internal/review`
package — a complete OpenAI-compatible reviewer with a structured verdict schema
— **is imported by nobody.** The runner instead uses a one-line heuristic:

- `internal/workflow/runner.go:322` — non-empty diff = `pass`, empty diff = `fail`.

The `review_base_url` / `review_api_key` / `review_model` config keys feed
nothing. So either the reviewer was ripped out mid-refactor or never connected.
**This is the single biggest "what is this thing" question to resolve** — it
defines whether the verdict is "heuristic" or "LLM review," which defines what
Foundry fundamentally claims to be.

### Gap #2 — Docs referenced everywhere, present nowhere
README links `docs/install.md`, `docs/self-hosting.md`, `docs/cerberus.md`,
`intent/README.md`, `SPEC.md`, and the prompt builder hard-codes reading
`intent/Agent Workflow.md`, `intent/Principles.md`, etc.

**None of these files exist in the repo.** Every phase prompt currently tells
cerberus to read files that aren't there (see `spec.DefaultIntentReferences` in
`internal/spec/spec.go:93`).

### Gap #3 — Config leak
`config.yaml:memory_repo_path` points at `/home/headtrollhunter/...` (not your
user). Stale paste.

### Gap #4 — `internal/api` is a 4,186-line monolith
Doing routing, HTML templating, SSE, cerberus callback buffering, and the entire
spec-draft chat state machine. This is where the next "I can't find anything"
moment will come from.

---

## Suggested order for pumping the brakes

1. **Decide Gap #1** (heuristic vs real LLM review). Everything else hangs off
   what Foundry claims to be.
2. **Resolve Gap #2** — either create the `intent/` + `docs/` + `SPEC.md` files,
   or stop the prompt builder from referencing them.
3. Fix the config leak (Gap #3) — trivial.
4. Plan a carve-up of `internal/api` (Gap #4) — defer until #1/#2 settle the
   shape of the thing.
