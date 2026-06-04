# Foundry — Block-by-Block Walkthrough

> Understanding what was made and why, one block at a time.
> Companion to `architecture-overview.md` and `direction.md`. Date: 2026-06-01.

Order:
1. Data model ✓
2. internal/workflow ← (this block)
3. Boot & config
4. spec
5. cerberus
6. memory
7. review
8. api
9. hub / discover

---

## Block 1 — The data model (11 migrations)

### The core chain

```
projects → specs → workflows → phases → phase_logs
```

Read it as a sentence: *a project has specs; running a spec creates a workflow;
a workflow runs phases; a phase emits logs.*

- **projects** — a target git repo. Just `name`, `repo_path`, and (added later)
  `memory_namespace`. This is the thing Foundry builds *into*.
- **specs** — a markdown document + `track` (poc/polish) + `status` + `tags`.
  This is the durable intent. One spec can be run many times (many workflows).
- **workflows** — ONE run of a spec on a track. Has `max_cost_usd` and a
  `finished_at`. This is the unit of "go do this now."
- **phases** — the heart. One step of a workflow. Carries everything about an
  agent run: `prompt_sent`, `status`, `retry_count`, `timeout_seconds`,
  `cerberus_session`, `cerberus_commit`, `cost_usd`, `review_verdict`,
  `review_notes`, `adjusted_prompt`, `files_touched`, `phase_feedback`.
- **phase_logs** — append-only log lines streamed from the agent.

### Why this shape — the choices

1. **spec vs workflow is a deliberate split.** The spec is the durable artifact
   (write once, edit, keep). A workflow is a disposable *execution* of it. This
   is why you can re-run a spec, and why a failed run doesn't destroy the spec.

2. **Phases are denormalized on purpose.** Everything about one agent run lives
   on one row — the prompt sent, the verdict, the commit, the files, the cost.
   You can reconstruct exactly what happened in a phase from a single row. This
   is an audit-trail choice, not a normalization choice.

3. **State machines are enforced in the DB via CHECK constraints**, not just app
   code:
   - spec.status: `dumpster → queued → running → done/failed/paused`
     (the "backlog" is literally `status='dumpster'`)
   - workflow.status: `running → done/failed/paused`
   - phase.status: `pending → running → awaiting_review → done/failed`
   - review_verdict: `pass | fail | null`
   The DB refuses an illegal state. Belt-and-suspenders against the app writing
   garbage.

### The side tables — what they reveal about intent

- **spec_drafts** (migration 002, expanded in 010) — an *iterative spec-builder
  chat*. Has `messages` (JSONB), `cerberus_session`, and later `original_intent`
  + `current_decision_needed`. Status flow grew from `active/saved/error` to
  `active/ready_to_freeze/frozen/abandoned/error`.
  → **This is the seed of the Forge.** Someone was already building "a
  conversation that produces a spec," with a notion of freezing.

- **draft_attempts / draft_attempt_events / draft_decisions** (010) — the big
  tell. `draft_decisions` has `prompt / options / decision / rationale / status
  (pending/answered/dismissed)`. **This is the decision-interview, already
  modeled in the schema.** The direction we agreed on isn't new invention — it's
  finishing something half-built.

- **profiles** (003) — per-run cerberus config: model, image, AWS profile/region,
  extra_env. Lets different specs run against different models/credentials.

- **memory_update_jobs** (008) — proposed memory writes awaiting human
  accept/reject (`pending/accepted/rejected`) + the proposal markdown. The
  human-in-the-loop gate for memory.

- **cerberus_events** (004) — raw JSONL events streamed from cerberus, keyed by
  session. The firehose behind the live UI.

- **app_settings** (011) — key/value runtime config. This is why config lives in
  the DB, not the yaml (see Block 2).

### Drift / archaeology worth noting

- **Migrations 005 → 006 → 007 are a visible mind-change about memory.**
  005 added `memory_repo_path` per project, then a comment deprecates it
  ("configured globally, not per project"), 006 adds `memory_namespace`, 007
  drops `memory_repo_path`. → The team decided: **one global memory repo, with a
  per-project namespace (subdirectory) inside it**, rather than a repo per
  project. Relevant to the "I don't trust memory yet" feeling — the memory model
  itself was already churning.

