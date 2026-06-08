# API Mock — Extract Draft Authoring

## Component picked

Pull **spec draft authoring** out of `internal/api/handlers.go`.

This is the right first component because it is both:

- product-core: spec building / Forge lives here
- currently buried: `handleSpecDrafts`, `handleSpecDraft`, spec extraction helpers, memory write helpers, draft streaming/recovery hooks

## Current shape

`internal/api/handlers.go` owns too many concerns:

- route registration
- HTML templates
- JSON endpoints
- spec-draft chat orchestration
- cerberus session setup
- memory preload/write
- draft save/freeze/delete cleanup
- helper parsing for final specs

Draft routes today:

```text
GET    /api/spec-drafts
POST   /api/spec-drafts
GET    /api/spec-drafts/{id}
DELETE /api/spec-drafts/{id}
GET    /api/spec-drafts/{id}/messages
POST   /api/spec-drafts/{id}/message
POST   /api/spec-drafts/{id}/save
GET    /api/spec-drafts/{id}/stream
```

Main code chunk today:

```text
internal/api/handlers.go
  handleSpecDrafts        lines 2835+
  handleSpecDraft         lines 2931+
  extractFinalSpec        lines 3139+
  extractSaveReady...     lines 3159+
  writeSpecMarkdown...    lines 3239+
```

## Target shape

Keep package `api`. Do not create new package yet. First carve file boundaries, not architecture boundaries.

```text
internal/api/
  server.go              Server, NewServer, routes, runtime settings
  ui.go                  templates + UI handlers
  drafts.go              draft HTTP routes + draft orchestration
  drafts_spec.go         final-spec extraction + save-ready validation
  drafts_memory.go       write finalized spec markdown to memory repo
  cerberus_events.go     callback ingestion stays where it is
  handlers.go            temporary leftovers during migration
```

Minimal first extraction:

```text
internal/api/drafts.go
  func (s *Server) handleSpecDrafts(w http.ResponseWriter, r *http.Request)
  func (s *Server) handleSpecDraft(w http.ResponseWriter, r *http.Request)

internal/api/drafts_spec.go
  func extractFinalSpec(messages []byte) string
  func extractSaveReadyMarkdownSpec(content string) string
  func extractSpecFromMarkdownFence(content string) string
  func extractSpecFromTitle(content string) string
  func isSaveReadySpec(content string) bool
  func extractSpecTitle(specContent string) string
  func slugifySpecFilename(s string) string

internal/api/drafts_memory.go
  func writeSpecMarkdownToMemory(repoPath, namespace string, draftID int64, title, content string) (string, error)
```

No behavior change. Same package means no exported names needed.

## What this buys

- Draft authoring becomes findable.
- Forge work gets one file to evolve in.
- Existing tests can move with helpers without changing test semantics.
- No route behavior changes.
- No package dependency fight yet.

## What this does not solve

- `Server` still knows everything.
- `cerb.SetRepoPath` shared mutable-state bug still exists.
- draft flow is still free-form chat, not decision interview.
- streaming/recovery still split across files.

Those are later. First cut should only make authoring visible.

## Second step after extraction

Once draft code has its own file, add decision endpoints beside it:

```text
GET  /api/spec-drafts/{id}/decisions
POST /api/spec-drafts/{id}/decisions
POST /api/spec-drafts/{id}/decisions/{decision_id}/answer
POST /api/spec-drafts/{id}/decisions/{decision_id}/dismiss
POST /api/spec-drafts/{id}/decisions/{decision_id}/mock
```

These map directly onto existing DB layer:

```text
db.CreateDraftDecision
db.ListDraftDecisionsByDraft
db.UpdateDraftDecision
```

The first real Forge slice can then live in `drafts_decisions.go` without bloating generic handlers again.

## Mocked handler ownership

```text
drafts.go
  handleSpecDrafts
    GET  -> list drafts
    POST -> create draft + start cerberus chat

  handleSpecDraft
    /stream   -> streamDraftEvents
    /messages -> return draft messages
    /message  -> append user msg + send cerberus msg
    /save     -> extract final spec + create spec + freeze draft
    DELETE    -> close/clean cerberus + delete draft

drafts_decisions.go
  handleDraftDecisions
    GET  -> list decisions
    POST -> create extracted/manual decision

  handleDraftDecision
    /answer  -> set decision+rationale, status=answered
    /dismiss -> status=dismissed
    /mock    -> create mock attempt record, start throwaway documentation/mock flow
```

## Why this order

API god-file refactor should serve Forge, not become independent cleanup project.

So order is:

1. Extract draft authoring into files.
2. Add decision endpoints.
3. Add one mock flow endpoint.
4. Only then consider larger API split.

This keeps momentum pointed at spec-building while reducing `handlers.go` pain where it matters.
