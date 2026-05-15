# 001: Track Rules — Mechanical Enforcement for PoC vs Polish

## Problem

Right now PoC and Polish differ only in a prompt overlay string and a softer/stricter
reviewer prompt. Both tracks run through the same runner with no mechanical gates.
The reviewer LLM is the sole enforcer of quality, which means:

- A Polish phase can "pass" with zero tests if the LLM doesn't notice.
- File scope is unchecked — a phase can touch anything in the repo.
- There's no pre-review gate, so every phase pays a reviewer API call even for
  obviously broken output (no diff, test failures, leftover TODOs).

## Solution

Add a `track_rules` concept: a set of mechanical checks the runner applies *before*
calling the reviewer. PoC has no mechanical gates. Polish has real ones.

### Track rules by track

**PoC:**
- No mechanical checks before review.
- Reviewer bar: "does this plausibly accomplish the goal."
- No change from current behavior.

**Polish:**
- Pre-review gates (run by the runner, not the LLM):
  1. **Test exit code** — cerberus must exit 0 AND the session must include a test run.
     Detect via cerberus logs or a convention (e.g. phase prompt includes "run tests").
  2. **File scope** — if the spec declares `allowed_paths` (a new optional field),
     the diff must only touch files under those paths. Violations fail the phase
     without a reviewer call.
  3. **Marker scan** — `grep -rn 'TODO\|FIXME\|HACK\|XXX'` on touched files.
     Any matches fail the phase.
- Only after all gates pass does the reviewer run.
- Reviewer bar stays the same (tests cover public surface, no unexported tests).

### Data model changes

```sql
-- Migration 004_track_rules.up.sql

-- Allowed file paths for Polish scope enforcement (optional).
-- If empty, no scope check is applied.
ALTER TABLE specs ADD COLUMN allowed_paths JSONB NOT NULL DEFAULT '[]';

-- Track rules resolved at workflow creation, stored as snapshot.
-- Runner reads this, not the track string, to decide what to enforce.
ALTER TABLE workflows ADD COLUMN track_rules JSONB NOT NULL DEFAULT '{}';
```

`track_rules` JSON shape:

```json
{
  "require_tests": false,
  "require_test_pass": false,
  "allowed_paths": [],
  "marker_scan": false
}
```

Resolved at workflow creation from track + spec fields:

| Field              | PoC     | Polish                         |
|--------------------|---------|--------------------------------|
| require_tests      | false   | true                           |
| require_test_pass  | false   | true                           |
| allowed_paths      | []      | spec.allowed_paths (if set)    |
| marker_scan        | false   | true                           |

### Runner changes — `internal/workflow/runner.go`

Between cerberus completion and the `awaiting_review` transition, add a gate step:

```
cerberus exits 0
  -> collect diff + files_touched
  -> run track gates:
       if track_rules.marker_scan:
         grep TODO/FIXME/HACK/XXX in touched files -> fail if found
       if track_rules.allowed_paths is non-empty:
         check every touched file is under an allowed path -> fail if not
       if track_rules.require_test_pass:
         check cerberus logs for test runner output + exit code -> fail if missing or non-zero
  -> all gates pass? -> awaiting_review -> call reviewer
  -> any gate fails? -> phase fails with gate_failure notes (no reviewer call)
```

The gate failure should be recorded in `review_notes` with a prefix like
`[GATE] marker_scan: found TODO in internal/api/handlers.go:42` so it's
distinguishable from reviewer failures in the UI.

### What this does NOT do

- Does not add new tracks. PoC and Polish stay as the only two.
- Does not change the reviewer prompt or LLM call.
- Does not enforce gates retroactively on existing workflows.
- Does not require `allowed_paths` — it's opt-in even for Polish.

### Build order

1. Migration `004_track_rules.up.sql`
2. Update `internal/db/queries.go` — read/write new columns
3. Add `internal/workflow/gates.go` — pure functions: `CheckMarkers`, `CheckScope`,
   `CheckTestPass`. Input: diff string, files list, logs string, track_rules.
   Output: list of gate failures (empty = pass).
4. Update `internal/workflow/runner.go` — call gates between cerberus done and review.
5. Update `internal/api/handlers.go` — resolve track_rules at workflow creation.
6. Update `web/app.js` — show gate failures distinctly from reviewer failures.

### Why this matters

The reviewer LLM is expensive and unreliable for mechanical checks. It might miss a
TODO, might not notice a file outside scope, might pass a phase with no tests.
Mechanical gates are cheap, deterministic, and run before the API call. The LLM
should only judge things that require judgment — "does this approach make sense?"
not "did the tests pass?"
