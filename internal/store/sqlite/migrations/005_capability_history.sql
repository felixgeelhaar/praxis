-- Phase 6 t-changelog-schema: persistent capability changelog.
-- Each row records one breaking-change re-registration of a capability,
-- captured by the registry's compat checker. /v1/capabilities/{name}/changelog
-- reads from this table so the audit-of-schema-drift survives restarts.

CREATE TABLE IF NOT EXISTS capability_history (
    id                    TEXT PRIMARY KEY,
    capability_name       TEXT NOT NULL,
    recorded_at           TEXT NOT NULL,
    prev_input_version    TEXT NOT NULL DEFAULT '',
    prev_output_version   TEXT NOT NULL DEFAULT '',
    next_input_version    TEXT NOT NULL DEFAULT '',
    next_output_version   TEXT NOT NULL DEFAULT '',
    issues                TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_capability_history_name
    ON capability_history (capability_name, recorded_at DESC);
