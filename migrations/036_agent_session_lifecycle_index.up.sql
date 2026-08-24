-- Supports bounded recent-session filtering without discarding historical rows.
CREATE INDEX agent_sessions_lifecycle_recent_idx
    ON agent_sessions(lifecycle_state, last_event_at DESC, id DESC);
