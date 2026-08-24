ALTER TABLE agent_messages
    DROP COLUMN content_redacted;

ALTER TABLE agent_tool_calls
    DROP COLUMN tool_result_omitted,
    DROP COLUMN tool_result_redacted,
    DROP COLUMN tool_input_omitted,
    DROP COLUMN tool_input_redacted;
