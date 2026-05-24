-- 007_drop_project_memory_repo_path.sql

ALTER TABLE projects
    DROP COLUMN IF EXISTS memory_repo_path;
