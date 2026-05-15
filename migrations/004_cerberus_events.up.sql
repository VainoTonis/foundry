CREATE TABLE cerberus_events (
    id         BIGSERIAL PRIMARY KEY,
    session    TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload    JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_cerberus_events_session ON cerberus_events(session);
