# Agent Workflow

Tags: #agents #workflow #intent

This page defines how agents should use the intent wiki during Foundry work.

The wiki is not decoration. It is context agents must read before shaping work, and memory agents must update when work changes durable understanding.

## Default Loop

```text
read intent -> clarify task -> draft or update spec -> do agent work -> gather evidence -> record decision -> update intent if durable
```

## 1. Before Drafting A Spec

Read these pages first:

- [[README]]
- [[Intent]]
- [[Product Model]]
- [[Principles]]
- [[Constraints]]
- [[Open Questions]]

Then read any linked pages relevant to the task.

Use these pages to decide:

- what problem the spec should solve
- what constraints the solution must respect
- what language the spec should use
- what open questions need user input before execution

## 2. When Writing A Spec

Specs should link back to relevant intent pages.

Use this pattern near the top:

```markdown
Related intent: [[Product Model]], [[Principles]], [[Constraints]]
```

If the spec depends on an unresolved question, either ask the user or link it explicitly:

```markdown
Open question: [[Open Questions]] - should intent updates be automatic or human-approved?
```

## 3. Before Running Agent Work

Confirm the spec has:

- clear goal
- bounded scope
- relevant intent links
- reviewable output
- known non-goals

If these are missing, improve the spec before execution.

## 4. During Review

Judge work against both the spec and relevant intent pages.

Evidence should answer:

- did work satisfy the spec?
- did work respect [[Principles]] and [[Constraints]]?
- did work create or answer any [[Open Questions]]?
- did work reveal a durable [[Decision]]?

## 5. After A Decision

If the decision is temporary, record it only in workflow/phase history.

If the decision is durable, update the wiki.

Durable updates may touch:

- [[Decisions]] for accepted tradeoffs
- [[Principles]] for durable taste or engineering rules
- [[Constraints]] for limits and commitments
- [[Glossary]] for vocabulary
- [[Open Questions]] for new or answered questions
- [[Product Model]] for conceptual changes

## 6. Intent Update Rule

No silent durable intent changes.

For now:

1. Agent proposes intent edits.
2. Human reviews the diff.
3. Human accepts, rejects, or asks for changes.

Only accepted durable changes should land in the wiki.

## 7. Minimal Agent Prompt Bundle

When an agent cannot read the full wiki, include this bundle:

- [[Intent]]
- [[Product Model]]
- [[Principles]]
- [[Constraints]]
- [[Open Questions]]
- relevant spec

This is the minimum context needed for agent work to reflect project intent.