- **migration 009** bolts `phase_feedback` JSONB onto phases with a structured
  default `{result, useful_context, problems, suggested_memory, confidence}`.
  → An attempt at structured, machine-readable phase outcomes (feeding memory?),
  separate from the free-text `review_notes`.

### One-line summary
The schema says Foundry is **two systems sharing a database**: a spec *authoring*
side (drafts, attempts, decisions) and a spec *execution* side (workflows,
phases, logs) — joined at the `specs` table. The authoring side is less built-out
but already modeled. That join is exactly where the Forge thesis lives.

---

## Block 2 — internal/workflow (the engine)

Files: `runner.go` (652 lines), `runner_test.go` (58 lines).
This is the brain: it turns a spec row into a sequence of agent runs and lands
the results. Everything else is plumbing around it.

### Public surface (what the rest of the app calls)

- `NewRunner(pool, cerb, cfg, hub)` — construct.
- `Start(workflowID)` — fire a workflow (async, in a goroutine).
- `Stop(workflowID)` — cancel a running workflow.
- `SetCerberusProfile(p)` / `SetMemoryRepoPath(p)` — live reconfigure at runtime.

That's it. The whole engine is driven by `Start` + `Stop`.

### Concurrency model

- **One goroutine per workflow.** `Start` spawns `go r.run(ctx, id)` and stores
  the `context.CancelFunc` in `cancels map[int64]context.CancelFunc` under a
  mutex. `Stop` looks up the cancel and calls it. (runner.go:76-101)
- **Phases run strictly sequentially inside a workflow** — `run()` is a `for`
  loop that pulls `NextPendingPhase` one at a time. No phase parallelism.
- `MaxConcurrentWorkflows` is in the config but **the runner doesn't enforce it**
  — nothing here counts active goroutines. Whoever calls `Start` would have to
  gate that. (Worth confirming in the api block.)

### The lifecycle (three nested functions)

```
Start → run()                  per-workflow loop
          └─ runPhase()        builds the prompt for one phase
               └─ execPhase()  runs cerberus, judges, lands or retries
```

**run() (runner.go:103-181)** — the workflow loop:
1. Load workflow → spec → project.
2. `spec.Parse(content)` → phases. No phases → pause the workflow + spec. (So a
   spec with no `## Phase N:` headers is a dead end — relevant to the Forge:
   the authoring side must emit phase headers.)
3. Create phase rows if none exist yet (idempotent on restart).
4. Pick track overlay (poc vs polish — just a string).
5. Loop forever:
   - ctx cancelled → pause.
   - **budget check** (see "Dead feature" below).
   - `NextPendingPhase`; `ErrNotFound` → workflow is finished, compute final
     status from phases, write spec status, return.
   - else `runPhase`; on error → workflow + spec marked failed.

**runPhase() (runner.go:183-202)** — assembles the prompt, in layers:
1. `spec.BuildPrompt(globalCtx, goal, overlay)` — OR `phase.AdjustedPrompt` if a
   retry set one.
2. `memory.LoadApproved(...)` prepended (tags extracted from the goal text).
3. `prependRepoRootContext(repoPath, prompt)` — a header telling the agent the
   working dir is the repo root (guards against phantom nested dirs).
Then calls `execPhase`.

**execPhase() (runner.go:204-401)** — the meat. In order:
1. Build session name `foundry-w<wf>-p<phase>`; set repo path on the cerb client.
2. **Pre-clean** the session. If clean fails, fall back to *manually* removing
   the git worktree + branch (`git worktree remove --force`, `branch -D`). This
   is crash-recovery: a previous run may have died leaving lingering git state.
   (runner.go:216-224) — note this hardcodes cerberus's internal path
   `.cerberus/sessions/<session>/worktrees/solve`, coupling Foundry to cerberus
   internals.
3. Mark phase `running`, publish event.
4. Write a per-session profile file from the DB `profiles` row, point cerb at it.
5. Launch `cerb.Start(...)` in a goroutine; block on its result channel. If no
   callback URL, poll logs on a 2s ticker (legacy path).
