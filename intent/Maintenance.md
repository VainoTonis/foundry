# Maintenance

Tags: #maintenance #agents

This page defines how humans and agents should update the intent wiki.

For when agents should read and use the wiki, see [[Agent Workflow]].

## Ownership

Humans own direction.

Agents may propose and apply maintenance updates when intent becomes durable.

## When To Update

Update this wiki when:

- A product direction becomes clearer.
- A constraint is added, removed, or clarified.
- A decision is accepted.
- An open question is answered or discovered.
- A spec or workflow reveals reusable knowledge.

Do not update this wiki for temporary implementation details, one-off logs, or ideas the user has not accepted as durable.

## How To Update

- Prefer small edits to existing pages.
- Add Obsidian `[[links]]` for core concepts.
- Keep `[[Decisions]]` newest-first.
- Move answered questions out of `[[Open Questions]]` only when the answer is recorded elsewhere.
- Preserve provenance by linking related pages or specs.
- Avoid creating many tiny pages before concepts are stable.

## Lint Checklist

Periodically check for:

- Contradictions between pages.
- Stale constraints.
- Open questions that are already answered.
- Important concepts with no links.
- Decisions without rationale.
- Specs that imply new durable intent but never updated this wiki.
