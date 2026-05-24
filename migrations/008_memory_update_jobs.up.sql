-- 008_memory_update_jobs.sql

CREATE TABLE memory_update_jobs (
    id                BIGSERIAL PRIMARY KEY,
    workflow_id       BIGINT      NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    status            TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected')),
    proposal_markdown TEXT        NOT NULL,
    reviewer_comment  TEXT        NOT NULL DEFAULT '',
    memory_path       TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON memory_update_jobs(workflow_id);
