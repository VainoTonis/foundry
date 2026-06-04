# Foundry — Direction

> PM synthesis after a direction conversation. Date: 2026-06-01.
> Companion to `architecture-overview.md`.

---

## Thesis (what Foundry IS now)

**Foundry is a forge for trustworthy specs.** Raw intent goes in; a decision
interview + cheap throwaway mocks hammer it into a spec where every meaningful
choice is made deliberately and the rejected paths are on record. The unattended
executor is the *payoff* you earn once a spec is trustworthy — not the thing to
build next.

The brake you asked for ("slow the frak down") **is the product**, not a detour
from it.

---

## Why this, grounded in the answers

- **Audience:** Just you, for now — but with ambition to grow it *if it works for
  you first*. So: build for your own velocity. No auth/multi-user/distribution.
  "Does it work for me" is the gate to growing it.

- **Autonomy:** Fully unattended is the destination — but specs need a lot of work
  first, especially interactive work with mocks. So unattended execution is the
  *reward*, unlocked once specs are trustworthy. Not the next problem.

- **Reality:** It runs but output isn't trustworthy. Root cause is **input
  quality**, not the reviewer. A thin spec → an agent improvising architecture at
  2am → untrustworthy commits. Fix the spec, and trust follows.

- **Mocks are thinking tools, not deliverables.** Code is cheap with LLMs;
  judgment is expensive. A mock makes a decision concrete enough to judge, then
  gets thrown away. **"Getting a good flow running for mocking is hard" — this is
  the single riskiest unknown in the whole project.**

- **Forcing function = decision interview.** Already seeded: migration `010`'s
  `draft_decisions` (prompt / options / decision / rationale). Promote this
  existing half-built idea to the center.

- **Deliberation lives IN the spec, for now.** Wanted it to feed memory, but
  doesn't trust memory yet and fears over-building. Decision: bake decisions +
  rejected options into the spec markdown. Durable, zero new systems, executor
  reads it for free. **Memory integration is PARKED until memory earns trust.**

---

## Bounded v1 — "the Forge"

### IN
- Intent dump → Foundry extracts the handful of real decisions, each with 2-4
  options.
- You answer each with a forced one-line rationale. Can't skip.
- For any decision you're unsure on: **"mock it"** → agent writes a throwaway
  spike (code, or mock HTML for UI) in a scratch space → you eyeball it → it
  informs the choice → it's discarded.
- Freeze: decisions + rationale + *rejected* paths baked into the spec doc. Now
  it's runnable by the existing phase loop.

### OUT — explicitly parked (anti-scope-creep list)
- ❌ Memory feedback loop — deliberation lives in the spec only, until memory is
  trusted.
- ❌ Rewiring the LLM reviewer (Gap #1 from architecture-overview.md).
- ❌ Multi-user / auth / distribution.
- ❌ Fancy multi-mock comparison UI — **one mock at a time, you read it.** Earn
  the fancy version later.

---

## PM recommendation: de-risk the mock flow FIRST, in isolation

The mock flow is the stated hard part. Don't bury it inside a big interview build
where you can't tell if *it* is the thing that feels bad.

Build a vertical spike of ONLY this:

> pick one decision → "mock it" → agent spikes in a scratch dir → it renders →
> you mark which way it pushed you → throw the spike away.

If that single interaction feels good, wrap the interview around it. If it
doesn't, you've spent two days, not two weeks, learning the core idea needs
rework.

---

## Open question (for next session)

Where to start:
- (A) De-risk the mock flow as a standalone spike (recommended), or
- (B) Build the decision-interview skeleton first (the `draft_decisions` bones
  already exist), then add mocking into it.
