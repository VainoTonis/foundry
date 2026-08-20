-- Reverse every rename performed by 030_repository_naming.up.sql, back to
-- the projects/repo_path/project_id/chat_session_projects physical names.
-- Pure renames: no data is dropped or lost.

-- feedback_repositories.repository_id -> feedback_repositories.project_id
ALTER INDEX feedback_repositories_repository_id_idx RENAME TO feedback_repositories_project_id_idx;
ALTER TABLE feedback_repositories RENAME CONSTRAINT feedback_repositories_repository_id_fkey TO feedback_repositories_project_id_fkey;
ALTER TABLE feedback_repositories RENAME COLUMN repository_id TO project_id;

-- plan_repositories.repository_id -> plan_repositories.project_id
ALTER INDEX plan_repositories_repository_id_idx RENAME TO plan_repositories_project_id_idx;
ALTER TABLE plan_repositories RENAME CONSTRAINT plan_repositories_plan_id_repository_id_key TO plan_repositories_plan_id_project_id_key;
ALTER TABLE plan_repositories RENAME CONSTRAINT plan_repositories_repository_id_fkey TO plan_repositories_project_id_fkey;
ALTER TABLE plan_repositories RENAME COLUMN repository_id TO project_id;

-- chat_session_repositories -> chat_session_projects
ALTER INDEX chat_session_repositories_session_id_idx RENAME TO chat_session_projects_session_id_idx;
ALTER TABLE chat_session_repositories RENAME CONSTRAINT chat_session_repositories_repository_id_fkey TO chat_session_projects_project_id_fkey;
ALTER TABLE chat_session_repositories RENAME CONSTRAINT chat_session_repositories_session_id_fkey TO chat_session_projects_session_id_fkey;
ALTER TABLE chat_session_repositories RENAME CONSTRAINT chat_session_repositories_pkey TO chat_session_projects_pkey;
ALTER TABLE chat_session_repositories RENAME COLUMN repository_id TO project_id;
ALTER TABLE chat_session_repositories RENAME TO chat_session_projects;

-- spec_drafts.repository_id -> spec_drafts.project_id
ALTER TABLE spec_drafts RENAME CONSTRAINT spec_drafts_repository_id_fkey TO spec_drafts_project_id_fkey;
ALTER TABLE spec_drafts RENAME COLUMN repository_id TO project_id;

-- specs.repository_id -> specs.project_id
ALTER INDEX specs_repository_id_idx RENAME TO specs_project_id_idx;
ALTER TABLE specs RENAME CONSTRAINT specs_repository_id_fkey TO specs_project_id_fkey;
ALTER TABLE specs RENAME COLUMN repository_id TO project_id;

-- repositories -> projects, local_path -> repo_path
ALTER TABLE repositories RENAME COLUMN local_path TO repo_path;
ALTER TABLE repositories RENAME CONSTRAINT repositories_locator_present TO projects_repo_locator_present;
ALTER TABLE repositories RENAME CONSTRAINT repositories_pkey TO projects_pkey;
ALTER TABLE repositories RENAME TO projects;
