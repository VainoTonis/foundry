CREATE TABLE knowledge_feedback (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL,
    note_path  TEXT,
    topic      TEXT,
    evidence   TEXT NOT NULL,
    suggestion TEXT,
    origin     TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'open',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_knowledge_feedback_status ON knowledge_feedback (status);
CREATE INDEX idx_knowledge_feedback_note_path ON knowledge_feedback (note_path);
