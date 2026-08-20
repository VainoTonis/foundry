-- Add scope_status to feedback and introduce feedback_repositories, an
-- unordered many-to-many membership table linking feedback rows to the
-- projects they concern. Unlike plan_repositories there is no notion of a
-- primary repository for feedback, so no position column is needed.

ALTER TABLE feedback
    ADD COLUMN scope_status TEXT NOT NULL DEFAULT 'legacy_unscoped'
        CHECK (scope_status IN ('linked', 'legacy_unscoped'));

CREATE TABLE feedback_repositories (
    feedback_id BIGINT NOT NULL REFERENCES feedback(id) ON DELETE CASCADE,
    project_id  BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    PRIMARY KEY (feedback_id, project_id)
);

CREATE INDEX ON feedback_repositories(project_id);
