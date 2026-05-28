-- 011_app_settings.sql

CREATE TABLE app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
