-- Deterministic source cursors used by the merged sessions worklist.
CREATE INDEX agent_sessions_recent_session_idx
    ON agent_sessions(last_event_at DESC, session DESC);

CREATE INDEX spec_drafts_session_recent_idx
    ON spec_drafts(updated_at DESC, cerberus_session DESC)
    WHERE cerberus_session <> '';

CREATE INDEX external_cerberus_sessions_recent_session_idx
    ON external_cerberus_sessions(last_seen_at DESC, session DESC);

CREATE INDEX phases_cerberus_session_idx
    ON phases(cerberus_session)
    WHERE cerberus_session IS NOT NULL AND cerberus_session <> '';
