ALTER TABLE phases DROP CONSTRAINT phases_lifecycle_check;

DROP TRIGGER IF EXISTS plan_steps_set_updated_at ON plan_steps;
DROP TRIGGER IF EXISTS plans_set_updated_at ON plans;
DROP FUNCTION IF EXISTS set_updated_at();

ALTER TABLE plan_steps DROP CONSTRAINT plan_steps_status_check;
ALTER TABLE plans DROP CONSTRAINT plans_status_check;

DROP INDEX IF EXISTS feedback_agent_session_id_idx;
ALTER TABLE feedback DROP COLUMN agent_session_id;

ALTER TABLE agent_sessions DROP CONSTRAINT agent_sessions_parent_source_session_id_fkey;
DROP INDEX IF EXISTS agent_sessions_parent_source_session_id_idx;

DROP TABLE session_plan_links;

ALTER TABLE plan_steps DROP CONSTRAINT plan_steps_plan_id_id_key;
