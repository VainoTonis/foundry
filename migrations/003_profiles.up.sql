-- 003_profiles.sql

CREATE TABLE profiles (
    id            BIGSERIAL   PRIMARY KEY,
    name          TEXT        NOT NULL UNIQUE,
    default_model TEXT        NOT NULL DEFAULT '',
    default_image TEXT        NOT NULL DEFAULT '',
    aws_profile   TEXT        NOT NULL DEFAULT '',
    aws_region    TEXT        NOT NULL DEFAULT '',
    extra_env     JSONB       NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
