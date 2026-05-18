# 009 - Intent Wiki

## Purpose

Foundry needs a durable intent layer before specs and after decisions.

Current flow:

```text
spec -> phases -> workflow -> review -> decision record
```

Target flow:

```text
conversation / note / idea
-> intent wiki update
-> spec generated from intent
-> workflow phases
-> agent work
-> evidence
-> decision
-> intent wiki update
```

Intent should not live only in prompts, chats, or individual specs. Intent should compound as project memory.

Motto:

```text
Here is intent. Here is agent work. Here is evidence. Here is decision.
```

## Core Idea

Use plain Markdown as Foundry's first intent system.

The intent wiki is Obsidian-friendly and git-native. It records durable goals, constraints, product language, open questions, and accepted decisions. It should be readable by humans and usable by agents without database infrastructure.

Initial directory:

```text
intent/
  README.md
  Intent.md
  Product Model.md
  Principles.md
  Constraints.md
  Glossary.md
  Open Questions.md
  Decisions.md
  Maintenance.md
  Agent Work.md
  Evidence.md
  Decision.md
  Spec.md
  Workflow.md
  Phase.md
  Activity.md
  Change.md
  Conversation.md
```

## Product Model

Foundry should treat these as separate concepts:

- Intent: durable product will and project memory.
- Spec: executable intent written as markdown.
- Workflow: one run against one spec.
- Phase: one bounded unit of agent work.
- Agent Work: attempted implementation, analysis, or review.
- Evidence: logs, diffs, tests, events, summaries, and review notes.
- Decision: verdict and rationale for approve, reject, retry, pause, or defer.
- Activity: timestamped machine output during work.
- Change: concrete diff or artifact produced by work.
- Conversation: temporary negotiation used to shape intent or specs.

Specs are executable intent. The intent wiki is durable intent.

## Required Behavior

### Intent Wiki As First-Class Project Area

Foundry should know where intent pages live.

Config should eventually support:

```yaml
intent_dir: intent
specs_dir: specs
```

The first implementation can default to `intent/` and avoid schema changes.

### Spec Creation Reads Intent

Spec generation should not start from blank chat context.

When creating or refining a spec, Foundry should include key intent pages:

- `intent/README.md`
- `intent/Product Model.md`
- `intent/Principles.md`
- `intent/Constraints.md`
- `intent/Open Questions.md`
- relevant linked pages when known

The goal is to make generated specs reflect durable project memory.

### Agent Workflow Uses Intent

Agents should use `intent/Agent Workflow.md` as the operating procedure for wiki-backed work.

Required loop:

```text
read intent -> clarify task -> draft or update spec -> do agent work -> gather evidence -> record decision -> update intent if durable
```

This makes the wiki part of execution instead of a passive documentation folder.

### Cerberus Runners Receive Intent References

Each Cerberus phase runner should receive direct references to relevant intent pages.

Intent should guide judgment, but the phase goal still controls scope. The runner must not treat intent pages as permission for broad cleanup or unrelated product changes.

First version should add a reference block to every phase prompt:

```text
## Intent References

Before making changes, read these files:

- intent/Agent Workflow.md
- intent/Product Model.md
- intent/Principles.md
- intent/Constraints.md
- intent/Open Questions.md

Use intent as guidance. Implement only the phase goal.
```

This block should be added in the prompt path used by workflow phases, before `cerberus start` receives the final prompt.

Current implementation hook:

```text
internal/workflow/runner.go runPhase -> spec.BuildPrompt(...)
internal/spec/spec.go BuildPrompt(...)
```

Default references should be file paths, not inlined full wiki contents. This keeps prompts small and points agents at current files inside the repo.

Later, Foundry can inline a small summary if runners ignore references or if the execution environment cannot read the files.

### Spec-Declared Intent References

Specs may declare extra intent references using Obsidian links:

```markdown
Related intent: [[Activity]], [[Evidence]], [[Decision]]
```

Future runner behavior:

1. Parse `[[...]]` links from the spec global context and current phase body.
2. Resolve links to files under `intent/`.
3. Add resolved file paths to the phase prompt reference block.
4. Keep default references even when a spec declares extras.

