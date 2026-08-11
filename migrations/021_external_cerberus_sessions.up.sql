CREATE TABLE external_cerberus_sessions (
    id            BIGSERIAL PRIMARY KEY,
    session       TEXT NOT NULL UNIQUE,
    repo          TEXT,
    status        TEXT NOT NULL DEFAULT 'active',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
