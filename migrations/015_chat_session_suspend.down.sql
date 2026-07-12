DROP INDEX IF EXISTS chat_sessions_idle_suspend_idx;

ALTER TABLE chat_sessions
    DROP COLUMN suspended_at,
    DROP COLUMN last_active_at;
