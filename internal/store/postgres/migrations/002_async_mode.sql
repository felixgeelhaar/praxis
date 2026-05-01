ALTER TABLE actions ADD COLUMN mode TEXT NOT NULL DEFAULT 'sync';

CREATE INDEX IF NOT EXISTS idx_actions_async_pending
  ON actions (mode, status, created_at)
  WHERE mode = 'async' AND status = 'validated';
