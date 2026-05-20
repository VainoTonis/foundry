-- 005_project_memory_repo_path.sql

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS memory_repo_path TEXT NOT NULL DEFAULT '';
