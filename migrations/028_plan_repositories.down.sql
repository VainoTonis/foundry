-- Restore plans.repo_name/project_id single-owner columns, backfilled
-- from projects.name and the position-0 plan_repositories row.
-- Project rows created by the up migration are intentionally left in
-- place: they are harmless extra registry entries.

ALTER TABLE plans ADD COLUMN repo_name TEXT;
ALTER TABLE plans ADD COLUMN project_id BIGINT REFERENCES projects(id) ON DELETE RESTRICT;

UPDATE plans p
SET project_id = pr.project_id,
    repo_name  = proj.name
FROM plan_repositories pr
JOIN projects proj ON proj.id = pr.project_id
WHERE pr.plan_id = p.id
  AND pr.position = 0;

ALTER TABLE plans ALTER COLUMN repo_name SET NOT NULL;

CREATE INDEX ON plans(repo_name);
CREATE INDEX ON plans(project_id);

DROP TABLE IF EXISTS plan_repositories;
