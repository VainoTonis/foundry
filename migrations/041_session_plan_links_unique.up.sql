-- session_plan_links had no uniqueness constraint at all, so a redelivered
-- session-start event (or any other repeated linking call) could create a
-- second, duplicate row for the same session/plan(/step)/method. A plain
-- multi-column UNIQUE constraint cannot cover this correctly because
-- plan_step_id is nullable and PostgreSQL treats every NULL as distinct
-- from every other NULL under a normal UNIQUE constraint, so two partial
-- unique indexes are used instead: one for plan-level links
-- (plan_step_id IS NULL, which is what system_derived links always are
-- today) and one for step-level links (plan_step_id IS NOT NULL, e.g.
-- from an explicit attach).
CREATE UNIQUE INDEX session_plan_links_plan_level_unique_idx
    ON session_plan_links (agent_session_id, plan_id, method)
    WHERE plan_step_id IS NULL;

CREATE UNIQUE INDEX session_plan_links_step_level_unique_idx
    ON session_plan_links (agent_session_id, plan_id, plan_step_id, method)
    WHERE plan_step_id IS NOT NULL;
