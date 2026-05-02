-- Phase 6 t-changelog-schema: persistent capability changelog.

CREATE TABLE IF NOT EXISTS capability_history (
    id                    TEXT PRIMARY KEY,
    capability_name       TEXT NOT NULL,
    recorded_at           TIMESTAMPTZ NOT NULL,
    prev_input_version    TEXT NOT NULL DEFAULT '',
    prev_output_version   TEXT NOT NULL DEFAULT '',
    next_input_version    TEXT NOT NULL DEFAULT '',
    next_output_version   TEXT NOT NULL DEFAULT '',
    issues                JSONB NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_capability_history_name
    ON capability_history (capability_name, recorded_at DESC);
