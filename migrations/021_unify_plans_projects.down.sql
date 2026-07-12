DROP TABLE IF EXISTS plan_workflows;
DROP INDEX IF EXISTS plans_project_id_idx;
ALTER TABLE plans DROP COLUMN content;
ALTER TABLE plans DROP COLUMN project_id;
