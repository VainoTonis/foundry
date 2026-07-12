# Foundry — Decision Log

> Why things are the way they are. Started 2026-06-04 after a full read-through.
> Status legend: DECIDED · DIRECTION (agreed, not yet detailed) · OPEN · RISKY (needs real analysis before committing).

---

## D1 — The reviewer runs through cerberus, not a separate harness · DECIDED
Foundry should not maintain a second LLM harness for review/judgment. Reviewing
is an LLM workflow like any other, so it should go through cerberus.
- **Why:** one harness to maintain; cerberus already does LLM calls.
- **Ripple:** may need a cerberus mode for "judge this diff, return structured
  verdict, don't touch the repo".

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

## D7 — Intent docs were removed on purpose · DECIDED
The `intent/*.md` and `docs/*` files referenced by the README and the phase
prompt builder were intentionally ripped out — there were multiple copies of the
same docs, no structure, just confusing.
- **Decision:** drop prompt-builder references so phase prompts stop pointing at
  missing docs. Durable intent belongs in the saved spec's global context until a
  better system earns its way back in.

## D8 — overloaded HTTP/API surface rewrite · DONE
The old God package was split into HTTP edge, JSON API, web UI, stream, and
authoring responsibilities.
- **Why:** the old package mixed routing, UI, authoring, streaming, settings, and
  product behavior.

## META — Rebuild-from-zero vs. evolve in place · DECIDED: EVOLVE IN PLACE
Standing temptation was to strip everything and start from 0. Self-identified as
a recurring pattern ("I cannot keep rebuilding things every time I get to this").
- **Decided:** evolve in place. Keep the working engine, data model, cerberus,
  and streaming. Rewrite only overloaded areas and finish the two organs
  (judgment, authoring).
- **Why:** the read-through showed the architecture is sound; the stall is a
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
