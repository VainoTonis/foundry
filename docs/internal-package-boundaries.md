# Internal Package Philosophy

This document is the stable rulebook for keeping Foundry understandable. It should survive file renames, package splits, SSE becoming WebSockets, and future feature cuts.

Do not treat this as a map of current files. Treat it as a filter for new code.

## Main Function

When adding or moving code, run this decision function first:

```text
1. Name the decision this code owns.
2. Name the thing that would make this code change.
3. Put it beside code with the same answer.
4. Keep transport, policy, persistence, rendering, and external tools separate unless the feature is still tiny.
5. If the package now has two unrelated reasons to change, split before adding more.
```

If step 1 is hard, code shape is not ready yet. Write smaller code or name the missing concept first.

## Responsibility Types

Keep these types of responsibility separate by default.

### Transport

Transport receives input and returns output through a protocol.

Examples:

- HTTP handlers
- request parsing
- response status codes
- JSON encoding
- SSE or WebSocket endpoint wiring

Transport should not decide product policy. It should call code that does.

### Policy

Policy decides what should happen.

Examples:

- workflow state transitions
- whether a phase can continue
- whether a draft can become a spec
- retry rules
- cleanup safety rules

Policy should be testable without HTTP, HTML, or external commands.

### Persistence

Persistence stores and loads durable state.

Examples:

- SQL queries
- row structs
- database error mapping
- transactions

Persistence should not know why a workflow exists. It should expose explicit operations.

### Presentation

Presentation turns state into something a human sees.

Examples:

- templates
- view models
- formatting helpers
- UI-specific labels

Presentation should not mutate application state.

### Integration

Integration talks to external tools or services.

Examples:

- CLI wrappers
- API clients
- command argument construction
- stdout/stderr handling

Integration should be boring. It should not contain product behavior unless the external tool itself defines that behavior.

### Parsing And Translation

Parsing and translation convert one shape into another.

Examples:

- markdown spec parsing
- prompt assembly from structured input
- config decoding
- event payload normalization

Parsing should not perform side effects unless that is the explicit purpose of the package.

## File Shape

Prefer files that reveal the package quickly.

Within a file, put constants and types first when present, then the main entrypoint. Readers should see vocabulary before flow, and top-level flow before helper details.

Prefer this order:

```text
package/imports
constants
types
main exported function or method
small orchestration helpers
leaf helpers
```

Put package-level shared types in `types.go`. Keep file-local types near the top of the file that owns them.

Good examples:

- `types.go` for shared package types
- `errors.go` for package errors
- `helpers.go` only when helpers are truly generic inside that package

Avoid dumping unrelated structs into `types.go`. Types should still belong to the package's one responsibility.

## Package Shape

Package names should describe a stable responsibility, not a current mechanism.

Prefer names that survive implementation changes.

For example, if live updates might move from SSE to WebSockets, avoid making policy depend on an `sse` package. Keep protocol details at the transport edge and pass events through a smaller abstraction.

Create a new package only when the code has its own vocabulary, lifecycle, and tests.

Do not create a package only because a file is long. Split files first if the package responsibility is still correct.

## Import Direction

Imports should point from edges toward core decisions.

General direction:

```text
transport/presentation -> policy -> persistence/integration/parsing
```

Rules:

- Persistence must not import transport or presentation.
- Integration wrappers must not import product policy.
- Parsing packages must not mutate durable state unless explicitly named for that purpose.
- Presentation must not execute workflows or external tools.
- Policy may coordinate other packages, but should not become those packages.

Cycles mean boundaries are unclear. Break the cycle by naming the missing responsibility.

## Split Signals

Split before adding more when any of these are true:

- File has multiple unrelated reasons to change.
- Package name no longer explains most files inside it.
- Tests need HTTP, DB, templates, and external commands to verify one rule.
- Removing one feature requires touching unrelated features.
- Helpers need names like `common`, `misc`, `utils`, or `manager`.
- A wrapper package contains product prompts or state-machine policy.

## Acceptable Temporary Mess

Some mess is acceptable while a feature is being discovered.

Temporary means:

- it is small
- it has one obvious removal path
- it does not infect stable packages
- it is cleaned up before more features build on top

Temporary does not mean abandoned half-designs stay forever.

## Cleanup Discipline

During structure cleanup, do one kind of work at a time.

Allowed:

- move code
- split files
- delete dead feature code
- update imports
- add or update docs
- keep tests/build passing

Avoid:

- new features
- UI redesigns
- policy changes
- broad rewrites
- compatibility layers for abandoned behavior

## Review Checklist

Before accepting structural work, ask:

- What responsibility does this code own?
- What would make it change?
- Is that reason shared by nearby code?
- Did imports keep edge concerns out of core packages?
- Can a future reader find the main flow at the top?
- Are shared types separated from orchestration code?

If answers are vague, structure is not done.
