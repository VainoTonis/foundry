ALTER TABLE plans ADD COLUMN project_id BIGINT REFERENCES projects(id) ON DELETE RESTRICT;
ALTER TABLE plans ADD COLUMN content TEXT NOT NULL DEFAULT '';

-- Preserve old CLI-created plans where the repository name identifies a project.
UPDATE plans p
SET project_id = pr.id
FROM projects pr
WHERE p.project_id IS NULL
  AND (pr.name = p.repo_name OR pr.repo_path = p.repo_name);

CREATE INDEX ON plans(project_id);

CREATE TABLE plan_workflows (
    plan_id BIGINT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    workflow_id BIGINT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    PRIMARY KEY (plan_id, workflow_id)
);
CREATE INDEX ON plan_workflows(workflow_id);
