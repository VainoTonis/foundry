ALTER TABLE chat_sessions
    ADD COLUMN last_active_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN suspended_at TIMESTAMPTZ;

CREATE INDEX chat_sessions_idle_suspend_idx
    ON chat_sessions (last_active_at)
    WHERE suspended_at IS NULL AND status <> 'streaming';
