CREATE TABLE plans (
    id         BIGSERIAL PRIMARY KEY,
    repo_name  TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    summary    TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON plans(repo_name);
CREATE INDEX ON plans(status);

CREATE TABLE plan_steps (
    id        BIGSERIAL PRIMARY KEY,
    plan_id   BIGINT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    position  INTEGER NOT NULL DEFAULT 0,
    text      TEXT NOT NULL DEFAULT '',
    status    TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON plan_steps(plan_id);
CREATE INDEX ON plan_steps(status);
