# Glossary

Tags: #glossary

## Intent

Durable project will: goals, constraints, preferences, unresolved questions, and accepted tradeoffs.

## Spec

Executable markdown intent. A spec should be clear enough for Foundry to turn into phases.

## Workflow

One execution run against one spec.

## Phase

One bounded unit of agent work inside a workflow.

## Phase Feedback

Structured raw signal stored on a phase for later review and memory proposal generation. It is not approved memory by itself.

## Agent Work

Attempted implementation, review, or analysis performed by Cerberus or another agent.

## Evidence

Material used to judge work: logs, diffs, tests, events, summaries, and review notes.

## Decision

Recorded verdict and rationale. Examples: approve, reject, retry, pause, defer.

## Activity

Timestamped machine output during work. Logs and events are activity.

## Change

Concrete modification or artifact produced by agent work.

## Conversation

Temporary chat used to shape intent or specs. Useful, but not durable by itself.

## Project Memory Namespace

Per-project path inside the configured private memory repo. Approved memory is loaded from Markdown files under this namespace.

## Memory Update Job

Workflow-scoped proposal for durable memory changes. It becomes approved memory only when accepted and written to the private memory repo.
