-- Expand agent telemetry with producer-neutral semantic metadata. Textual
-- fields use "unknown" rather than constraining producer vocabularies, so
-- newer producers can be ingested without a database migration.

ALTER TABLE agent_sessions
    ADD COLUMN schema_version TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN last_event_at TIMESTAMPTZ,
    ADD COLUMN close_reason TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN start_event_seen BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN end_event_seen BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN parent_source_session_id TEXT;

-- Preserve useful ordering for pre-migration sessions instead of assigning
-- every existing row the migration timestamp.
UPDATE agent_sessions
SET last_event_at = COALESCE(ended_at, started_at)
WHERE last_event_at IS NULL;

ALTER TABLE agent_sessions
    ALTER COLUMN last_event_at SET DEFAULT now(),
    ALTER COLUMN last_event_at SET NOT NULL;

ALTER TABLE agent_turns
    ADD COLUMN model TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN provider TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN thinking_level TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN stop_reason TEXT NOT NULL DEFAULT 'unknown';

ALTER TABLE agent_messages
    ADD COLUMN source_message_id TEXT,
    ADD COLUMN input_source TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN is_final BOOLEAN NOT NULL DEFAULT false;

-- A session can concern multiple repositories. Keep agent_sessions.repository_id
-- as the additive migration's legacy/primary attribution while new producers
-- populate this membership table.
CREATE TABLE agent_session_repositories (
    agent_session_id       BIGINT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    repository_id          BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    attribution_method     TEXT NOT NULL DEFAULT 'unknown',
    attribution_confidence NUMERIC(4,3),
    PRIMARY KEY (agent_session_id, repository_id)
);

CREATE UNIQUE INDEX agent_messages_session_source_message_id_key
    ON agent_messages(agent_session_id, source_message_id)
    WHERE source_message_id IS NOT NULL;

-- Supports stable keyset pagination by most recently observed activity.
CREATE INDEX agent_sessions_recent_idx
    ON agent_sessions(last_event_at DESC, id DESC);

-- The primary key supports session-first lookups; this reverse index supports
-- repository filtering before joining back to the recent-session ordering.
CREATE INDEX agent_session_repositories_repository_session_idx
    ON agent_session_repositories(repository_id, agent_session_id);
