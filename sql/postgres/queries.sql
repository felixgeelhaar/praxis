-- name: UpsertCapability :exec
INSERT INTO capabilities (name, description, input_schema, input_schema_version, output_schema, output_schema_version, permissions, simulatable, idempotent, registered_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT(name) DO UPDATE SET
    description           = EXCLUDED.description,
    input_schema          = EXCLUDED.input_schema,
    input_schema_version  = EXCLUDED.input_schema_version,
    output_schema         = EXCLUDED.output_schema,
    output_schema_version = EXCLUDED.output_schema_version,
    permissions           = EXCLUDED.permissions,
    simulatable           = EXCLUDED.simulatable,
    idempotent            = EXCLUDED.idempotent,
    registered_at         = EXCLUDED.registered_at;

-- name: GetCapability :one
SELECT name, description, input_schema, input_schema_version, output_schema, output_schema_version, permissions, simulatable, idempotent, registered_at
FROM capabilities WHERE name = $1;

-- name: ListCapabilities :many
SELECT name, description, input_schema, input_schema_version, output_schema, output_schema_version, permissions, simulatable, idempotent, registered_at
FROM capabilities
ORDER BY name;

-- name: UpsertAction :exec
INSERT INTO actions (id, capability, payload, caller_type, caller_id, caller_name, scope, idempotency_key, status, mode, result, error, policy_decision, executed_at, completed_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT(id) DO UPDATE SET
    capability      = EXCLUDED.capability,
    payload         = EXCLUDED.payload,
    caller_type     = EXCLUDED.caller_type,
    caller_id       = EXCLUDED.caller_id,
    caller_name     = EXCLUDED.caller_name,
    scope           = EXCLUDED.scope,
    idempotency_key = EXCLUDED.idempotency_key,
    status          = EXCLUDED.status,
    mode            = EXCLUDED.mode,
    result          = EXCLUDED.result,
    error           = EXCLUDED.error,
    policy_decision = EXCLUDED.policy_decision,
    executed_at     = EXCLUDED.executed_at,
    completed_at    = EXCLUDED.completed_at,
    updated_at      = EXCLUDED.updated_at;

-- name: GetAction :one
SELECT id, capability, payload, caller_type, caller_id, caller_name, scope, idempotency_key, status, mode, result, error, policy_decision, executed_at, completed_at, created_at, updated_at
FROM actions WHERE id = $1;

-- name: ListPendingAsync :many
SELECT id, capability, payload, caller_type, caller_id, caller_name, scope, idempotency_key, status, mode, result, error, policy_decision, executed_at, completed_at, created_at, updated_at
FROM actions
WHERE mode = 'async' AND status = 'validated'
ORDER BY created_at
LIMIT $1;

-- name: UpdateActionStatus :exec
UPDATE actions SET status = $2, updated_at = $3 WHERE id = $1;

-- name: PutActionResult :exec
UPDATE actions SET status = $2, result = $3, error = $4, completed_at = $5, updated_at = $6 WHERE id = $1;

-- name: ListActionsPaged :many
SELECT id, capability, payload, caller_type, caller_id, caller_name, scope, idempotency_key, status, mode, result, error, policy_decision, executed_at, completed_at, created_at, updated_at
FROM actions
ORDER BY created_at DESC
LIMIT $1;

-- name: AppendAuditEvent :exec
INSERT INTO audit_events (id, action_id, kind, capability, caller_type, org_id, team_id, detail, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListAuditForAction :many
SELECT id, action_id, kind, capability, caller_type, org_id, team_id, detail, created_at
FROM audit_events WHERE action_id = $1 ORDER BY created_at;

-- name: SearchAuditEvents :many
SELECT id, action_id, kind, capability, caller_type, org_id, team_id, detail, created_at
FROM audit_events
WHERE (sqlc.narg('capability')::TEXT       IS NULL OR capability  = sqlc.narg('capability'))
  AND (sqlc.narg('caller_type')::TEXT      IS NULL OR caller_type = sqlc.narg('caller_type'))
  AND (sqlc.narg('org_id')::TEXT           IS NULL OR org_id      = sqlc.narg('org_id'))
  AND (sqlc.narg('from_ts')::TIMESTAMPTZ   IS NULL OR created_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::TIMESTAMPTZ     IS NULL OR created_at <= sqlc.narg('to_ts'))
ORDER BY created_at;

-- name: PurgeAuditBefore :execrows
DELETE FROM audit_events
WHERE created_at < sqlc.arg('before')
  AND org_id = sqlc.arg('org_id');

-- name: LookupIdempotency :one
SELECT key, action_id, result, expires_at, created_at
FROM idempotency_keys WHERE key = $1;

-- name: RememberIdempotency :exec
INSERT INTO idempotency_keys (key, action_id, result, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT(key) DO UPDATE SET
    action_id  = EXCLUDED.action_id,
    result     = EXCLUDED.result,
    expires_at = EXCLUDED.expires_at;

-- name: UpsertPolicyRule :exec
INSERT INTO policy_rules (id, capability, caller_type, scope, decision, reason, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT(id) DO UPDATE SET
    capability  = EXCLUDED.capability,
    caller_type = EXCLUDED.caller_type,
    scope       = EXCLUDED.scope,
    decision    = EXCLUDED.decision,
    reason      = EXCLUDED.reason,
    updated_at  = EXCLUDED.updated_at;

-- name: ListPolicyRules :many
SELECT id, capability, caller_type, scope, decision, reason, created_at, updated_at
FROM policy_rules
ORDER BY id;

-- name: DeletePolicyRule :exec
DELETE FROM policy_rules WHERE id = $1;

-- name: EnqueueOutcome :exec
INSERT INTO outcome_outbox (id, action_id, payload, attempts, next_attempt, delivered_at, last_error, created_at)
VALUES ($1, $2, $3, 0, $4, NULL, NULL, $5);

-- name: NextOutcomeBatch :many
SELECT id, action_id, payload, attempts, next_attempt, delivered_at, last_error, created_at
FROM outcome_outbox
WHERE delivered_at IS NULL AND next_attempt <= $1
ORDER BY next_attempt
LIMIT $2;

-- name: MarkOutcomeDelivered :exec
UPDATE outcome_outbox SET delivered_at = $1 WHERE id = $2;

-- name: BumpOutcomeAttempt :exec
UPDATE outcome_outbox SET attempts = attempts + 1, next_attempt = $1, last_error = $2 WHERE id = $3;