Missing or unresolved links should not fail the workflow at first. They should appear as warnings in phase logs or review notes.

### Specs Link Back To Intent

New specs should reference relevant intent pages using Obsidian-style links.

Example:

```markdown
Related intent: [[Product Model]], [[Constraints]], [[Principles]]
```

Avoid required YAML frontmatter until it is clearly useful.

### Explicit Intent Update Operation

Foundry should support an operation like:

```text
Update intent from this conversation / spec / decision.
```

First version must be human-reviewed:

1. Agent proposes edits to intent pages.
2. Foundry shows a diff.
3. Human accepts or rejects.
4. Accepted edits update Markdown files.
5. Durable choices are recorded in `intent/Decisions.md`.

No silent intent mutation.

### Decision Feedback Into Intent

When a phase or workflow decision becomes durable, Foundry should propose updates:

- Product choice -> `Product Model.md` or `Principles.md`
- New constraint -> `Constraints.md`
- New vocabulary -> `Glossary.md`
- Unresolved issue -> `Open Questions.md`
- Accepted tradeoff -> `Decisions.md`

This closes the loop from execution back into memory.

### Intent Lint

Foundry should eventually support a lint pass for the wiki.

Command shape:

```text
foundry intent lint
```

Checks:

- broken `[[links]]`
- orphan pages
- stale constraints
- open questions that appear answered
- decisions without rationale
- specs that imply durable intent but did not update the wiki
- contradictions between intent pages and specs

This can begin as an LLM-driven review over Markdown files. No database required.

### Intent Ingest

Later, Foundry should ingest raw sources into the intent wiki.

Possible sources:

- chat transcript
- README or existing specs
- issue notes
- pasted article
- design document
- workflow decision summary

Flow:

```text
raw source -> proposed wiki edits -> reviewed patch -> intent updated
```

This follows the LLM-wiki pattern: raw sources remain source material, while the wiki is the compiled durable artifact.

## Agent Rules

Agents working with intent should follow these rules:

- Read relevant intent pages before drafting specs.
- Update intent only when information is durable.
- Prefer small edits to existing pages.
- Use Obsidian `[[links]]` for core concepts.
- Keep `intent/Decisions.md` newest-first.
- Preserve rationale for decisions.
- Move answered questions out of `Open Questions.md` only when the answer is recorded elsewhere.
- Never turn transient chat into durable intent without user approval.

## Data Model

Do not add database tables for the first version.

Markdown should be the source of truth.

Later, if reviewed intent updates need persistence, add a table like:

```text
intent_updates
  id
  source_type
  source_id
  summary
  patch
  status
  created_at
  applied_at
```

This should store proposals and approval state, not replace Markdown.

## Frontend Scope

No frontend work is required for the first version.

Obsidian is the initial graph viewer.

Future UI may add:

- Intent page browser
- intent graph view
- generate spec from intent
- propose intent update
- review intent diff
- intent lint results

## Build Order

1. Maintain `intent/` manually for several sessions.
2. Use `intent/Agent Workflow.md` as the manual operating procedure for all new intent-backed work.
3. Add spec convention: new specs link relevant intent pages.
4. Add default intent reference block to Cerberus phase prompts.
5. Add intent context bundle for spec-builder prompts.
6. Parse spec-declared `[[intent links]]` into extra runner references.
7. Add reviewed `propose intent update` operation.
8. Add intent lint over Markdown files.
9. Add optional indexing/search only after conventions stabilize.

## Non-Goals

- Do not build a database-backed knowledge graph yet.
- Do not auto-update intent silently.
- Do not add frontend graph tooling yet.
- Do not replace specs with the wiki.
- Do not require YAML frontmatter unless a concrete use case appears.
- Do not let runner intent references expand phase scope beyond the spec.

## Success Criteria

- A new spec can be generated from durable intent instead of blank chat context.
- Cerberus phase runners receive default intent references before execution.
- Important decisions feed back into the intent wiki after human approval.
- Obsidian graph shows useful relationships between product concepts.
- Agents can understand Foundry's direction by reading `intent/README.md` and linked pages.
- Intent, agent work, evidence, and decision remain separate concepts throughout the workflow.
