# Foundry workflow premium mockups

These are disposable, standalone HTML/CSS design artifacts for the live workflow experience. They are not wired into server routes and require no build step or external assets.

## Directions

### `workflow-premium-command-center.html`

Dense mission-control layout optimized for active runs. It puts workflow status, Cerberus session metadata, phase state, bounded logs, and memory review in one scan. This is the strongest production direction because it maps closely to Foundry's current server-rendered workflow page and supports targeted SSE updates with stable status chips and phase rows.

### `workflow-premium-timeline.html`

Spacious narrative/audit-trail layout. It makes the workflow read like a chronological story and gives each phase room for rationale and evidence. This is useful for review mode, but may be too airy for monitoring an active agent run.

### `workflow-premium-glass-console.html`

Polished operations dashboard with glass panels, metrics, and a console feel. It demonstrates premium feedback, toasts, disabled/pending controls, and Cerberus safety states. It is visually strong, but the heavier aesthetic should be toned down for production readability.

## Recommended production carry-forward

Use the command-center direction as the base for the HTMX UI, with selective details from the others:

- Stable status chips for workflow and each phase (`running`, `awaiting review`, `done`, `failed`, `pending`) so SSE can update text/classes directly.
- A bounded live activity/log panel that appends rows incrementally by log id instead of replaying all history.
- Cerberus session visibility near the workflow header: session name, status, repo context, and cleanup safety.
- Immediate action feedback: disabled in-flight buttons, small inline spinner/pending label, and toast/banner results instead of routine `alert()` calls.
- Timeline-style phase hierarchy from the narrative mockup: active phase emphasis, clear completed/pending states, and review affordances.
- Conservative glass-console polish only where it improves clarity: subtle shadows, rounded panels, high-contrast chips, and non-motion-only status indicators.

Keep the production implementation server-rendered with HTMX plus vanilla `web/app.js`; these mockups are only visual references.
