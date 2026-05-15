# 005: Doctor Command — Explicit Repair Instead of Startup Magic

## Problem

As foundry evolves, the data model will change. Decision record schemas will gain
fields. Config.yaml will add keys. Spec markdown format might get new conventions.
Workflow rows from old runs will have stale shapes.

The temptation is to auto-migrate on startup: read old data, detect old shape,
rewrite it. This is what openclaw explicitly moved away from, and for good reason:

- Silent startup mutations are invisible. The user doesn't know their data changed.
- Migration bugs corrupt live data with no audit trail.
- Startup time grows with migration count.
- Error handling during startup migration is awkward — do you refuse to start?
  Log and continue? Partial migrate?

## What openclaw does

`openclaw doctor --fix` is a dedicated command that:

1. **Reports** what it found (formatted issue lines).
2. **Previews** what it would change (without `--fix`, read-only).
3. **Backs up** before writing (`.bak` files).
4. **Confirms** interactively (unless `--non-interactive`).
5. **Handles categories separately**: config migration, state integrity, auth
   health, service health, plugin readiness.

Runtime paths never auto-repair. They read canonical shapes and fail explicitly
if the shape is wrong ("run `openclaw doctor --fix`").

## What foundry should do

### `foundry doctor` command

Add a CLI subcommand (or a flag on the server binary) that inspects and optionally
repairs foundry state.

```
foundry doctor           # report only
foundry doctor --fix     # report + repair
```

### Check categories (v0.0.1)

**1. Schema shape checks**
- Scan `phases.files_touched` for non-array JSONB (old shape or corruption).
- Scan `workflows.track_rules` for missing/unknown keys.
- Scan `specs.allowed_paths` for non-array JSONB.
- Report: "phase 42: files_touched is not a JSON array"
- Fix: normalize to canonical shape, preserve data where possible.

**2. Stale workflow detection**
- Find workflows with `status=running` where no phase has `status=running`
  and the last phase activity was >2x the timeout ago.
- Report: "workflow 7: status=running but no active phase, last activity 3h ago"
- Fix: set workflow status to `paused`.

**3. Config validation**
- Read config.yaml, check for unknown keys, deprecated keys, type mismatches.
- Report: "config.yaml: unknown key 'cerberus_timeout' (did you mean 'default_phase_timeout_seconds'?)"
- Fix: remove unknown keys (with backup), migrate renamed keys.

**4. Cerberus session cleanup**
- List cerberus sessions matching `foundry-*` pattern.
- Cross-reference with active phases.
- Report: "orphaned cerberus session: foundry-12-p3 (no active phase)"
- Fix: `cerberus --name <session> clean`.

**5. Database connectivity**
- Verify postgres connection, check migration version, report drift.
- No fix — just report.

### Output format

```
$ foundry doctor

[ok]  database connection
[ok]  migration version: 005
[warn] workflow 7: status=running but last phase activity 3h12m ago
[warn] orphaned cerberus session: foundry-12-p3
[err]  phase 42: files_touched is not a JSON array

2 warnings, 1 error. Run 'foundry doctor --fix' to repair.
```

### Runtime behavior when shape is wrong

When the runner or API encounters data it can't parse (bad JSONB shape, unknown
track, missing required field), it should fail with a message pointing to doctor:

```
error: phase 42 has invalid files_touched shape; run 'foundry doctor --fix'
```

Not: silently default to empty array. Not: auto-repair and continue.

### Implementation

```
cmd/
  server/main.go      (existing)
  doctor/main.go      (new — or add as subcommand to server binary)
internal/
  doctor/
    doctor.go          — runs all checks, collects findings
    checks.go          — individual check functions
    fixes.go           — individual fix functions
    report.go          — formatting
```

Each check is a function: `func(ctx, pool) []Finding`. Each fix is a function:
`func(ctx, pool, Finding) error`. Doctor runs all checks, reports, then (with
`--fix`) runs fixes for each finding that has one.

### What this does NOT do

- Does not run on startup. Ever.
- Does not auto-schedule. User runs it manually.
- Does not handle SQL schema migrations (that's golang-migrate's job).
- Does not repair spec content or conversation history.

## Build order

1. Define `Finding` type (category, severity, message, fixable bool).
2. Implement stale workflow check (most immediately useful).
3. Implement orphaned session check.
4. Implement JSONB shape checks (needed after spec 001 adds track_rules).
5. Implement config validation.
6. Wire up CLI entry point.
7. Add "run foundry doctor" hints to runtime error messages.
