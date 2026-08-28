-- Final physical schema rename for the Project -> Repository terminology
-- cutover (plans 31-34 already moved every Go caller to Repository /
-- repository_id wording externally; this migration catches up the
-- physical names to match).
--
--   projects                -> repositories
--   projects.repo_path      -> repositories.local_path
--   specs.project_id             -> specs.repository_id
--   spec_drafts.project_id       -> spec_drafts.repository_id
--   chat_session_projects        -> chat_session_repositories
--     .project_id                 -> .repository_id
--   plan_repositories.project_id -> plan_repositories.repository_id
--   feedback_repositories.project_id -> feedback_repositories.repository_id
--
-- Before any rename, locatorless projects rows (no repo_path/remote_url)
-- that are not referenced by any of the tables above are deleted. If a
-- locatorless row IS referenced, the migration aborts rather than
-- deleting or renaming anything, since that row would violate the
-- locator-presence invariant once validated below.

-- nucleus-hooks:ignore-next: data-only cleanup of unreferenced locatorless projects rows plus a read-only referenced-row count check ahead of the projects->repositories rename; no schema/ER change happens in this block, the DDL renames follow separately below
DO $$
DECLARE
    unenforceable_count BIGINT;
BEGIN
    -- Remove locatorless projects that nothing references.
    DELETE FROM projects p
    WHERE (p.repo_path IS NULL OR length(trim(p.repo_path)) = 0)
      AND (p.remote_url IS NULL OR length(trim(p.remote_url)) = 0)
      AND NOT EXISTS (SELECT 1 FROM specs s WHERE s.project_id = p.id)
      AND NOT EXISTS (SELECT 1 FROM spec_drafts d WHERE d.project_id = p.id)
      AND NOT EXISTS (SELECT 1 FROM chat_session_projects csp WHERE csp.project_id = p.id)
      AND NOT EXISTS (SELECT 1 FROM plan_repositories pr WHERE pr.project_id = p.id)
      AND NOT EXISTS (SELECT 1 FROM feedback_repositories fr WHERE fr.project_id = p.id);

    -- Any locatorless project still present at this point must be
    -- referenced (unreferenced ones were just deleted above): abort
    -- instead of silently renaming/validating over data that would
    -- violate the locator-presence invariant.
    SELECT COUNT(*) INTO unenforceable_count
    FROM projects p
    WHERE (p.repo_path IS NULL OR length(trim(p.repo_path)) = 0)
      AND (p.remote_url IS NULL OR length(trim(p.remote_url)) = 0);

    IF unenforceable_count > 0 THEN
        RAISE EXCEPTION 'migration 030: % referenced project(s) have neither repo_path nor remote_url; cannot proceed', unenforceable_count;
    END IF;
END $$;

-- The locator-presence check added NOT VALID in 027 can now be fully
-- enforced: every remaining row satisfies it (locatorless rows were
-- either unreferenced and deleted above, or the migration aborted).
ALTER TABLE projects VALIDATE CONSTRAINT projects_repo_locator_present;

-- projects -> repositories, repo_path -> local_path.
ALTER TABLE projects RENAME TO repositories;
ALTER TABLE repositories RENAME CONSTRAINT projects_pkey TO repositories_pkey;
ALTER TABLE repositories RENAME CONSTRAINT projects_repo_locator_present TO repositories_locator_present;
ALTER TABLE repositories RENAME COLUMN repo_path TO local_path;

-- specs.project_id -> specs.repository_id
ALTER TABLE specs RENAME COLUMN project_id TO repository_id;
ALTER TABLE specs RENAME CONSTRAINT specs_project_id_fkey TO specs_repository_id_fkey;
ALTER INDEX specs_project_id_idx RENAME TO specs_repository_id_idx;

-- spec_drafts.project_id -> spec_drafts.repository_id
ALTER TABLE spec_drafts RENAME COLUMN project_id TO repository_id;
ALTER TABLE spec_drafts RENAME CONSTRAINT spec_drafts_project_id_fkey TO spec_drafts_repository_id_fkey;

-- chat_session_projects -> chat_session_repositories
ALTER TABLE chat_session_projects RENAME TO chat_session_repositories;
ALTER TABLE chat_session_repositories RENAME COLUMN project_id TO repository_id;
ALTER TABLE chat_session_repositories RENAME CONSTRAINT chat_session_projects_pkey TO chat_session_repositories_pkey;
ALTER TABLE chat_session_repositories RENAME CONSTRAINT chat_session_projects_session_id_fkey TO chat_session_repositories_session_id_fkey;
ALTER TABLE chat_session_repositories RENAME CONSTRAINT chat_session_projects_project_id_fkey TO chat_session_repositories_repository_id_fkey;
ALTER INDEX chat_session_projects_session_id_idx RENAME TO chat_session_repositories_session_id_idx;

-- plan_repositories.project_id -> plan_repositories.repository_id
ALTER TABLE plan_repositories RENAME COLUMN project_id TO repository_id;
ALTER TABLE plan_repositories RENAME CONSTRAINT plan_repositories_project_id_fkey TO plan_repositories_repository_id_fkey;
ALTER TABLE plan_repositories RENAME CONSTRAINT plan_repositories_plan_id_project_id_key TO plan_repositories_plan_id_repository_id_key;
ALTER INDEX plan_repositories_project_id_idx RENAME TO plan_repositories_repository_id_idx;

-- feedback_repositories.project_id -> feedback_repositories.repository_id
ALTER TABLE feedback_repositories RENAME COLUMN project_id TO repository_id;
ALTER TABLE feedback_repositories RENAME CONSTRAINT feedback_repositories_project_id_fkey TO feedback_repositories_repository_id_fkey;
ALTER INDEX feedback_repositories_project_id_idx RENAME TO feedback_repositories_repository_id_idx;
