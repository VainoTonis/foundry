-- Telemetry receipt ledger: records producer-assigned event identity
-- (producer_id + event_id) and client sequence so duplicate deliveries of
-- the same event can be detected and rejected transactionally before any
-- telemetry side effects are applied. The unique constraint below is the
-- source of truth for duplicate detection.
CREATE TABLE telemetry_receipts (
    id            BIGSERIAL PRIMARY KEY,
    producer_id   TEXT        NOT NULL,
    event_id      TEXT        NOT NULL,
    client_seq    BIGINT      NOT NULL,
    event_type    TEXT        NOT NULL,
    session       TEXT        NOT NULL,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (producer_id, event_id)
);

CREATE INDEX ON telemetry_receipts(session);
CREATE INDEX ON telemetry_receipts(producer_id, client_seq);
