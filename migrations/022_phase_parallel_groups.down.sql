DROP INDEX IF EXISTS phases_workflow_id_parallel_group_position_idx;
ALTER TABLE phases DROP COLUMN parallel_group;
