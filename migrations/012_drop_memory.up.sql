DELETE FROM app_settings WHERE key = 'memory_repo_path';

ALTER TABLE phases
    ALTER COLUMN phase_feedback SET DEFAULT '{"result":"","useful_context":[],"problems":[],"confidence":0}'::jsonb;

UPDATE phases
SET phase_feedback = phase_feedback - 'suggested_memory'
WHERE phase_feedback ? 'suggested_memory';

DROP TABLE IF EXISTS memory_update_jobs;

ALTER TABLE projects
    DROP COLUMN IF EXISTS memory_namespace;
