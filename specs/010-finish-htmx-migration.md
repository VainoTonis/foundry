# Finish HTMX Migration

## Goal

Finish the frontend migration so Foundry is server-rendered with HTMX, uses Go standard library rendering, keeps `/api/*` JSON behavior stable, and uses only small JavaScript islands where browser APIs are required.

## Plan

1. Stabilize the HTMX shell.
   - Keep `/api/*`, static assets, and UI routes clearly separated.
   - Move inline templates out of `internal/api/handlers.go` into template files.
   - Add a render helper for full page vs fragment requests.

2. Make UI endpoints HTML-native.
   - Add UI POST handlers for common actions.
   - Use form values and return fragments or `HX-Redirect`.
   - Keep existing JSON API endpoints unchanged.

3. Finish backlog flow.
   - Keep `Build with AI`, `+ Spec`, and `+ Project` actions.
   - Return updated backlog fragments after create actions.
   - Redirect workflow creation to `/workflows/{id}`.

4. Finish spec detail.
   - Edit title/content through HTMX forms.
   - Promote and run workflow through UI handlers.
   - Show validation errors inline.

5. Finish workflow detail.
   - Render workflow and phases server-side.
   - Keep a small EventSource island for live updates.
   - Support phase logs, diff, approve, reject, and clean actions.

6. Fix spec builder responsiveness.
   - Use HTMX page plus chat JavaScript island.
   - Optimistically append user messages.
   - Disable input and show thinking state while the model works.
   - Stream `text_delta` into a live assistant bubble.
   - Refresh the builder fragment on `turn_complete` or error.
   - Preserve preview updates from `update_spec` tool calls.

7. Remove the half-migration JSON bridge.
   - Replace generic JSON form/button helpers with proper HTMX handlers where possible.
   - Keep only focused JavaScript for workflow streaming, phase log streaming, and spec builder streaming.

8. Clean template data.
   - Add small view models for backlog, spec detail, workflow detail, spec builder, and settings.
   - Keep template funcs limited to formatting helpers.

9. Add focused tests.
   - Smoke test full pages and fragments with `httptest`.
   - Smoke test template parsing/rendering.
   - Keep `node --check web/app.js` in verification.

10. Final cleanup.
    - Remove old SPA assumptions and unused dependencies.
    - Audit links and buttons for dead routes.
    - Verify with `gofmt`, `node --check web/app.js`, and `go test ./...`.

## Implementation Order

1. Spec builder responsiveness.
2. HTML-native UI handlers.
3. Template file split.
4. Smoke tests.
