CREATE TABLE chat_session_projects (
    session_id  BIGINT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    project_id  BIGINT NOT NULL REFERENCES projects(id)      ON DELETE CASCADE,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (session_id, project_id)
);

CREATE INDEX ON chat_session_projects(session_id);
