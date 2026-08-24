DROP INDEX IF EXISTS agent_messages_session_turn_index_idx;
DROP INDEX IF EXISTS agent_turns_session_turn_index_idx;

ALTER TABLE agent_messages
    DROP COLUMN turn_index;

ALTER TABLE agent_turns
    DROP COLUMN source_message_id,
    DROP COLUMN turn_index;
