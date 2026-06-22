# Web UI Internal Cleanup Plan

## Goal

Keep one `internal/webui` package, but make it easier to navigate and safer to grow.

The cleanup is an internal split only:

- one Go package: `internal/webui`
- feature-grouped files and templates
- shared UI helpers kept central
- product actions moved out only when they are clearly not UI concerns

No frontend package split. No webui subpackages. No microfrontends. No generic design-system layer.

## Target Shape

```text
internal/webui/
  core.go          # Handler, config, route registration
  renderer.go      # template parsing and ExecuteTemplate wrapper
  funcs.go         # generic template funcs
  htmx.go          # HTMX response helpers
  shell.go         # shell/page rendering

  backlog.go
  projects.go
  specs.go
  workflows.go
  phases.go
  builder.go
  settings.go

  diff_view.go     # workflow/phase diff display helpers
  log_view.go      # workflow/phase log display helpers

  templates/
    shared/
      shell.html
      flash.html
      empty_state.html

    backlog/
      page.html

    projects/
      list.html
      detail.html

    specs/
      detail.html

    workflows/
      detail.html

    phases/
      logs.html
      diff.html

    builder/
      start.html
      detail.html
      messages.html

    settings/
      page.html
```

This shape keeps locality without package overhead.

## Rules

`internal/webui` owns:

- server-rendered HTML
- HTMX fragments
- shell/layout
- shared UI partials
- view models
- display formatting
- small HTTP response helpers for UI routes

`internal/webui` may:

- parse form/query values
- load simple read data for pages
- call application/use-case functions
- return fragments, full pages, `HX-Redirect`, or HTTP errors

`internal/webui` should avoid:

- workflow policy
- multi-step product mutations
- direct workflow runner orchestration
- Cerberus behavior beyond displaying status/data
- broad shared helpers that are not UI-specific

## Phase 1: Record Current UI Surface

Create a compact route map for all server-rendered UI routes.

Record:

- route path
- full page route vs fragment route
- owning feature
- handler function
- template name
- whether route mutates state

Feature groups:

- backlog
- projects
- specs
- workflows
- phases
- builder
- settings
- shared shell

Purpose: make existing UI surface explicit before moving files.

## Phase 2: Split Renderer Internals

Keep package name `webui`, but split current renderer helpers by responsibility.

Target files:

```text
renderer.go   # template FS, parse, render helper
funcs.go      # date, datetime, money, json, string helpers
shell.go      # shellData and renderShell
diff_view.go  # diffSummary, diffRows, DOM-hook detection
log_view.go   # logRows and log classification
```

Rules:

- Generic template funcs stay domain-neutral.
- Feature-specific formatting should live near the feature handler or view model.
- Diff/log display helpers stay separate from core renderer setup.
- No behavior changes in this phase.

## Phase 3: Move Templates Into Feature Folders

Move templates from flat `templates/*.html` into feature folders.

Template names should make feature ownership obvious:

```text
shared.shell
backlog.page
projects.list
projects.detail
specs.detail
workflows.detail
phases.logs
phases.diff
builder.start
builder.detail
builder.messages
settings.page
```

Rules:

- Feature-specific markup stays in that feature folder.
- Shared templates must be domain-neutral.
- Do not create shared partials before there is repeated use.
- Copy once if needed; extract only after duplication becomes real.
- Preserve current UI behavior while moving.

## Phase 4: Normalize Feature Files

Each feature file should follow the same rough order:

```text
types/view models
page handler
fragment handler
mutation handlers
small feature-local helpers
```

Rules:

- Keep feature view models close to handlers.
- Keep feature-local helpers in the feature file unless they are reused.
- Do not create generic helper files for one caller.
- Prefer readable handlers over premature abstraction.

## Phase 5: Add HTMX Helpers Where Useful

Create `htmx.go` only for repeated response patterns.

Useful helpers may include:

```text
redirectHX(w, url)
html(w)
methodAllowed(w)
badRequest(w, msg)
serverError(w, err)
```

Rules:

- Helpers must reduce repeated noise.
- Helpers must not hide important behavior.
- Keep errors explicit at call sites when context matters.
- Do not build a framework.

## Phase 6: Normalize HTMX Route Conventions

Prefer clear page/fragment pairs:

```text
/projects
/projects/fragment
/projects/{id}
/projects/{id}/fragment
```

Rules:

- Full page routes render shell.
- Fragment routes render only feature content.
- Mutations return one of:
  - updated fragment
  - `HX-Redirect`
  - normal HTTP error with useful body
- Stable swap target remains `#app` unless a feature needs smaller target.
- IDs/classes/data attributes used by HTMX or JS are UI contracts.

Avoid route behavior that is hard to predict.

## Phase 7: Move Only Clear Product Actions Out

Do not create a large `app` layer just for tidiness.

Move code out of `webui` only when handler coordinates product behavior, not just UI response shape.

Likely candidates:

- create workflow from spec
- update spec status as part of workflow start
- start workflow runner
- settings updates that affect runtime services
- draft/save flows if UI handlers coordinate multiple product steps

Target direction:

```text
internal/app/
  workflows.go
  settings.go
  drafts.go
```

Example split:

```text
webui handler:
  parse form
  call app.StartWorkflowFromSpec(...)
  return HX redirect

app use case:
  load spec
  create workflow
  update spec status
  start runner
```

Rules:

- Simple read queries can stay in `webui`.
- Single simple writes can stay until they become awkward.
- Multi-step product mutations should move out.
- Do not move code just to satisfy a diagram.

## Phase 8: Keep Shared Frontend Assets Simple

Keep current shared assets unless feature-specific sections become obvious.

Current shape is acceptable:

```text
web/app.js
web/style.css
```

Rules:

- JS is progressive enhancement, not source of truth.
- Prefer HTMX attributes in templates over custom JS when reasonable.
- Split JS/CSS only when a feature-specific section is large enough to be easier as its own file.
- Do not introduce a bundler unless there is a concrete need.

Possible later shape:

```text
web/js/app.js
web/js/workflow.js
web/js/builder.js

web/css/base.css
web/css/components.css
web/css/workflow.css
web/css/builder.css
```

This is optional, not part of first cleanup.

## Phase 9: Add Lightweight Checks

Add tests/checks only where they protect real boundaries.

Useful checks:

- main UI routes return successful HTML with test fixtures
- fragment routes return fragment content, not full shell
- template names used by handlers exist
- `webui` does not import `workflow` after workflow start moves to an app use case

Avoid heavy architecture tooling.

## Phase 10: Final Cleanup

After internal movement:

- delete obsolete helpers
- delete unused templates
- remove duplicated route parsing if helper is useful
- make file names match features
- update package comments if needed
- run full tests
- smoke-test main UI flows

Smoke-test flows:

- open backlog
- create project
- create spec
- open spec detail
- start workflow
- open workflow detail
- view phase diff/logs
- open builder
- open settings

## Non-Goals

- No `internal/webui/backlog` subpackage.
- No frontend package split.
- No React/Vite setup.
- No microfrontends.
- No design system package.
- No generic component framework.
- No route compatibility layer unless a shipped URL needs it.
- No big service layer for simple read-only pages.

## Success Criteria

- `internal/webui` remains one package.
- Feature UI is easy to find.
- Templates are grouped by feature.
- Shared templates/helpers are small and domain-neutral.
- HTMX pages/fragments follow consistent conventions.
- Product-heavy mutations are not buried in UI handlers.
- Adding a new UI feature has an obvious file and template folder.
