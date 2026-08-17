ALTER TABLE feedback
    ALTER COLUMN body DROP NOT NULL,
    ADD COLUMN dimension TEXT,
    ADD COLUMN target TEXT,
    ADD COLUMN score INTEGER,
    ADD COLUMN tags TEXT[],
    ADD COLUMN evidence TEXT,
    ADD COLUMN impact TEXT,
    ADD COLUMN recommended_action TEXT,
    ADD COLUMN owner TEXT,
    ADD COLUMN status TEXT NOT NULL DEFAULT 'open';

CREATE INDEX idx_feedback_dimension ON feedback (dimension);
CREATE INDEX idx_feedback_status ON feedback (status);
CREATE INDEX idx_feedback_session_id ON feedback (session_id);
