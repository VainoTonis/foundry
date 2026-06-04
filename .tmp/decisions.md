# Foundry — Decision Log

> Why things are the way they are. Started 2026-06-04 after a full read-through.
> Status legend: DECIDED · DIRECTION (agreed, not yet detailed) · OPEN · RISKY (needs real analysis before committing).

---

## D1 — The reviewer runs through cerberus, not a separate harness · DECIDED
The `internal/review` package maintains its own OpenAI-compatible HTTP client. We
will not maintain a second LLM harness. Reviewing is an LLM workflow like any
other, so it should go through cerberus.
- **Why:** one harness to maintain; cerberus already does LLM calls.
- **Cheaper than expected:** `cerb.Generate` already exists and is already used
  for memory proposals (`handlers.go:2277`). Review collapses to: build prompt →
  `cerb.Generate` → parse JSON verdict.
- **Ripple:** may need a cerberus change to support a clean "judge this diff,
  return JSON, don't touch the repo" mode. The `review.Result` schema stays.

## D2 — Smart retry comes from the reviewer · DIRECTION
Retry should be *reasoned* (use the failure to adjust the next prompt), not the
current hardcoded "produced no changes, try again."
- **Why:** quality. Blind retry wastes a run.
- **Note:** this is the SAME work as D1 — `review.Result.AdjustedPrompt` is
  exactly the reasoned-retry prompt. Wiring the reviewer delivers smart retry.

## D3 — Cerberus client is instance-specific, not repo-specific · DIRECTION
The cerberus client currently holds a mutable `repoPath`/`profile` that the
runner mutates before each phase. Move to a model where repo/target is passed
per-call (or per-session), not stored on a shared client.
- **Why:** kills the latent concurrency bug (concurrent workflows clobber each
  other's repoPath) and removes repo-specific state juggling.

## D4 — Specs are living documents that evolve over time · DIRECTION (vision)
Specs should flow and update with time, not be write-once artifacts.
- **Why:** stated product vision. A spec is a durable, evolving statement of
  intent, not a frozen ticket.
- **Open:** versioning? edit history? how does an evolving spec interact with
  already-run workflows?

## D5 — Specs live in the database, not Obsidian/markdown files · DIRECTION
Stop persisting specs as Obsidian-friendly markdown in a git repo. Keep them in
Postgres (where `specs.content` already lives).
- **Why:** "Realistically I'm not going to look at them in Obsidian."
- **Ripple (must decide):** this weakens the founding rationale for the git
  **memory repo** (the whole "Obsidian-friendly, human-edited" argument). If
  specs don't need Obsidian, does memory? Revisit alongside the parked
  memory-trust question. See [[memory]] decision, TBD.

## D6 — "Query via cerberus, write directly" · RISKY / OPEN — DO NOT LOCK IN
Idea: reads/understanding go through cerberus (in container), but file writes are
done directly by Foundry, because at write-time direct edits may be more
economical.
- **Tension:** today ALL writes happen in cerberus's isolated worktree and land
  via `git cherry-pick`. That isolation is the safety model — a bad run can't
  corrupt the real repo. Direct writes trade blast-radius protection for cost.
- **Verdict:** needs a real cost-vs-safety analysis before committing. Flagged,
  not decided.

## D7 — Intent docs (Gap #2) were removed on purpose · CONTEXT + OPEN
The `intent/*.md` and `docs/*` files referenced by the README and the phase
prompt builder were intentionally ripped out — there were multiple copies of the
same docs, no structure, just confusing.
- **Still open:** the prompt builder (`spec.DefaultIntentReferences`) and README
  still REFERENCE them. Decide: (a) rebuild intent docs with ONE clear structure,
  or (b) drop the references so prompts stop pointing at nothing.

## D8 — `internal/api` needs a rewrite · DIRECTION
The 3,759-line God-file doing eight jobs is the worst-maintained part of the
codebase and where the real product (spec authoring) is buried. It is the one
package where a rewrite (not just refactor) is justified.
- **Why:** unmaintainable; mixes routing, UI, authoring, streaming, settings.

## META — Rebuild-from-zero vs. evolve in place · DECIDED: EVOLVE IN PLACE
Standing temptation was to strip everything and start from 0. Self-identified as
a recurring pattern ("I cannot keep rebuilding things every time I get to this").
- **Decided:** evolve in place. Keep the working engine, data model, cerberus,
  memory, and streaming. Rewrite only `internal/api` (D8) and finish the two
  organs (judgment, authoring).
- **Why:** the walkthrough showed the architecture is sound; the stall is a
  punch-list, not an architecture failure. A ground-up rebuild discards working
  code to re-arrive at the same plateau. This path is the one that breaks the
  rebuild loop.

## META — Working method: every rewrite runs through the decision ritual · DECIDED
We do not just rewrite. For EACH individual solution we go through the same
process Foundry is meant to instill. This dogfoods the product: the rewrite
becomes the first real dataset for the decision-interview feature.

The ritual, per solution:
1. **Frame** — what are we solving, what are the constraints/non-negotiables.
2. **Alternatives** — generate 2–3 *genuinely distinct* approaches (not strawmen).
3. **Examine** — for each: how it works, what it touches, pros/cons.
4. **Mock (optional)** — throwaway code/HTML for the most uncertain one(s).
5. **Decide** — pick one, write the rationale AND the rejected paths here.
6. **Implement** — only after the above.

No solution gets written before it has an entry in this log. Skipping the ritual
is the failure mode we are explicitly designing against.
