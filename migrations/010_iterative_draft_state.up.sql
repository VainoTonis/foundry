-- 010_iterative_draft_state.sql

ALTER TABLE spec_drafts
    ADD COLUMN IF NOT EXISTS original_intent TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS current_decision_needed TEXT NOT NULL DEFAULT '';

ALTER TABLE spec_drafts
    DROP CONSTRAINT IF EXISTS spec_drafts_status_check;

ALTER TABLE spec_drafts
    ADD CONSTRAINT spec_drafts_status_check
    CHECK (status IN ('active','ready_to_freeze','frozen','abandoned','error'));

CREATE TABLE draft_attempts (
    id               BIGSERIAL   PRIMARY KEY,
    draft_id         BIGINT      NOT NULL REFERENCES spec_drafts(id) ON DELETE CASCADE,
    attempt_number   INTEGER     NOT NULL DEFAULT 1,
    cerberus_session TEXT        NOT NULL DEFAULT '',
    status           TEXT        NOT NULL DEFAULT 'active',
    prompt           TEXT        NOT NULL DEFAULT '',
    result           TEXT        NOT NULL DEFAULT '',
    error_message    TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (draft_id, attempt_number)
);

CREATE INDEX ON draft_attempts(draft_id);
CREATE INDEX ON draft_attempts(status);
CREATE INDEX ON draft_attempts(cerberus_session);

CREATE TABLE draft_attempt_events (
    id         BIGSERIAL   PRIMARY KEY,
    draft_id   BIGINT      NOT NULL REFERENCES spec_drafts(id) ON DELETE CASCADE,
    attempt_id BIGINT      REFERENCES draft_attempts(id) ON DELETE CASCADE,
    event_type TEXT        NOT NULL DEFAULT '',
    payload    JSONB       NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON draft_attempt_events(draft_id, id);
CREATE INDEX ON draft_attempt_events(attempt_id, id);
CREATE INDEX ON draft_attempt_events(event_type);

CREATE TABLE draft_decisions (
    id             BIGSERIAL   PRIMARY KEY,
    draft_id       BIGINT      NOT NULL REFERENCES spec_drafts(id) ON DELETE CASCADE,
    prompt         TEXT        NOT NULL DEFAULT '',
    options        JSONB       NOT NULL DEFAULT '[]',
    decision       TEXT        NOT NULL DEFAULT '',
    rationale      TEXT        NOT NULL DEFAULT '',
    status         TEXT        NOT NULL DEFAULT 'pending'
                               CHECK (status IN ('pending','answered','dismissed')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON draft_decisions(draft_id, id);
CREATE INDEX ON draft_decisions(status);
