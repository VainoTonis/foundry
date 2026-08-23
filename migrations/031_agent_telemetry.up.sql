CREATE TABLE agent_sessions (
    id                  BIGSERIAL PRIMARY KEY,
    session             TEXT        NOT NULL UNIQUE,
    source_session_id   TEXT        NOT NULL UNIQUE,
    kind                TEXT        NOT NULL DEFAULT 'unknown',
    origin              TEXT        NOT NULL,
    repository_id       BIGINT      REFERENCES repositories(id) ON DELETE SET NULL,
    phase_id            BIGINT      REFERENCES phases(id) ON DELETE SET NULL,
    repo_path           TEXT,
    model               TEXT,
    parent_session      TEXT,
    started_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at            TIMESTAMPTZ,
    input_tokens        BIGINT      NOT NULL DEFAULT 0,
    output_tokens       BIGINT      NOT NULL DEFAULT 0,
    cache_read_tokens   BIGINT      NOT NULL DEFAULT 0,
    cache_write_tokens  BIGINT      NOT NULL DEFAULT 0,
    cost_usd            NUMERIC(12,6) NOT NULL DEFAULT 0,
    tool_call_count     BIGINT      NOT NULL DEFAULT 0,
    turn_count          BIGINT      NOT NULL DEFAULT 0,
    next_seq            BIGINT      NOT NULL DEFAULT 0
);

CREATE TABLE agent_turns (
    id                  BIGSERIAL PRIMARY KEY,
    agent_session_id    BIGINT      NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    seq                 BIGINT      NOT NULL,
    input_tokens        BIGINT      NOT NULL DEFAULT 0,
    output_tokens       BIGINT      NOT NULL DEFAULT 0,
    cache_read_tokens   BIGINT      NOT NULL DEFAULT 0,
    cache_write_tokens  BIGINT      NOT NULL DEFAULT 0,
    cost_usd            NUMERIC(12,6) NOT NULL DEFAULT 0,
    ts                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_session_id, seq)
);

CREATE TABLE agent_tool_calls (
    id                        BIGSERIAL PRIMARY KEY,
    agent_session_id          BIGINT      NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    seq                       BIGINT      NOT NULL,
    result_seq                BIGINT,
    tool_call_id              TEXT,
    tool_name                 TEXT        NOT NULL,
    tool_input                TEXT,
    tool_input_truncated      BOOLEAN     NOT NULL DEFAULT false,
    tool_input_sha256         TEXT,
    tool_input_original_bytes BIGINT,
    tool_result                TEXT,
    tool_result_truncated       BOOLEAN     NOT NULL DEFAULT false,
    tool_result_sha256          TEXT,
    tool_result_original_bytes  BIGINT,
    is_error                  BOOLEAN,
    duration_ms               BIGINT,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at               TIMESTAMPTZ,
    UNIQUE (agent_session_id, seq)
);

CREATE UNIQUE INDEX agent_tool_calls_session_result_seq_key
    ON agent_tool_calls(agent_session_id, result_seq)
    WHERE result_seq IS NOT NULL;

CREATE TABLE agent_messages (
    id                  BIGSERIAL PRIMARY KEY,
    agent_session_id    BIGINT      NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    seq                 BIGINT      NOT NULL,
    role                    TEXT        NOT NULL,
    content                 TEXT,
    content_truncated       BOOLEAN     NOT NULL DEFAULT false,
    content_sha256          TEXT,
    content_original_bytes  BIGINT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_session_id, seq)
);

CREATE INDEX ON agent_sessions(repository_id);
CREATE INDEX ON agent_sessions(phase_id);
CREATE INDEX ON agent_sessions(parent_session);
CREATE INDEX ON agent_turns(agent_session_id);
CREATE INDEX ON agent_tool_calls(agent_session_id);
CREATE INDEX ON agent_tool_calls(tool_name);
CREATE INDEX ON agent_messages(agent_session_id);
