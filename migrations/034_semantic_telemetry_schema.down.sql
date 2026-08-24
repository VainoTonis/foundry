DROP TABLE IF EXISTS agent_session_repositories;

DROP INDEX IF EXISTS agent_sessions_recent_idx;
DROP INDEX IF EXISTS agent_messages_session_source_message_id_key;

ALTER TABLE agent_messages
    DROP COLUMN is_final,
    DROP COLUMN input_source,
    DROP COLUMN source_message_id;

ALTER TABLE agent_turns
    DROP COLUMN stop_reason,
    DROP COLUMN thinking_level,
    DROP COLUMN provider,
    DROP COLUMN model;

ALTER TABLE agent_sessions
    DROP COLUMN parent_source_session_id,
    DROP COLUMN end_event_seen,
    DROP COLUMN start_event_seen,
    DROP COLUMN lifecycle_state,
    DROP COLUMN close_reason,
    DROP COLUMN last_event_at,
    DROP COLUMN schema_version;
