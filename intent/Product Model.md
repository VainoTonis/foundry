# Product Model

Tags: #product #model

Foundry is an agent workbench and audit trail for turning specs into code changes.

It should make four things obvious:

- Here is [[Intent]].
- Here is [[Agent Work]].
- Here is [[Evidence]].
- Here is [[Decision]].

## Core Objects

[[Intent]] is durable product direction and project memory.

[[Spec]] is executable intent written as markdown.

[[Workflow]] is one run against one spec.

[[Phase]] is one bounded unit of work inside a workflow.

[[Agent Work]] is what Cerberus or another agent attempts during a phase.

[[Evidence]] is logs, diffs, tests, events, and review material.

[[Decision]] is the verdict and rationale for keeping, rejecting, retrying, or pausing work.

[[Activity]] is timestamped machine output while work happens.

[[Change]] is the concrete diff or artifact produced by work.

[[Conversation]] is temporary negotiation used to shape intent or specs.

## Product Shape

Backlog answers: what work exists?

Spec answers: what are we trying to build?

Run answers: what is the agent doing now?

Review answers: what changed, and is it safe?

Memory answers: what did we decide, and why?
