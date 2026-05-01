-- name: UpsertCapability :exec
INSERT INTO capabilities (name, description, input_schema, output_schema, permissions, simulatable, idempotent, registered_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
    description   = excluded.description,
    input_schema  = excluded.input_schema,
    output_schema = excluded.output_schema,
    permissions   = excluded.permissions,
    simulatable   = excluded.simulatable,
    idempotent    = excluded.idempotent,
    registered_at = excluded.registered_at;

-- name: GetCapability :one
SELECT name, description, input_schema, output_schema, permissions, simulatable, idempotent, registered_at
FROM capabilities WHERE name = ?;

-- name: ListCapabilities :many
SELECT name, description, input_schema, output_schema, permissions, simulatable, idempotent, registered_at
FROM capabilities
ORDER BY name;

-- name: UpsertAction :exec
INSERT INTO actions (id, capability, payload, caller_type, caller_id, caller_name, scope, idempotency_key, status, result, error, policy_decision, executed_at, completed_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    capability      = excluded.capability,
    payload         = excluded.payload,
    caller_type     = excluded.caller_type,
    caller_id       = excluded.caller_id,
    caller_name     = excluded.caller_name,
    scope           = excluded.scope,
    idempotency_key = excluded.idempotency_key,
    status          = excluded.status,
    result          = excluded.result,
    error           = excluded.error,
    policy_decision = excluded.policy_decision,
    executed_at     = excluded.executed_at,
    completed_at    = excluded.completed_at,
    updated_at      = excluded.updated_at;

-- name: GetAction :one
SELECT id, capability, payload, caller_type, caller_id, caller_name, scope, idempotency_key, status, result, error, policy_decision, executed_at, completed_at, created_at, updated_at
FROM actions WHERE id = ?;

-- name: UpdateActionStatus :exec
UPDATE actions SET status = ?, updated_at = ? WHERE id = ?;

-- name: PutActionResult :exec
UPDATE actions SET status = ?, result = ?, error = ?, completed_at = ?, updated_at = ? WHERE id = ?;

-- name: AppendAuditEvent :exec
INSERT INTO audit_events (id, action_id, kind, capability, caller_type, detail, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditForAction :many
SELECT id, action_id, kind, capability, caller_type, detail, created_at
FROM audit_events WHERE action_id = ? ORDER BY created_at;

-- name: SearchAuditEvents :many
SELECT id, action_id, kind, capability, caller_type, detail, created_at
FROM audit_events
WHERE (sqlc.narg('capability')  IS NULL OR capability  = sqlc.narg('capability'))
  AND (sqlc.narg('caller_type') IS NULL OR caller_type = sqlc.narg('caller_type'))
  AND (sqlc.narg('from_ts')     IS NULL OR created_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')       IS NULL OR created_at <= sqlc.narg('to_ts'))
ORDER BY created_at;

-- name: LookupIdempotency :one
SELECT key, action_id, result, expires_at, created_at
FROM idempotency_keys WHERE key = ?;

-- name: RememberIdempotency :exec
INSERT INTO idempotency_keys (key, action_id, result, expires_at, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
    action_id  = excluded.action_id,
    result     = excluded.result,
    expires_at = excluded.expires_at;

-- name: UpsertPolicyRule :exec
INSERT INTO policy_rules (id, capability, caller_type, scope, decision, reason, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    capability  = excluded.capability,
    caller_type = excluded.caller_type,
    scope       = excluded.scope,
    decision    = excluded.decision,
    reason      = excluded.reason,
    updated_at  = excluded.updated_at;

-- name: ListPolicyRules :many
SELECT id, capability, caller_type, scope, decision, reason, created_at, updated_at
FROM policy_rules
ORDER BY id;

-- name: DeletePolicyRule :exec
DELETE FROM policy_rules WHERE id = ?;

-- name: EnqueueOutcome :exec
INSERT INTO outcome_outbox (id, action_id, payload, attempts, next_attempt, delivered_at, last_error, created_at)
VALUES (?, ?, ?, 0, ?, NULL, NULL, ?);

-- name: NextOutcomeBatch :many
SELECT id, action_id, payload, attempts, next_attempt, delivered_at, last_error, created_at
FROM outcome_outbox
WHERE delivered_at IS NULL AND next_attempt <= ?
ORDER BY next_attempt
LIMIT ?;

-- name: MarkOutcomeDelivered :exec
UPDATE outcome_outbox SET delivered_at = ? WHERE id = ?;

-- name: BumpOutcomeAttempt :exec
UPDATE outcome_outbox SET attempts = attempts + 1, next_attempt = ?, last_error = ? WHERE id = ?;
