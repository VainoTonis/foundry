-- Correlate assistant usage turns with their persisted message evidence.
-- Columns remain nullable for telemetry produced before turn lifecycle and
-- message identity were available.

ALTER TABLE agent_turns
    ADD COLUMN turn_index BIGINT,
    ADD COLUMN source_message_id TEXT;

ALTER TABLE agent_messages
    ADD COLUMN turn_index BIGINT;

CREATE INDEX agent_turns_session_turn_index_idx
    ON agent_turns(agent_session_id, turn_index)
    WHERE turn_index IS NOT NULL;

CREATE INDEX agent_messages_session_turn_index_idx
    ON agent_messages(agent_session_id, turn_index)
    WHERE turn_index IS NOT NULL;
