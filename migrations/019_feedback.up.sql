CREATE TABLE feedback (
    id         BIGSERIAL PRIMARY KEY,
    body       TEXT NOT NULL,
    model      TEXT,
    session_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);