# 004: Prompt Cache Stability

## Problem

Foundry sends prompts to cerberus (via the agent LLM) and to the reviewer LLM.
Both of these are API calls where prompt caching saves money and latency. But
caching only works if the same prefix bytes are sent across calls.

Right now nothing guarantees ordering. If the spec has tags, if phase context
includes file lists, if the reviewer gets files_touched — any of these can
serialize in a different order between runs and break cache prefixes.

This matters more as costs grow. A Polish workflow with 6 phases makes 6+
cerberus calls and 6 reviewer calls. Cache hits on the global context prefix
(which is identical across all phases) would cut costs significantly.

## What openclaw does

OpenClaw has an explicit cache stability layer:

1. **Deterministic ordering everywhere** — plugin lists, capability IDs, context
   files, provider registries are all sorted lexicographically before prompt
   assembly. Dedicated functions like `normalizePromptCapabilityIds()` and
   `sortContextFilesForPrompt()`.

2. **Cache boundary markers** — the system prompt is split into a stable prefix
   (cached) and a dynamic suffix. Additions go after the boundary to preserve
   the prefix. `SYSTEM_PROMPT_CACHE_BOUNDARY` is a literal marker in the prompt.

3. **Trailing-turn-only cache control** — cache write scope is placed only on
   the last user turn, so the prefix stays valid across turns.

## What foundry should do

### Phase prompts (sent to cerberus)

The prompt is assembled in `spec.BuildPrompt(globalContext, goal, trackOverlay)`.
This is already deterministic for a given spec — same global context, same goal
text, same overlay string. Good.

But if track_rules (from spec 001) or allowed_paths end up in the prompt, sort
them before serialization:

```go
// internal/spec/prompt.go
sort.Strings(allowedPaths)
```

### Reviewer prompts

`review.buildUserMessage()` assembles goal + diff + track overlay. The diff
changes per phase (expected — can't cache). But the system prompt is identical
across all reviewer calls. Structure the API call so the system prompt is a
stable prefix:

```
messages[0] = system prompt (identical across all calls, cacheable)
messages[1] = user message (goal + diff + test output, varies per phase)
```

This is already the structure. The thing to protect: don't add per-phase
metadata (phase position, workflow ID, timestamps) into the system prompt.
Keep it static.

### Decision records / files_touched

When `files_touched` is stored as JSONB, sort the file list before writing:

```go
sort.Strings(files)
```

This isn't for caching — it's for diffability. When comparing two phases or
two workflow runs, sorted file lists are easier to diff than random-order ones.

### Future: if conversation history enters the prompt

Spec 002 adds conversational spec creation. If conversation history ever gets
prepended to phase prompts (as additional context), the entire conversation
must be frozen at workflow creation and stored on the workflow row — not
re-fetched from the conversation table. Re-fetching could return different
formatting or additional messages.

## Build order

1. Sort `files_touched` before writing in `runner.go` (or in the new
   `cerberus.FilesTouched()` after spec 003).
2. Sort `allowed_paths` before embedding in prompts (after spec 001).
3. Add a comment in `review.go` marking the system prompt as cache-stable —
   don't add per-call metadata to it.
4. If using Anthropic API: set `cache_control` on the system message.
   If using OpenAI-compatible: just keep the prefix stable and let
   server-side automatic caching work.

## What this does NOT do

- Does not add a caching layer in foundry. Caching is the API provider's job.
- Does not change prompt content. Only ordering and structure.
- Does not require any migration.