6. cerberus errored → phase `failed`. ctx cancelled → phase `failed`.
7. Mark `awaiting_review`.
8. **Verdict** (see below).
9. On pass with a commit hash → `git cherry-pick <hash>` into the real repo.
   On cherry-pick failure → `cherry-pick --abort` + phase `failed`.
   On success → phase `done`.
10. On fail → retry once, else `failed`.

### The verdict — THIS is the trust problem (Gap #1)

```go
// runner.go:320-325
verdict := "pass"
if strings.TrimSpace(diff) == "" {
    verdict = "fail"   // "cerberus exited 0 but produced no diff"
}
```

**The verdict is: "did the agent produce a non-empty diff?"** That's it.
- `internal/review` (the real LLM reviewer) is **not imported here** and never
  called.
- `cerb.Review(...)` IS called, but only to scrape the list of files touched —
  not to judge quality.
- So an agent that confidently writes *wrong* code still produces a diff → pass →
  cherry-picked into your repo.

**This is the direct mechanical cause of "runs, but output isn't trustworthy."**
The engine has no concept of "is this correct" — only "is this non-empty."

### Retry — exactly once, and dumb

If the verdict is fail and it's not already a retry (`isRetry || RetryCount >= 1`),
it re-runs the phase with a hardcoded suffix:
`"[Previous attempt produced no changes. Try again.]"` (runner.go:388)
- The retry prompt assumes the failure was "no changes." If `review` were wired,
  `review.Result.AdjustedPrompt` would carry a *reasoned* retry — but it isn't,
  so the retry is uninformed.
- After one retry, fail is terminal for the phase, which fails the workflow.

### phase_feedback — structured, but synthetic

`buildPhaseFeedback` (runner.go:599-633) emits the `{result, useful_context,
problems, suggested_memory, confidence}` JSON that migration 009 added. But:
- `confidence` is a **hardcoded constant**: 0.85 for pass, 0.4 for fail, 0.6
  otherwise. It is not a real signal — it's keyed purely off the diff-empty
  heuristic.
- `useful_context` is just the touched files + commit hash + notes, restated.
- This is the *only* part of the package with test coverage
  (`runner_test.go` tests `buildPhaseFeedback` and nothing else).

### Dead feature — the budget

`run()` checks `WorkflowTotalCost(workflowID) >= MaxCostUSD` before each phase and
pauses if exceeded (runner.go:147-154). But:
- `WorkflowTotalCost` = `SELECT COALESCE(SUM(cost_usd),0) FROM phases ...`
- **Nothing ever writes `phases.cost_usd`.** Confirmed: no caller anywhere sets
  `CostUSD` in `UpdatePhaseParams`.
- Therefore `WorkflowTotalCost` is always 0, the check never trips, and the
  budget (config `default_workflow_budget_usd`, the per-workflow `max_cost_usd`,
  the UI for it) is **non-functional theater.** The wiring to capture per-phase
  cost from cerberus was never built.

### Test coverage

Near zero for the engine. `runner_test.go` tests only `buildPhaseFeedback` (a
formatting helper). The run loop, retry, cherry-pick, verdict, crash-recovery,
and budget paths are **untested**. For "the brain," that's the riskiest gap in
the repo — there's nothing pinning the cherry-pick/retry behavior against
regression.

### Design choices worth respecting

- **Orchestrate, don't touch.** The runner never edits files in the target repo;
  cerberus works in an isolated worktree/branch and the runner only
  `cherry-pick`s the approved commit. Clean blast-radius control.
- **Idempotent-ish restarts.** Phase rows are only created if absent; pre-clean
  handles leftover git state. The engine is built to survive a crash mid-run.
- **Live reconfiguration** via mutex-guarded setters — settings can change from
  the API without restarting the runner.
- **Events over polling.** When a callback URL is set, cerberus streams JSONL and
  the log ticker is disabled; polling is a legacy fallback.

### Smells / things to question (not fixing — just naming)

