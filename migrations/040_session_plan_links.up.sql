-- 1. session_plan_links: records how an agent session got attributed to a
-- plan (and optionally a specific plan step), and with what confidence.
-- plan_steps needs a UNIQUE(plan_id, id) constraint so the FK below can be
-- a composite (plan_id, plan_step_id) reference: plan_step_id alone is
-- already unique via its primary key, but a composite FK lets Postgres
-- verify that a linked step actually belongs to the linked plan, not just
-- that the step id exists somewhere.
ALTER TABLE plan_steps ADD CONSTRAINT plan_steps_plan_id_id_key UNIQUE (plan_id, id);

CREATE TABLE session_plan_links (
    id               BIGSERIAL PRIMARY KEY,
    agent_session_id BIGINT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    plan_id          BIGINT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    plan_step_id     BIGINT,
    method           TEXT NOT NULL,
    confidence       NUMERIC(4,3),
    note             TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT session_plan_links_method_check
        CHECK (method IN ('system_derived', 'explicit', 'api_inferred', 'heuristic')),
    -- MATCH SIMPLE (the default, spelled out here for clarity) means this
    -- composite FK is only checked when plan_step_id is non-NULL: with
    -- MATCH SIMPLE, if any referencing column is NULL the whole FK is
    -- considered satisfied and skipped. That is exactly what a link with
    -- no specific step (plan_step_id IS NULL) needs, since plan_id alone
    -- is already independently enforced by the single-column FK above.
    -- MATCH FULL would instead require every column to be NULL together,
    -- which is irrelevant here since plan_id is NOT NULL and always set.
    --
    -- The delete action is qualified as "ON DELETE SET NULL (plan_step_id)"
    -- -- the PostgreSQL 15+ column-specific form -- rather than a plain
    -- "ON DELETE SET NULL". An unqualified ON DELETE SET NULL on a
    -- composite FK sets *every* referencing column (plan_id included) to
    -- NULL when the referenced plan_steps row is deleted, but plan_id is
    -- NOT NULL here and must never be touched by this FK's delete action;
    -- only plan_step_id should be cleared, leaving the link attached to
    -- its plan.
    CONSTRAINT session_plan_links_plan_step_fkey
        FOREIGN KEY (plan_id, plan_step_id) REFERENCES plan_steps (plan_id, id)
        MATCH SIMPLE ON DELETE SET NULL (plan_step_id)
);

CREATE INDEX ON session_plan_links(agent_session_id);
CREATE INDEX ON session_plan_links(plan_id);
CREATE INDEX ON session_plan_links(plan_step_id);

-- 2. Backfill agent_sessions.parent_source_session_id from the legacy
-- parent_session column before constraining it with a foreign key.
-- parent_session has three known shapes in production:
--   - 'pi:<uuid>': strip the 'pi:' prefix and use the uuid, but only when
--     that uuid actually matches an existing source_session_id (otherwise
--     leave NULL rather than set a value that would fail the FK below).
--   - raw filesystem paths ending in '.jsonl': cannot be resolved to a
--     session id; left NULL, not parsed.
--   - anything else that does not resolve to a known source_session_id:
--     left NULL.
UPDATE agent_sessions AS s
SET parent_source_session_id = substring(s.parent_session FROM 4)
WHERE s.parent_source_session_id IS NULL
  AND s.parent_session LIKE 'pi:%'
  AND EXISTS (
      SELECT 1 FROM agent_sessions AS t
      WHERE t.source_session_id = substring(s.parent_session FROM 4)
  );

CREATE INDEX ON agent_sessions(parent_source_session_id);

-- Added NOT VALID then validated separately so the intent -- add the
-- constraint now, confirm the backfilled data actually satisfies it --
-- is explicit and does not require a long-held lock while scanning the
-- whole table.
ALTER TABLE agent_sessions
    ADD CONSTRAINT agent_sessions_parent_source_session_id_fkey
    FOREIGN KEY (parent_source_session_id) REFERENCES agent_sessions (source_session_id)
    ON DELETE SET NULL NOT VALID;

ALTER TABLE agent_sessions
    VALIDATE CONSTRAINT agent_sessions_parent_source_session_id_fkey;

-- 3. feedback.agent_session_id: best-effort attribution of a feedback row
-- to the agent session it was submitted from, matched by feedback's
-- existing session_id column against agent_sessions.source_session_id.
-- Rows that do not match stay NULL permanently -- this is expected, not
-- an error, since feedback.session_id predates structured session
-- telemetry and not every historical value is a resolvable session.
ALTER TABLE feedback
    ADD COLUMN agent_session_id BIGINT REFERENCES agent_sessions(id) ON DELETE SET NULL;

UPDATE feedback AS f
SET agent_session_id = a.id
FROM agent_sessions AS a
WHERE f.session_id IS NOT NULL
  AND a.source_session_id = f.session_id;

CREATE INDEX ON feedback(agent_session_id);

-- 4. A single shared status vocabulary for plans and plan_steps.
-- plan_steps historically also used 'in_progress'; fold that into
-- 'running' before adding the CHECK. This UPDATE is idempotent and a
-- no-op if no rows are at 'in_progress'.
UPDATE plan_steps SET status = 'running' WHERE status = 'in_progress';

ALTER TABLE plans
    ADD CONSTRAINT plans_status_check
    CHECK (status IN ('pending', 'running', 'done', 'failed')) NOT VALID;
ALTER TABLE plans VALIDATE CONSTRAINT plans_status_check;

ALTER TABLE plan_steps
    ADD CONSTRAINT plan_steps_status_check
    CHECK (status IN ('pending', 'running', 'done', 'failed')) NOT VALID;
ALTER TABLE plan_steps VALIDATE CONSTRAINT plan_steps_status_check;

-- 5. Shared updated_at maintenance trigger. Neither plans nor plan_steps
-- currently has a trigger of any kind; application code sets
-- updated_at = NOW() explicitly on every UPDATE today, and this trigger
-- is added as a database-level safety net so that guarantee holds even
-- for updates issued outside that code path.
CREATE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER plans_set_updated_at
    BEFORE UPDATE ON plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER plan_steps_set_updated_at
    BEFORE UPDATE ON plan_steps
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 6. phases_lifecycle_check only constrains the two truly terminal
-- statuses. It does NOT mirror plan_reviews_lifecycle_check's per-status
-- shape for every status: the actual write path in
-- internal/workflow/phase.go records review outcome fields (finished_at,
-- review_verdict, cerberus_commit) in an UpdatePhase call that happens
-- before a later, separate UpdatePhase call that sets the terminal
-- status, so a valid row can legitimately sit at status='awaiting_review'
-- with finished_at already set. Likewise, status='pending' is reused when
-- a phase is reset for a retry, and the prior attempt's started_at is not
-- guaranteed to be cleared, so a legitimately-retrying pending row can
-- have started_at set. pending, running, and awaiting_review are
-- therefore left unconstrained on started_at/finished_at/review_verdict/
-- cerberus_commit; only the terminal statuses done and failed impose a
-- shape: done phases must have started, finished, and been reviewed as a
-- pass with a commit recorded; failed phases must have finished_at set.
ALTER TABLE phases ADD CONSTRAINT phases_lifecycle_check CHECK (
    (status IN ('pending', 'running', 'awaiting_review')) OR
    (status = 'done'             AND started_at IS NOT NULL AND finished_at IS NOT NULL AND review_verdict = 'pass' AND cerberus_commit IS NOT NULL) OR
    (status = 'failed'           AND finished_at IS NOT NULL)
) NOT VALID;
ALTER TABLE phases VALIDATE CONSTRAINT phases_lifecycle_check;
