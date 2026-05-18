# Principles

Tags: #principles #engineering

## Product Principles

- Preserve intent before optimizing execution.
- Treat agent output as evidence, not truth.
- Prefer inspectable artifacts over hidden state.
- Keep humans in control of acceptance decisions.
- Make the audit trail useful after the run is over.

## Engineering Principles

- Small focused changes beat broad refactors.
- Explicit data flow beats hidden framework behavior.
- Simple structs and SQL beat opaque abstractions.
- Errors should be returned, surfaced, or handled at the call site.
- No ORM unless a future decision explicitly reverses this.
- Keep backend model clear before adding UI complexity.

## Agent Principles

- Agents should operate from specs and constraints, not vague chat memory.
- Agents should produce evidence that reviewers can inspect.
- Agents should update intent only when a decision or durable learning occurs.
