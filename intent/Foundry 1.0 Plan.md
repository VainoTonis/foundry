# Foundry 1.0 Plan

Tags: #intent #foundry #plan

## Product Definition

Foundry is a self-hosted web tool for managing private project memory around agentic work.

It connects:
- target git repos containing code
- one unified private memory repo containing markdown knowledge
- Cerberus as external LLM and agent bridge

Foundry does not replace Cerberus. Cerberus remains the tool that calls LLMs and runs agent work.

## Core Purpose

Primary value is memory.

Foundry exists so agentic work does not start from scratch each run. It records why work happened, what was learned, what failed, and what should influence future runs.

Priority order:
- Memory
- Orchestration
- Work tracking
- Spec generation

## Architecture Boundary

Foundry owns:
- web UI
- project registry
- run records
- phase records
- feedback records
- memory update workflow
- unified private memory repo integration

Cerberus owns:
- LLM execution
- agent execution
- model calls
- worktree and session behavior
- code changes

Target repos own:
- source code
- normal git history

Unified private memory repo owns:
- curated project memory
- specs
- run summaries
- durable lessons
- decisions
- patterns
- failure knowledge

## Storage Model

Markdown is primary citizen for durable knowledge.

Database stores runtime and index state:
- projects
- runs
- phases
- statuses
- feedback metadata
- Cerberus session references
- paths to markdown artifacts

Unified memory repo stores human-readable knowledge:
- facts
- decisions
- lessons
- failures
- patterns
- specs
- summaries

## Core Loop

1. User opens Foundry web UI.
2. User selects target git repo or project.
3. User writes intent.
4. Foundry uses private memory to help create spec.
5. User reviews and approves spec.
6. Foundry calls Cerberus to run work.
7. Run executes as phases.
8. Each phase emits feedback.
9. Foundry collects all phase feedback.
10. User may add final feedback.
11. Memory pass reviews collected feedback.
12. Cerberus updates private memory repo.
13. User reviews memory diff.
14. User accepts, rejects, or comments.
15. Approved memory influences future runs.

## Run Phases

A run contains multiple phases.

Each phase should produce structured feedback:
- phase name
- goal
- result
- useful context
- problems
- suggested memory
- confidence

Phase feedback is raw signal. It is not canonical memory.

## Memory Pass

After run phases complete, Foundry collects:
- approved spec
- run summary
- phase feedback
- user comments
- important errors
- current relevant memory

Then Foundry runs Cerberus against the private memory repo in a memory update context.

The memory pass decides what is worth updating. It should not blindly copy all feedback into memory.

Priority order for memory updates:
- explicit user feedback
- high-confidence phase feedback
- repeated issues across phases
- run result
- raw logs
- model inference

## Review Model

Memory updates are reviewed as git diffs in the private memory repo.

User can:
- accept memory update
- reject memory update
- comment and request revision

If user comments, Foundry reruns Cerberus on the same memory update job with the comment as follow-up context.

## Memory Policy

Feedback is not memory.

Feedback is raw signal. Memory is curated knowledge.

Do not let every phase write memory directly. Each phase writes feedback. One memory pass updates durable memory after the run.

No feedback means no canonical memory update by default.

## Cerberus Integration

Foundry treats Cerberus as a CLI bridge to LLM and agent work.

Foundry can run Cerberus against any git repo:
- code repo for implementation work
- private memory repo for spec creation and memory updates

Same mechanism, different context.

Examples:
- Spec creation targets private memory repo and writes spec markdown.
- Code execution targets code repo and modifies source files.
- Memory update targets private memory repo and modifies memory markdown.

Foundry should constrain Cerberus prompts with specific file paths and goals.

## Key Safety Rule

Foundry itself should not write private memory, prompts, logs, or agent metadata into target source repos by default.

Foundry writes to:
- database
- app data directory
- private memory repo

Cerberus behavior inside target repos is external to Foundry and can be addressed separately.

## Initial Milestones

### 0.1 Project Registry

- Register target git repo path.
- Register one unified private memory repo path at app/config level.
- Map each project to a namespace or directory inside the unified memory repo.
- Do not store separate memory repo paths per project.
- List projects in web UI.

### 0.2 Relevant Memory Review

- Load approved memory relevant to selected project and run.
- Show memory used for spec and review context.
- Show proposed memory diffs during memory review.
- Do not build a general memory browser in 1.0.
- Do not build manual memory editing UI in 1.0.

### 0.3 Intent To Spec

- Select project.
- Write intent.
- Use approved private memory as context.
- Generate spec markdown in memory repo.
- Review and approve spec.

### 0.4 Run Execution

- Run approved spec through Cerberus.
- Track phase status.
- Successful phase returns code, commit, and feedback.
- Failed phase returns error.
- Logs may be streamed or stored in app data, but are not durable memory.

### 0.5 Feedback And Memory Pass

- Collect phase feedback.
- Allow final user feedback.
- Run memory pass against private memory repo.
- Review memory diff.
- Accept, reject, or revise.
- Auto-commit accepted memory updates in the private memory repo.

### 0.6 Debug And Recovery

- Show failed phase.
- Show relevant logs.
- Create follow-up run from failure.
- Preserve failure learning through memory pass.

### 0.7 Tests

- Test project mapping.
- Test storage boundaries.
- Test run and phase state transitions.
- Test memory pass review states.
- Test that Foundry does not write target repo metadata directly.

### 0.8 UX Hardening

- Keyboard-friendly navigation.
- Verbosity levels.
- Mobile usable enough for review and feedback.

### 0.9 Documentation

- Install guide.
- Self-hosting guide.
- Private memory repo setup.
- Cerberus integration notes.
- Troubleshooting.

### 1.0 Stability

- No data loss.
- Clear errors.
- Stable storage layout.
- Stable memory workflow.
- Backup and export story.

## Decisions

- Foundry should not build a general memory browser in 1.0.
- Foundry should not build manual memory editing UI in 1.0.
- Accepted memory updates should be auto-committed in the private memory repo.
- Raw logs should never be committed to the private memory repo.
- Logs may be streamed or stored in app data only.
- A successful phase returns code, commit, and feedback.
- A failed phase returns an error.
- The minimum Cerberus output contract is code plus commit.
- Pending memory should not influence future specs before approval.
