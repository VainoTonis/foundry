# Working on this repository

## Attribute your own session to the plan you're working on

If you are an agent or orchestrator session doing real work against a Foundry
plan — reading it, implementing a step, reviewing it, opening a PR for it —
and you were **not** launched by Foundry's own server (a workflow phase or a
Steward review), Foundry has no way to know you exist or which plan you're
working on. There is no session-start event for it to hook.

Attach yourself early, before you start the real work, not after:

```
foundry sessions attach <your-session-name> --plan-id <id> [--plan-step-id <id>]
```

If you are a Pi orchestrator session, use `--self` instead of typing your own
session name by hand:

```
foundry sessions attach --self --plan-id <id> [--plan-step-id <id>]
```

This is not optional bookkeeping. Without it, `session_plan_links` has no
record that this work ever happened, and there is no automatic mechanism that
will ever recover it after the fact — `system_derived` linking only covers
sessions Foundry itself launched (workflow phases, Steward reviews). An
unattached session working on a real plan is a session-identity gap that will
never self-heal, unlike the ones migration 040/041 and their reconciliation
pass fixed for historical data.
