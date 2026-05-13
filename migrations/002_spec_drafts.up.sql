-- 002_spec_drafts.sql

CREATE TABLE spec_drafts (
    id               BIGSERIAL   PRIMARY KEY,
    project_id       BIGINT      REFERENCES projects(id) ON DELETE SET NULL,
    title            TEXT        NOT NULL DEFAULT '',
    cerberus_session TEXT        NOT NULL DEFAULT '',
    messages         JSONB       NOT NULL DEFAULT '[]',
    status           TEXT        NOT NULL DEFAULT 'active'
                                   CHECK (status IN ('active','saved','error')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON spec_drafts(status);
