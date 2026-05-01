-- Phase 3 M3.3: per-tenant audit retention + access controls.
-- Stamps every audit row with its caller's org/team so retention sweeps
-- and search queries can be tenant-scoped without joining back to the
-- action row (which may have been purged).

ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS org_id  TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS team_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_audit_org      ON audit_events (org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_org_kind ON audit_events (org_id, kind, created_at DESC);
