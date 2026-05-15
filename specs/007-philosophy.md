# 007: Philosophical Principles

Not a build spec. A reference for decision-making when the specs don't cover
a situation.

---

## The orchestrator never learns about the agent

The deepest rule. Foundry owns specs, phases, sequencing, review, memory.
Cerberus owns execution. The runner should never branch on what happened
*inside* a cerberus session — only on what came *out* (exit code, diff,
files touched). If the runner starts parsing cerberus logs to understand
what the agent did, the seam has leaked.

The corollary: the agent should never learn about the orchestrator. The phase
prompt should contain the goal, the context, and the track overlay. Not the
workflow ID, not the retry count, not the budget remaining, not the phase
position. The agent doesn't need to know it's being orchestrated.

---

## Set goals, not steps

OpenClaw's subagent system delegates tasks, not procedures. The parent says
"do X," the child figures out how. If the parent starts dictating steps,
it's doing the child's job with extra latency.

For foundry: a phase goal should describe the *outcome*, not the
implementation. "Create a user table with email, hashed password, and
created_at" is a goal. "Run `CREATE TABLE users ...` then create
`internal/db/users.go` with a `CreateUser` function" is micromanagement
that the LLM will follow blindly even when it has a better idea.

The spec author sets direction. The agent makes implementation decisions.
The reviewer validates the result. Nobody does two jobs.

---

## No hidden state

OpenClaw agents only "remember" what's written to disk. There's no in-memory
state that survives across sessions, no invisible context that accumulates.
If a fact matters, it's in a file (MEMORY.md, SOUL.md, IDENTITY.md) or it
doesn't exist.

For foundry: the decision record on the phase row is the memory. If something
isn't captured in `decision_summary`, `decision_rationale`, or `files_touched`,
it didn't happen as far as the system is concerned. Don't build invisible
state channels between phases (shared environment variables, temp files that
persist across cerberus sessions, in-memory caches in the runner). Each phase
should be independently understandable from its row in the database.

This also means: if a future phase needs to know what a previous phase decided,
that information must be in the prompt — pulled from the decision record,
not from runner memory. The runner is a state machine, not a knowledge store.

---

## Move discovery earlier, not cache it later

OpenClaw's hottest rule: "Do not fix repeated request-time discovery with
scattered caches. Move the canonical fact earlier; reuse prepared runtime
objects; delete duplicate lookup branches."

For foundry: when the workflow starts, resolve everything — parse the spec,
extract phases, snapshot the track, resolve track_rules, resolve the cerberus
profile, compute the budget. Store it all on the workflow/phase rows. During
phase execution, the runner reads prepared data. It never re-queries the spec,
re-parses markdown, re-resolves config, or re-checks the profile.

The temptation is to "just cache it" when something is slow. The fix is to
not need it at that point at all.

---

## Errors are classified, not generic

OpenClaw classifies every error into a closed enum (`billing`, `rate_limit`,
`auth`, `timeout`, `format`, `model_not_found`, `session_expired`). Each
class has a different handler — rate limits suspend the session, auth errors
are permanent, timeouts retry once.

For foundry: phase failures should carry a reason enum, not just "failed":

- `cerberus_exit_nonzero` — agent crashed or errored
- `cerberus_timeout` — wall-clock limit hit
- `no_diff` — agent exited 0 but produced no changes
- `gate_failure` — mechanical check failed (from spec 001)
- `review_fail` — reviewer LLM rejected the diff
- `budget_exceeded` — workflow cost limit hit

The runner's retry/pause/fail logic should switch on this enum, not on
string matching or ad-hoc conditionals. The UI shows different messages for
different failure classes. Future automation (auto-retry rate limits but not
auth errors) needs the classification to exist.

---

## The conversation is the audit trail, not the source of truth

From spec 002, but it's a general principle. Users interact through
conversations, chatboxes, UIs. The system acts on structured data extracted
from those interactions. The extraction is a one-way gate.

If you can re-derive the structured data from the conversation, the
conversation is redundant. If you can't, the structured data is incomplete.
Design for the second case: the structured data should always be sufficient
on its own. The conversation is for humans reviewing "why did the system
do that?" — never for the system deciding what to do.

---

## Mechanical checks before judgment calls

From spec 001, generalized. Any check that can be done deterministically
should run before any check that requires LLM judgment. Reasons:

1. Deterministic checks are free. LLM calls cost money.
2. Deterministic checks are reproducible. LLM judgment varies.
3. Failing fast on a missing test is better UX than waiting 30 seconds
   for a reviewer to notice the same thing.

Apply everywhere: validate spec format before sending to the creation LLM.
Check budget before starting a cerberus session. Verify the repo exists
before creating a workflow. Parse config before starting the server.

---

## Two quality bars, same machinery

PoC and Polish run through the same runner, same cerberus client, same
reviewer, same database schema. The difference is in what the runner
*enforces*, not in what code path it takes. No if/else branches that
duplicate the phase execution flow for different tracks.

If a third track is added someday, it should be a new `track_rules`
configuration, not a new code path. The runner is generic. Tracks are data.