1. **Verdict heuristic is the trust bug** (Gap #1). Top of the list.
2. **Budget is dead** — decide: wire cost capture, or delete the feature + UI so
   it stops implying a guarantee it doesn't provide.
3. **`confidence` is fake** — same problem, smaller: it looks like signal, isn't.
4. **No engine tests** — the highest-value place to add them, but per your
   testing rule, via the public `Start`/`Stop` surface, not internals.
5. **"review" is overloaded** three ways: `cerb.Review` (files), `review.Reviewer`
   (unused LLM), `review_verdict` (heuristic). Naming debt that hides Gap #1.
6. **`extractFilesJSON` hand-builds JSON** with string concatenation — a file
   path containing a quote would corrupt it. Minor, but it's there.
7. **Cerberus-internals coupling** — the crash-recovery hardcodes
   `.cerberus/sessions/<session>/worktrees/solve`. If cerberus changes its
   layout, this breaks silently.

### One-line summary
The engine is well-structured for *orchestration* (isolation, cherry-pick,
crash-recovery, live config) but **has no real notion of quality**: the verdict is
"is the diff non-empty," the budget is dead wiring, and confidence is a constant.
The plumbing is sound; the judgment is missing. That gap is exactly your "output
isn't trustworthy."

---

## Block 3 — Boot & config (`cmd/server/main.go` + `internal/config`)

Files: `main.go` (129 lines), `config.go` (170 lines), `config_test.go`.

### What it does
`main.go` is the whole boot sequence, top to bottom:
1. Load `config.yaml` (path from argv, default `config.yaml`).
2. Run migrations (`golang-migrate`, file source, up only).
3. Open a pgx pool, ping.
4. `seedAndLoadRuntimeSettings` — seed `app_settings` from yaml defaults *if
   missing*, then read them back and overlay onto the config.
5. Construct the cerberus client, the event hub, the workflow runner.
6. Kick off `RecoverOrphanDrafts` in a goroutine (crash recovery for the spec
   builder).
7. Build the `http.ServeMux`: `/api/` + `/` → the api Server; `/style.css` +
   `/app.js` → static files with `no-store`. Listen.

### The one real idea here: config lives in the DB
- `config.yaml` is a **bootstrap seed, not the source of truth.** On first boot,
  `SeedAppSettingIfMissing` copies yaml values into the `app_settings` table.
  After that, **the DB row wins** and the UI can edit settings live.
- `RuntimeSettingKeys()` defines which keys are runtime-editable (cerberus_*,
  ui_verbosity, budgets, timeouts, review_*, git_root, memory_repo_path).
- **`db_url` and `server_port` are NOT runtime keys** — they're read from the
  yaml only, because you can't change your DB connection from a UI backed by that
  DB. Sensible split.
- `ApplyRuntimeSettings` parses the string values back into typed fields;
  `setDefaults` fills blanks; `expandHome` turns `~/` into an absolute path.

### Choices worth respecting
- **Migrations run automatically on boot.** No separate migrate step; the binary
  self-migrates. Good for a single-user tool.
- **Settings are hot-editable** without restart, because the runner reads them
  via mutex-guarded getters and the api pushes updates in.

### Smells / things to question
- `review_base_url` / `review_api_key` / `review_model` are seeded, surfaced in
  settings, and parsed — but feed the **orphaned reviewer** (Block 7). Live
  config for a dead feature.
- `migrate.New("file:///"+migrationsPath, ...)` resolves migrations relative to
  the *current working directory*. Run the binary from the wrong dir and
  migrations silently aren't found where you expect. Tied to launch location.

---

## Block 4 — `internal/spec` (intent → phases)

Files: `spec.go` (~107 lines), `spec_test.go`.

### What it does
Two pure functions, no state:
- **`Parse(content)`** — splits a markdown spec on `## Phase N: <name>` headers
  (regex). Everything before the first header is `GlobalContext`; each header
  starts a `Phase{Position, Name, Goal}`. Position is parsed from the header
  number.
- **`BuildPrompt(globalContext, goal, trackOverlay)`** — composes the prompt sent
  to the agent: global context → `DefaultIntentReferences` → the phase goal →
  the track overlay.

### Why it matters
This is the **contract between the authoring side and the execution side.** A
spec is "runnable" iff it contains `## Phase N:` headers — otherwise `run()`
pauses it as a dead end (Block 2). So whatever the Forge produces, it MUST emit
that structure. The parser is the spec format's only enforcement.

### Choices
- **Markdown-as-format** — the spec is human-writable/editable plain text, parsed
  by one regex. No schema, no frontmatter required. Cheap and Obsidian-friendly.
- **Track is just an appended sentence** — `OverlayPoC` ("Make it work…") vs
  `OverlayPolish` ("Write this properly…"). The entire poc/polish distinction is
  these two strings. No structural difference downstream.

### Smells / things to question
- **`DefaultIntentReferences` points at files that don't exist** (Gap #2):
  `intent/Agent Workflow.md`, `intent/Principles.md`, etc. Every phase prompt
  tells the agent to read them; they're not in the repo. Either create them or
  stop referencing them.
- The parser is **position-from-header-number**, but phases are later ordered by
  the DB `position` column. Duplicate/sparse/non-sequential phase numbers in a
  spec would behave in ways nothing validates.

---

## Block 5 — `internal/cerberus` (the execution adapter)

Files: `cerberus.go` (214 lines), `cerberus_test.go`.

### What it does
A thin wrapper that shells out to the `cerberus` CLI. Every method builds an
`exec.Command` with `cmd.Dir = repoPath` and runs it. The full contract:
- **Execution:** `Start` (run a phase, blocking), `Status`, `Logs`, `Diff`
  (`review --diff`), `Review` (files/commit), `Clean`.
- **Interactive (spec builder):** `Chat` (first turn, injects the spec-writer
  system prompt), `Message` (follow-up), `Generate` (one-shot prompt→stdout),
  `Close`.
- **Naming:** `SessionName(wf,phase)` → `foundry-w<wf>-p<phase>`;
  `DraftSessionName(id)` → `foundry-draft-<id>`.
- **Streaming:** when a callback URL is passed, adds `--callback <url> --output
  jsonl` so cerberus POSTs incremental events instead of being polled.

### Choices worth respecting
- **Loose coupling via a CLI boundary.** Foundry treats cerberus as an external
  process with a stable command contract. The agent engine is swappable in
  principle — Foundry never imports cerberus code.
- **Prompt passed via temp file** (`Start`) to avoid arg-length/escaping limits.
- The **spec-writer system prompt** lives here (`specBuilderSystemPrompt`) —
  read-only, `/workspace`-rooted, "don't write files, produce the spec." This is
  the persona behind the authoring chat.

### Smells / things to question
- **The client is stateful and shared.** `repoPath` and `profile` are mutable
  fields on one `*Client` instance shared by the whole app. The runner calls
  `SetRepoPath`/`SetProfile` right before `Start`. **If two workflows ever ran
  concurrently they'd clobber each other's repoPath** — safe today only because
  `MaxConcurrentWorkflows` defaults to 1 and isn't enforced anyway (Block 2).
  A latent concurrency bug waiting for the day you raise concurrency.
- **The CLI contract is implicit** — flag names (`--name`, `--prompt-file`,
  `--callback`) are hardcoded strings. A cerberus version bump that renames a
  flag breaks Foundry with a runtime error, not a compile error.

---

## Block 6 — `internal/memory` (git-repo knowledge store)

Files: `memory.go` (~265 lines), `memory_test.go`.

### What it does
- **`LoadApproved(repoPath, namespace, tags)`** — walks `repoPath/namespace/`,
  reads every non-hidden `.md`, parses YAML frontmatter (`title`, `tags`,
  `always`), and includes a file if `always: true`, or no tags were requested,
  or any requested tag matches a frontmatter tag. Returns assembled markdown.
- **`Prepend(markdown, prompt)`** — glues approved memory on top of a phase
  prompt (idempotent via a prefix check).
- **`WriteApprovedUpdate(...)`** — writes a proposal to
  `namespace/workflow-updates/workflow-<id>.md` and **git-commits it** to the
  memory repo.

### Choices worth respecting (covered in the earlier memory discussion)
- Git repo as store: human-readable, Obsidian-friendly, version-controlled,
  approval = commit, outlives the DB.
- **Security-hardened path handling**: namespace cleaning, path-traversal
  rejection (`..`, absolute), and symlink rejection at every path segment. This
  is the most defensively-written package in the repo.

### Smells / things to question
- **Tag matching is naive.** The runner builds tags by tokenizing the *entire
  phase goal into individual words* (`extractTags`), then OR-matches against
  frontmatter tags. A goal mentioning "database" pulls every memory tagged
  "database"; a memory file with no frontmatter tags only loads via `always:
  true` or empty-tag fallback. Relevance is keyword-coincidence, not meaning —
  part of why you "don't trust memory yet."
- **Writes commit directly to the repo** (no branch/PR). Fine for a private
  single-user repo; surprising if that repo is ever shared.

---

## Block 7 — `internal/review` (the orphan)

Files: `review.go` (147 lines).

### What it is
A **complete, well-formed LLM reviewer that nothing uses.** Calls an
OpenAI-compatible `/chat/completions` endpoint, sends the phase goal + diff (+
test output on polish), and parses a strict JSON verdict:
`{verdict, notes, decisions, rationale, adjusted_prompt, decision_summary,
files_touched}`. It even has **different review bars for poc vs polish** (poc:
"plausibly accomplishes the goal"; polish: "tests cover the public surface, no
tests against unexported symbols" — which matches your testing rule).

### The finding (Gap #1, confirmed)
- **No code constructs it.** `grep` for `review.New` / `review.Config{}` → zero
  hits. The package is imported by nobody.
- The runner instead uses the **non-empty-diff heuristic** (Block 2). The
  reviewer's `adjusted_prompt` field — designed to give a *reasoned* retry — is
  exactly what the runner's dumb hardcoded retry string should have been.
- This package is the clearest evidence of "vibed then drifted": someone built
  the right thing, then wired the engine to a shortcut and never connected it.

### Why it matters for direction
This is the missing half of the trust problem. The Forge fixes spec *input*;
this package (if wired) is what would judge *output*. Both are needed for "fully
unattended." Right now neither is active.

---

## Block 8 — `internal/api` (the 4,186-line surface)

Files: `handlers.go` (3,759), `cerberus_events.go` (232), `handlers_test.go`
(195). One package doing **eight jobs**. This is the monolith.

### The eight concerns living here
1. **HTTP server + routing** — `ServeHTTP`, `routes()` registers ~20 endpoints on
   an internal mux.
2. **Server-rendered UI** — `handleUI*` functions render HTML via
   `html/template`; `renderShell` + "fragment" handlers are an HTMX-style
   partial-rendering setup. The `web/` dir (index.html 22 lines, app.js 529,
   style.css 536) is the thin client.
3. **REST-ish JSON API** — projects, specs, workflows, phases, profiles,
   settings, export.
4. **The spec-builder / draft authoring flow** — `handleSpecDraft*`. This is the
   *real, wired* authoring path (see below).
5. **Cerberus callback ingestion** — `handleCerberusCallback` →
   `handleCompactCerberusEvent` (in `cerberus_events.go`): receives the JSONL
   firehose, filters to text_delta / message_end / tool_use, buffers text
   (3KB flush), and either writes phase logs (for workflow sessions) or stores
   cerberus_events (for draft sessions).
6. **SSE streaming** — `streamLogs`, `streamWorkflow`, `streamDraftEvents` push
   live updates to the browser via the hub.
7. **Memory-update proposal flow** — `handleWorkflowMemoryUpdate` +
   `generateMemoryProposalMarkdown`, which **calls `cerb.Generate`** (the agent,
   not the orphaned reviewer) to draft a memory proposal, with accept / reject /
   revise actions that ultimately call `memory.WriteApprovedUpdate`.
8. **Runtime settings + profiles + orphan-draft recovery** — `handleSettings`
   (writes back to BOTH the yaml file and the DB via `mergeYAMLRuntimeSettings`),
   profile CRUD, `RecoverOrphanDrafts`.

### The wired authoring flow (important for the Forge)
- A draft is a **chat with the spec-writer agent**: `cerb.Chat` →
  `cerb.Message`. The agent calls the **`update_spec` tool** (the
  `.pi/extensions/update-spec.ts` extension), which writes `.foundry-spec.md` and
  emits a `tool_use` event. `cerberus_events.go` watches for
  `tool_name == "update_spec"`.
- When done, `extractFinalSpec` / `isSaveReadySpec` pull the finished spec out of
  the chat, and `writeSpecMarkdownToMemory` saves it.
- **This is free-form conversational authoring** — NOT a decision interview.

### The follow-up / feedback loop
`handleWorkflowFollowUp` + `buildFollowUpSpecContent` take a failed workflow's
phases + recent logs and synthesize a *new* spec with the failure context
injected before the phases. A primitive "learn from the failed run" loop — and
the one place failure context is reused.

### Choices worth respecting
- **stdlib only** — `net/http` + `html/template` + server-rendered fragments +
  SSE. No framework, no build step for the client. Matches the "no framework"
  stack decision.
- **Events filtered aggressively** — the callback handler drops noisy event types
  and buffers text to avoid hammering the DB with every token.

### Smells / things to question
- **One 3,759-line file doing eight things.** This is where "I can't find
  anything" will keep happening. The authoring flow (your actual product) is
  buried in here instead of being its own package.
- **`writeProfileFile` / `profileFilePath` are duplicated** — one copy here, one
  in `internal/workflow`. Two implementations of the same thing.
- **The decision-interview is schema-only.** `draft_decisions` /
  `draft_attempts` have full db query functions (`CreateDraftDecision`,
  `UpdateDraftDecision`, …) but **no handler calls them.** Built bottom-up
  (schema + data access) then abandoned before any API/UI wiring. The Forge
  direction = finishing this connection — the bottom two layers already exist.
- Tests cover **helpers** (spec extraction, follow-up content, memory-proposal
  formatting, settings key separation) but **not the handlers themselves**.

---

## Block 9 — `internal/hub` & `internal/discover` (utilities)

### `hub` (44 lines) — in-memory pub/sub
- `Subscribe(key)` returns a buffered channel (cap 64); `Publish(key, data)`
  fan-outs to all subscribers; `Unsubscribe` removes + closes.
- **Non-blocking publish**: `select { case ch <- data: default: }` — if a
  subscriber's buffer is full, **the event is dropped**, not blocked on.
- **Choice:** dead-simple, single-process. Perfect for one user on one instance.
- **Smell / limit:** drops events under backpressure (a slow browser tab loses
  log lines), and being in-process means it **won't work across multiple server
  instances** — a hard ceiling if "grow it bigger" ever means horizontal scaling.

### `discover` (66 lines) — repo scanner
- `FindRepos(root)` scans 2 levels deep (`root/group/repo/.git`, plus
  `root/repo/.git`) and returns `{name, path}`.
- **Choice:** assumes your `~/git/<group>/<repo>` layout. Trivial, does one thing.
- **Smell:** the 2-level depth is hardcoded; repos nested differently won't be
  found.

---

## Addendum — `internal/db` (the data-access layer)

Files: `queries.go` (~1,440 lines, **68 exported functions**), `errors.go`,
`queries_test.go`.

Not in the original block list because it's the *implementation* of Block 1's
schema, but worth naming:
- **Hand-written pgx, no ORM.** Every table has explicit
  Create/Get/List/Update functions. `Update*Params` structs use pointer fields so
  only non-nil fields get written (partial updates) — that's the pattern behind
  all the `db.UpdatePhase(ctx, ..., UpdatePhaseParams{Status: &s})` calls.
- `errors.go` defines `ErrNotFound`, the sentinel the runner switches on.
- **It already contains the data access for the unfinished features** —
  `draft_decisions`, `draft_attempts` query functions exist here with no callers.
  The DB layer ran ahead of the handlers.

### One-line summary (whole system)
Foundry is a **sound orchestration skeleton with two unfinished organs**: the
*judgment* organ (the `review` reviewer — built, unwired) and the *authoring*
organ (the decision-interview — schema + db built, unwired; free-form chat
authoring wired as a stopgap). The Forge direction is to grow the authoring
organ; the trust problem also needs the judgment organ. Everything else — the
engine, the data model, cerberus integration, memory, streaming — is real and
working.
