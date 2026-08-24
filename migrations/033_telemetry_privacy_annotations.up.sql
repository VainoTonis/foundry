ALTER TABLE agent_tool_calls
    ADD COLUMN tool_input_redacted BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN tool_input_omitted BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN tool_result_redacted BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN tool_result_omitted BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE agent_messages
    ADD COLUMN content_redacted BOOLEAN NOT NULL DEFAULT false;
