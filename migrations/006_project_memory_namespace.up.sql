-- 006_project_memory_namespace.sql

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS memory_namespace TEXT NOT NULL DEFAULT '';
