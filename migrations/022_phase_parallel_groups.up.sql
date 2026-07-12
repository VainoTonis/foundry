ALTER TABLE phases ADD COLUMN parallel_group INTEGER;
CREATE INDEX ON phases(workflow_id, parallel_group, position);
