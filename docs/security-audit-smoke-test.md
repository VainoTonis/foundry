# Security Audit (Smoke Test): Cerberus HTTP Callback / Telemetry Ingest

## Scope

Focused review of the Cerberus HTTP callback trust boundary and the
telemetry ingest path it feeds into. No source changes were made; this is
an audit-only pass.

Files reviewed:
- `internal/httpserver/cerberus_api.go`
- `internal/httpserver/cerberus_events.go`
- `internal/telemetry/ingest.go`

Non-goals: broad repository audit, dependency review, refactors.

## Findings

### 1. Unauthenticated callback endpoint accepting attacker-controlled session identity and cost data — Severity: High (exploitable, not local-only)

- Evidence: `/api/cerberus/events` is registered with no auth/signature
  middleware (`internal/httpserver/core.go:178`,
  `s.mux.HandleFunc("/api/cerberus/events", s.handleCerberusCallback)`).
  `handleCerberusCallback` (`internal/httpserver/cerberus_api.go:24-42`)
  only checks the HTTP method, then parses the body and dispatches it via
  `handleCompactCerberusEvent`.
- The `session` and `type` fields of the JSON body are fully
  caller-supplied (`compactCerberusEvent` in
  `internal/httpserver/cerberus_events.go:22-27`) and are used directly to
  look up and mutate state for an existing phase/chat session
  (`db.GetPhaseByCerberusSession`, `storeAndPublishPhaseLog`,
  `applyManagedMessageEndCost` at `internal/httpserver/cerberus_events.go:479-492`).
  For `message_end` events, an attacker-supplied `usage.cost_usd` value is
  added directly to a phase's tracked budget via `db.AddPhaseCost`
  (`internal/httpserver/cerberus_events.go:483-490`) with no bounds check
  (not validated as non-negative or capped), and no correlation secret
  binds the caller to that session.
- This is not confirmed to be local-only: the server listens on
  `addr := fmt.Sprintf(":%d", cfg.ServerPort)` — all interfaces, not
  `127.0.0.1` (`cmd/server/main.go:94,96`). Only the *self-referential*
  callback URL built for Cerberus itself uses `http://localhost:...`
  (`cmd/server/main.go:75`, `internal/httpserver/core.go:104`); that only
  documents the intended caller, it does not restrict the listening socket
  or enforce that requests actually originate from localhost.
- Impact: any network peer able to reach the port can forge events for any
  session string it knows/guesses, inject phase log lines, and inflate or
  (with a negative float) deflate a phase's tracked cost — a data
  integrity issue on the trust boundary between Cerberus and Foundry.

### 2. Unbounded request body read on the callback handler — Severity: Medium (plausible DoS, not directly confirmed exploitable in this codebase)

- Evidence: `raw, err := io.ReadAll(r.Body)`
  (`internal/httpserver/cerberus_api.go:29`) has no
  `http.MaxBytesReader` or equivalent size cap before reading the full
  request body into memory.
- Combined with finding 1 (no auth), a caller that can reach the endpoint
  can send arbitrarily large POST bodies to exhaust server memory. This is
  a plausible resource-exhaustion vector; it was not exploited/tested here
  (out of scope), so it is reported as a design gap rather than a
  demonstrated exploit.

### 3. No issue confirmed: telemetry content size limits are enforced correctly

- Evidence: `internal/telemetry/ingest.go` defines
  `MaxContentBytes = 256 * 1024` and applies `truncateContent`/
  `contentFields` (`internal/telemetry/ingest.go:118-135`) to all
  string content fields (`tool_input`, `tool_result`, message content)
  before storage, with UTF-8-safe truncation and a SHA-256 fingerprint of
  the original content when truncated.
- `validateEvent` (`internal/telemetry/ingest.go:82-105`) rejects unknown
  event types and enforces required fields per event type (e.g.
  `source_session_id`/`origin` for `session_start`, `tool_name` for tool
  events), and explicitly rejects raw `delta` events from being stored.
- No exploitable input-validation gap was found in the ingest layer itself
  for content-size handling or event-type validation. The numeric
  `cost_usd`/token fields are the exception (see Finding 1) — those are
  not range-validated here or upstream, but the string/content handling in
  this file is sound based on direct code inspection.

## Recommendation

Add caller authentication to the Cerberus callback endpoint (e.g. a
shared secret/HMAC signature verified in `handleCerberusCallback`) and
bind non-verifiable numeric fields such as `cost_usd` to a sane range
before applying them to phase budgets. Add a body-size limit
(`http.MaxBytesReader`) to `handleCerberusCallback` as a low-effort
mitigation for Finding 2. These are process/config changes to the
handler wiring and are not included in this audit-only pass.

## Verification (smoke test)

- Finding 1 (unauthenticated callback accepting caller-controlled session/cost
  data) remains supported: `handleCerberusCallback` in
  `internal/httpserver/cerberus_api.go` (lines 24-44) still only checks
  `r.Method`, with no auth/signature check, before dispatching the body; the
  caller-supplied `session`/`type` fields are defined in `compactCerberusEvent`
  in `internal/httpserver/cerberus_events.go` (lines 23-28), and
  `applyManagedMessageEndCost` in `internal/httpserver/cerberus_events.go`
  (lines 479-490) still passes an unvalidated `usage.cost_usd` straight to
  `db.AddPhaseCost` with no bounds check.
- Finding 2 (unbounded request body read) remains supported: `raw, err :=
  io.ReadAll(r.Body)` in `internal/httpserver/cerberus_api.go` (line 29) is
  still called with no `http.MaxBytesReader` or other size cap.
- No changes were made to either finding, to source code, or to any other
  file as part of this verification pass.
