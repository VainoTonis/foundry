-- 001_init.sql

CREATE TABLE projects (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT        NOT NULL,
    repo_path        TEXT        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE specs (
    id         BIGSERIAL PRIMARY KEY,
    title      TEXT        NOT NULL,
    content    TEXT        NOT NULL,
    track      TEXT        NOT NULL DEFAULT 'poc' CHECK (track IN ('poc', 'polish')),
    status     TEXT        NOT NULL DEFAULT 'dumpster' CHECK (status IN ('dumpster','queued','running','done','failed','paused')),
    project_id BIGINT      NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    tags       JSONB       NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE workflows (
    id              BIGSERIAL PRIMARY KEY,
    spec_id         BIGINT      NOT NULL REFERENCES specs(id) ON DELETE CASCADE,
    track           TEXT        NOT NULL CHECK (track IN ('poc', 'polish')),
    status          TEXT        NOT NULL DEFAULT 'running' CHECK (status IN ('running','done','failed','paused')),
    max_cost_usd    NUMERIC(10,4),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at     TIMESTAMPTZ
);

CREATE TABLE phases (
    id                  BIGSERIAL PRIMARY KEY,
    workflow_id         BIGINT      NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    position            INT         NOT NULL,
    name                TEXT        NOT NULL,
    goal                TEXT        NOT NULL,
    prompt_sent         TEXT,
    status              TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','awaiting_review','done','failed')),
    retry_count         INT         NOT NULL DEFAULT 0,
    timeout_seconds     INT         NOT NULL DEFAULT 1800,
    cerberus_session    TEXT,
    cerberus_commit     TEXT,
    cost_usd            NUMERIC(10,4),
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    review_verdict      TEXT        CHECK (review_verdict IN ('pass','fail') OR review_verdict IS NULL),
    review_notes        TEXT,
    adjusted_prompt     TEXT,
    decision_summary    TEXT,
    decision_rationale  TEXT,
    files_touched       JSONB       NOT NULL DEFAULT '[]'
);

CREATE TABLE phase_logs (
    id       BIGSERIAL PRIMARY KEY,
    phase_id BIGINT      NOT NULL REFERENCES phases(id) ON DELETE CASCADE,
    line     TEXT        NOT NULL,
    ts       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON specs(project_id);
CREATE INDEX ON specs(status);
CREATE INDEX ON workflows(spec_id);
CREATE INDEX ON phases(workflow_id);
CREATE INDEX ON phase_logs(phase_id);
