CREATE TABLE IF NOT EXISTS capabilities (
    name           TEXT PRIMARY KEY,
    description    TEXT,
    input_schema   JSONB NOT NULL,
    output_schema  JSONB NOT NULL,
    permissions    TEXT[] NOT NULL DEFAULT '{}',
    simulatable    BOOLEAN NOT NULL DEFAULT FALSE,
    idempotent     BOOLEAN NOT NULL DEFAULT FALSE,
    registered_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS actions (
    id              TEXT PRIMARY KEY,
    capability      TEXT NOT NULL REFERENCES capabilities(name),
    payload         JSONB NOT NULL,
    caller_type     TEXT NOT NULL,
    caller_id       TEXT NOT NULL,
    caller_name     TEXT NOT NULL DEFAULT '',
    scope           TEXT[] NOT NULL DEFAULT '{}',
    idempotency_key TEXT NOT NULL,
    status          TEXT NOT NULL,
    result          JSONB,
    error           JSONB,
    policy_decision JSONB,
    executed_at     TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_actions_idempotency ON actions (idempotency_key);
CREATE INDEX IF NOT EXISTS idx_actions_capability ON actions (capability, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_actions_caller ON actions (caller_type, caller_id, created_at DESC);

CREATE TABLE IF NOT EXISTS audit_events (
    id            TEXT PRIMARY KEY,
    action_id     TEXT NOT NULL,
    kind          TEXT NOT NULL,
    capability    TEXT NOT NULL DEFAULT '',
    caller_type   TEXT NOT NULL DEFAULT '',
    detail        JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_action     ON audit_events (action_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_kind       ON audit_events (kind, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_capability ON audit_events (capability, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_caller     ON audit_events (caller_type, created_at DESC);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key         TEXT PRIMARY KEY,
    action_id   TEXT NOT NULL,
    result      JSONB NOT NULL,
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_idempotency_expires ON idempotency_keys (expires_at);

CREATE TABLE IF NOT EXISTS policy_rules (
    id           TEXT PRIMARY KEY,
    capability   TEXT,
    caller_type  TEXT,
    scope        TEXT[],
    decision     TEXT NOT NULL,
    reason       TEXT,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS outcome_outbox (
    id            TEXT PRIMARY KEY,
    action_id     TEXT NOT NULL,
    payload       JSONB NOT NULL,
    attempts      INTEGER NOT NULL DEFAULT 0,
    next_attempt  TIMESTAMPTZ NOT NULL,
    delivered_at  TIMESTAMPTZ,
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outcome_outbox (delivered_at, next_attempt);
