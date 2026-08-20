-- Rollback is destructive for any project that only has a remote_url
-- (repo_path NULL/blank), since the old schema requires repo_path NOT NULL.
-- Refuse the rollback in that case instead of silently dropping data.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM projects
        WHERE repo_path IS NULL OR length(trim(repo_path)) = 0
    ) THEN
        RAISE EXCEPTION 'cannot roll back 027_repository_locators: projects exist with no usable repo_path (remote_url-only rows)';
    END IF;
END $$;

ALTER TABLE projects
    DROP CONSTRAINT projects_repo_locator_present;

ALTER TABLE projects
    ALTER COLUMN repo_path SET NOT NULL;

ALTER TABLE projects
    DROP COLUMN remote_url;
