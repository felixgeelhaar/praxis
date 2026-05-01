-- Phase 5 schema versioning: track each capability's input/output
-- schema version so the compatibility checker (internal/schema) can
-- detect breaking changes between reloads. Default '1' so legacy rows
-- look like fresh v1 capabilities and the checker has a defined
-- baseline rather than NULL semantics.

ALTER TABLE capabilities ADD COLUMN IF NOT EXISTS input_schema_version  TEXT NOT NULL DEFAULT '1';
ALTER TABLE capabilities ADD COLUMN IF NOT EXISTS output_schema_version TEXT NOT NULL DEFAULT '1';
