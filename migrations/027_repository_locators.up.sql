-- Expand migration: add remote_url as an alternative repository locator.
-- projects.repo_path keeps its physical name; remote_url is new and nullable.
-- repo_path becomes nullable so rows may be identified by remote_url alone.
-- A check constraint enforces that at least one of the two (trimmed,
-- non-empty) is present. Existing rows are grandfathered in case they
-- somehow violate this (e.g. NULL/blank repo_path already present),
-- so the constraint is NOT VALID and only enforced for new/updated rows
-- until it is explicitly validated.

ALTER TABLE projects
    ADD COLUMN remote_url TEXT;

ALTER TABLE projects
    ALTER COLUMN repo_path DROP NOT NULL;

ALTER TABLE projects
    ADD CONSTRAINT projects_repo_locator_present CHECK (
        (repo_path IS NOT NULL AND length(trim(repo_path)) > 0)
        OR
        (remote_url IS NOT NULL AND length(trim(remote_url)) > 0)
    ) NOT VALID;
