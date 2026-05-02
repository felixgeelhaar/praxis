// Package postgres implements ports.Repos backed by PostgreSQL via pgx/v5.
//
// Schema lives in internal/store/postgres/migrations/, applied in lexical
// order at Open(). All queries are sqlc-generated.
package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/postgres/sqlcgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open connects to a Postgres database via the supplied DSN, runs migrations,
// and returns a fully-wired *ports.Repos.
//
// `conn` accepts any libpq DSN supported by pgx (e.g.
// "postgres://user:pass@host:5432/db?sslmode=disable").
func Open(ctx context.Context, logger *bolt.Logger, conn string) (*ports.Repos, error) {
	if conn == "" {
		return nil, errors.New("postgres: PRAXIS_DB_CONN required")
	}
	pool, err := pgxpool.New(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	if err := runMigrations(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres migrate: %w", err)
	}
	if logger != nil {
		logger.Info().Str("backend", "postgres").Msg("storage opened")
	}

	q := sqlcgen.New(pool)
	return &ports.Repos{
		Capability:        &capabilityAdapter{q: q},
		Action:            &actionAdapter{q: q},
		Idempotency:       &idempotencyAdapter{q: q},
		Audit:             &auditAdapter{q: q},
		Policy:            &policyAdapter{q: q},
		Outbox:            &outboxAdapter{q: q},
		CapabilityHistory: &capabilityHistoryAdapter{q: q},
		Close: func() error {
			pool.Close()
			return nil
		},
	}, nil
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	rows, err := pool.Query(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("list applied: %w", err)
	}
	applied := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		applied[n] = true
	}
	rows.Close()

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if applied[n] {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", n, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, n); err != nil {
			return fmt.Errorf("record %s: %w", n, err)
		}
	}
	return nil
}

// --- helpers ---

func mustJSONBytes(v any) []byte {
	if v == nil {
		return []byte("null")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

func parseJSONMap(b []byte) map[string]any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func ts(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func tsPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil || t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func textNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func textVal(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func tsVal(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func tsValPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	tm := t.Time
	return &tm
}

// --- capability adapter ---

type capabilityAdapter struct{ q *sqlcgen.Queries }

func (a *capabilityAdapter) Upsert(ctx context.Context, c domain.Capability) error {
	// `permissions` column is `text[] not null default '{}'`; pgx
	// serialises a nil []string as NULL, which trips the not-null
	// constraint. Normalise to an empty slice so the schema's default
	// and the runtime contract agree.
	perms := c.Permissions
	if perms == nil {
		perms = []string{}
	}
	return a.q.UpsertCapability(ctx, sqlcgen.UpsertCapabilityParams{
		Name:                c.Name,
		Description:         textNull(c.Description),
		InputSchema:         mustJSONBytes(c.InputSchema),
		InputSchemaVersion:  defaultStr(c.InputSchemaVersion, "1"),
		OutputSchema:        mustJSONBytes(c.OutputSchema),
		OutputSchemaVersion: defaultStr(c.OutputSchemaVersion, "1"),
		Permissions:         perms,
		Simulatable:         c.Simulatable,
		Idempotent:          c.Idempotent,
		RegisteredAt:        ts(time.Now()),
	})
}

// defaultStr mirrors the sqlite adapter's helper.
func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (a *capabilityAdapter) Get(ctx context.Context, name string) (domain.Capability, error) {
	row, err := a.q.GetCapability(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Capability{}, ports.ErrNotFound
	}
	if err != nil {
		return domain.Capability{}, err
	}
	return domain.Capability{
		Name:                row.Name,
		Description:         textVal(row.Description),
		InputSchema:         parseJSONMap(row.InputSchema),
		InputSchemaVersion:  row.InputSchemaVersion,
		OutputSchema:        parseJSONMap(row.OutputSchema),
		OutputSchemaVersion: row.OutputSchemaVersion,
		Permissions:         row.Permissions,
		Simulatable:         row.Simulatable,
		Idempotent:          row.Idempotent,
	}, nil
}

func (a *capabilityAdapter) List(ctx context.Context) ([]domain.Capability, error) {
	rows, err := a.q.ListCapabilities(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Capability, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.Capability{
			Name:                r.Name,
			Description:         textVal(r.Description),
			InputSchema:         parseJSONMap(r.InputSchema),
			InputSchemaVersion:  r.InputSchemaVersion,
			OutputSchema:        parseJSONMap(r.OutputSchema),
			OutputSchemaVersion: r.OutputSchemaVersion,
			Permissions:         r.Permissions,
			Simulatable:         r.Simulatable,
			Idempotent:          r.Idempotent,
		})
	}
	return out, nil
}

// --- action adapter ---

type actionAdapter struct{ q *sqlcgen.Queries }

func (a *actionAdapter) Save(ctx context.Context, act domain.Action) error {
	if act.UpdatedAt.IsZero() {
		act.UpdatedAt = time.Now()
	}
	if act.CreatedAt.IsZero() {
		act.CreatedAt = act.UpdatedAt
	}
	scope := act.Scope
	if scope == nil {
		scope = []string{}
	}
	mode := string(act.Mode)
	if mode == "" {
		mode = string(domain.ModeSync)
	}
	return a.q.UpsertAction(ctx, sqlcgen.UpsertActionParams{
		ID:             act.ID,
		Capability:     act.Capability,
		Payload:        mustJSONBytes(act.Payload),
		CallerType:     act.Caller.Type,
		CallerID:       act.Caller.ID,
		CallerName:     act.Caller.Name,
		Scope:          scope,
		IdempotencyKey: act.IdempotencyKey,
		Status:         string(act.Status),
		Mode:           mode,
		Result:         mustJSONBytes(act.Result),
		Error:          mustJSONBytes(act.Error),
		PolicyDecision: mustJSONBytes(act.PolicyDecision),
		ExecutedAt:     tsPtr(act.ExecutedAt),
		CompletedAt:    tsPtr(act.CompletedAt),
		CreatedAt:      ts(act.CreatedAt),
		UpdatedAt:      ts(act.UpdatedAt),
	})
}

func (a *actionAdapter) Get(ctx context.Context, id string) (domain.Action, error) {
	row, err := a.q.GetAction(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Action{}, ports.ErrNotFound
	}
	if err != nil {
		return domain.Action{}, err
	}
	return actionFromRow(sqlcgen.Action{
		ID: row.ID, Capability: row.Capability, Payload: row.Payload,
		CallerType: row.CallerType, CallerID: row.CallerID, CallerName: row.CallerName,
		Scope: row.Scope, IdempotencyKey: row.IdempotencyKey, Status: row.Status,
		Mode: row.Mode, Result: row.Result, Error: row.Error, PolicyDecision: row.PolicyDecision,
		ExecutedAt: row.ExecutedAt, CompletedAt: row.CompletedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}), nil
}

func (a *actionAdapter) ListPendingAsync(ctx context.Context, limit int) ([]domain.Action, error) {
	rows, err := a.q.ListPendingAsync(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]domain.Action, 0, len(rows))
	for _, r := range rows {
		out = append(out, actionFromRow(sqlcgen.Action{
			ID: r.ID, Capability: r.Capability, Payload: r.Payload,
			CallerType: r.CallerType, CallerID: r.CallerID, CallerName: r.CallerName,
			Scope: r.Scope, IdempotencyKey: r.IdempotencyKey, Status: r.Status,
			Mode: r.Mode, Result: r.Result, Error: r.Error, PolicyDecision: r.PolicyDecision,
			ExecutedAt: r.ExecutedAt, CompletedAt: r.CompletedAt,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}))
	}
	return out, nil
}

func (a *actionAdapter) UpdateStatus(ctx context.Context, id string, s domain.ActionStatus) error {
	return a.q.UpdateActionStatus(ctx, sqlcgen.UpdateActionStatusParams{
		ID:        id,
		Status:    string(s),
		UpdatedAt: ts(time.Now()),
	})
}

func (a *actionAdapter) PutResult(ctx context.Context, id string, r domain.Result) error {
	completed := r.CompletedAt
	if completed.IsZero() {
		completed = time.Now()
	}
	return a.q.PutActionResult(ctx, sqlcgen.PutActionResultParams{
		ID:          id,
		Status:      string(r.Status),
		Result:      mustJSONBytes(r.Output),
		Error:       mustJSONBytes(r.Error),
		CompletedAt: ts(completed),
		UpdatedAt:   ts(time.Now()),
	})
}

func actionFromRow(r sqlcgen.Action) domain.Action {
	a := domain.Action{
		ID:             r.ID,
		Capability:     r.Capability,
		Payload:        parseJSONMap(r.Payload),
		Caller:         domain.CallerRef{Type: r.CallerType, ID: r.CallerID, Name: r.CallerName},
		Scope:          r.Scope,
		IdempotencyKey: r.IdempotencyKey,
		Status:         domain.ActionStatus(r.Status),
		Mode:           domain.ActionMode(r.Mode),
		ExecutedAt:     tsValPtr(r.ExecutedAt),
		CompletedAt:    tsValPtr(r.CompletedAt),
		CreatedAt:      tsVal(r.CreatedAt),
		UpdatedAt:      tsVal(r.UpdatedAt),
	}
	if len(r.Result) > 0 {
		a.Result = parseJSONMap(r.Result)
	}
	if len(r.Error) > 0 {
		var ae domain.ActionError
		if err := json.Unmarshal(r.Error, &ae); err == nil && ae.Code != "" {
			a.Error = &ae
		}
	}
	if len(r.PolicyDecision) > 0 {
		var pd domain.PolicyDecision
		if err := json.Unmarshal(r.PolicyDecision, &pd); err == nil && pd.Decision != "" {
			a.PolicyDecision = &pd
		}
	}
	return a
}

// --- idempotency adapter ---

type idempotencyAdapter struct{ q *sqlcgen.Queries }

func (a *idempotencyAdapter) Lookup(ctx context.Context, key string) (*domain.Result, error) {
	row, err := a.q.LookupIdempotency(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.ExpiresAt.Valid && !row.ExpiresAt.Time.IsZero() && time.Now().After(row.ExpiresAt.Time) {
		return nil, ports.ErrNotFound
	}
	var res domain.Result
	if err := json.Unmarshal(row.Result, &res); err != nil {
		return nil, fmt.Errorf("decode idempotency result: %w", err)
	}
	return &res, nil
}

func (a *idempotencyAdapter) Remember(ctx context.Context, key string, r domain.Result, ttl int64) error {
	var exp pgtype.Timestamptz
	if ttl > 0 {
		exp = ts(time.Now().Add(time.Duration(ttl) * time.Second))
	}
	return a.q.RememberIdempotency(ctx, sqlcgen.RememberIdempotencyParams{
		Key:       key,
		ActionID:  r.ActionID,
		Result:    mustJSONBytes(r),
		ExpiresAt: exp,
		CreatedAt: ts(time.Now()),
	})
}

// --- audit adapter ---

type auditAdapter struct{ q *sqlcgen.Queries }

func (a *auditAdapter) Append(ctx context.Context, e domain.AuditEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	cap, _ := e.Detail["capability"].(string)
	callerType, _ := e.Detail["caller_type"].(string)
	return a.q.AppendAuditEvent(ctx, sqlcgen.AppendAuditEventParams{
		ID:         e.ID,
		ActionID:   e.ActionID,
		Kind:       e.Kind,
		Capability: cap,
		CallerType: callerType,
		OrgID:      e.OrgID,
		TeamID:     e.TeamID,
		Detail:     mustJSONBytes(e.Detail),
		CreatedAt:  ts(e.CreatedAt),
	})
}

func (a *auditAdapter) ListForAction(ctx context.Context, actionID string) ([]domain.AuditEvent, error) {
	rows, err := a.q.ListAuditForAction(ctx, actionID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AuditEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, auditFromRow(sqlcgen.AuditEvent{
			ID:         r.ID,
			ActionID:   r.ActionID,
			Kind:       r.Kind,
			Capability: r.Capability,
			CallerType: r.CallerType,
			OrgID:      r.OrgID,
			TeamID:     r.TeamID,
			Detail:     r.Detail,
			CreatedAt:  r.CreatedAt,
		}))
	}
	return out, nil
}

func (a *auditAdapter) Search(ctx context.Context, q ports.AuditQuery) ([]domain.AuditEvent, error) {
	params := sqlcgen.SearchAuditEventsParams{}
	if q.Capability != "" {
		params.Capability = pgtype.Text{String: q.Capability, Valid: true}
	}
	if q.CallerType != "" {
		params.CallerType = pgtype.Text{String: q.CallerType, Valid: true}
	}
	if q.OrgID != "" {
		params.OrgID = pgtype.Text{String: q.OrgID, Valid: true}
	}
	if q.From > 0 {
		params.FromTs = ts(time.Unix(q.From, 0))
	}
	if q.To > 0 {
		params.ToTs = ts(time.Unix(q.To, 0))
	}
	rows, err := a.q.SearchAuditEvents(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AuditEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, auditFromRow(sqlcgen.AuditEvent{
			ID:         r.ID,
			ActionID:   r.ActionID,
			Kind:       r.Kind,
			Capability: r.Capability,
			CallerType: r.CallerType,
			OrgID:      r.OrgID,
			TeamID:     r.TeamID,
			Detail:     r.Detail,
			CreatedAt:  r.CreatedAt,
		}))
	}
	return out, nil
}

func (a *auditAdapter) PurgeBefore(ctx context.Context, orgID string, before int64) (int64, error) {
	return a.q.PurgeAuditBefore(ctx, sqlcgen.PurgeAuditBeforeParams{
		Before: ts(time.Unix(before, 0)),
		OrgID:  orgID,
	})
}

func auditFromRow(r sqlcgen.AuditEvent) domain.AuditEvent {
	return domain.AuditEvent{
		ID:        r.ID,
		ActionID:  r.ActionID,
		Kind:      r.Kind,
		OrgID:     r.OrgID,
		TeamID:    r.TeamID,
		Detail:    parseJSONMap(r.Detail),
		CreatedAt: tsVal(r.CreatedAt),
	}
}

// --- policy adapter ---

type policyAdapter struct{ q *sqlcgen.Queries }

func (a *policyAdapter) ListRules(ctx context.Context) ([]domain.PolicyRule, error) {
	rows, err := a.q.ListPolicyRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.PolicyRule, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.PolicyRule{
			ID:         r.ID,
			Capability: textVal(r.Capability),
			CallerType: textVal(r.CallerType),
			Scope:      r.Scope,
			Decision:   r.Decision,
			Reason:     textVal(r.Reason),
			CreatedAt:  tsVal(r.CreatedAt),
			UpdatedAt:  tsVal(r.UpdatedAt),
		})
	}
	return out, nil
}

func (a *policyAdapter) UpsertRule(ctx context.Context, rule domain.PolicyRule) error {
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = time.Now()
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = rule.UpdatedAt
	}
	scope := rule.Scope
	if scope == nil {
		scope = []string{}
	}
	return a.q.UpsertPolicyRule(ctx, sqlcgen.UpsertPolicyRuleParams{
		ID:         rule.ID,
		Capability: textNull(rule.Capability),
		CallerType: textNull(rule.CallerType),
		Scope:      scope,
		Decision:   rule.Decision,
		Reason:     textNull(rule.Reason),
		CreatedAt:  ts(rule.CreatedAt),
		UpdatedAt:  ts(rule.UpdatedAt),
	})
}

func (a *policyAdapter) DeleteRule(ctx context.Context, id string) error {
	return a.q.DeletePolicyRule(ctx, id)
}

// --- outbox adapter ---

type outboxAdapter struct{ q *sqlcgen.Queries }

func (a *outboxAdapter) Enqueue(ctx context.Context, e ports.OutboxEnvelope) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	return a.q.EnqueueOutcome(ctx, sqlcgen.EnqueueOutcomeParams{
		ID:          e.ID,
		ActionID:    e.ActionID,
		Payload:     mustJSONBytes(e.Event),
		NextAttempt: ts(e.NextAttempt),
		CreatedAt:   ts(e.CreatedAt),
	})
}

func (a *outboxAdapter) NextBatch(ctx context.Context, limit int, before time.Time) ([]ports.OutboxEnvelope, error) {
	rows, err := a.q.NextOutcomeBatch(ctx, sqlcgen.NextOutcomeBatchParams{
		NextAttempt: ts(before),
		Limit:       int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ports.OutboxEnvelope, 0, len(rows))
	for _, r := range rows {
		var ev domain.MnemosEvent
		_ = json.Unmarshal(r.Payload, &ev)
		out = append(out, ports.OutboxEnvelope{
			ID:          r.ID,
			ActionID:    r.ActionID,
			Event:       ev,
			Attempts:    int(r.Attempts),
			NextAttempt: tsVal(r.NextAttempt),
			DeliveredAt: tsValPtr(r.DeliveredAt),
			LastError:   textVal(r.LastError),
			CreatedAt:   tsVal(r.CreatedAt),
		})
	}
	return out, nil
}

func (a *outboxAdapter) MarkDelivered(ctx context.Context, id string, deliveredAt time.Time) error {
	return a.q.MarkOutcomeDelivered(ctx, sqlcgen.MarkOutcomeDeliveredParams{
		ID:          id,
		DeliveredAt: ts(deliveredAt),
	})
}

func (a *outboxAdapter) BumpAttempt(ctx context.Context, id string, nextAttempt time.Time, lastError string) error {
	return a.q.BumpOutcomeAttempt(ctx, sqlcgen.BumpOutcomeAttemptParams{
		ID:          id,
		NextAttempt: ts(nextAttempt),
		LastError:   textNull(lastError),
	})
}

// --- capability history adapter ---

type capabilityHistoryAdapter struct{ q *sqlcgen.Queries }

func (a *capabilityHistoryAdapter) Append(ctx context.Context, e domain.CapabilityHistoryEntry) error {
	if e.RecordedAt.IsZero() {
		e.RecordedAt = time.Now()
	}
	issues, err := json.Marshal(e.Issues)
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		issues = []byte("[]")
	}
	return a.q.AppendCapabilityHistory(ctx, sqlcgen.AppendCapabilityHistoryParams{
		ID:                e.ID,
		CapabilityName:    e.CapabilityName,
		RecordedAt:        ts(e.RecordedAt),
		PrevInputVersion:  e.PrevInputVersion,
		PrevOutputVersion: e.PrevOutputVersion,
		NextInputVersion:  e.NextInputVersion,
		NextOutputVersion: e.NextOutputVersion,
		Issues:            issues,
	})
}

func (a *capabilityHistoryAdapter) ListForCapability(ctx context.Context, name string) ([]domain.CapabilityHistoryEntry, error) {
	rows, err := a.q.ListCapabilityHistory(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]domain.CapabilityHistoryEntry, 0, len(rows))
	for _, r := range rows {
		var issues []domain.CapabilityHistoryIssue
		if len(r.Issues) > 0 {
			_ = json.Unmarshal(r.Issues, &issues)
		}
		out = append(out, domain.CapabilityHistoryEntry{
			ID:                r.ID,
			CapabilityName:    r.CapabilityName,
			RecordedAt:        tsVal(r.RecordedAt),
			PrevInputVersion:  r.PrevInputVersion,
			PrevOutputVersion: r.PrevOutputVersion,
			NextInputVersion:  r.NextInputVersion,
			NextOutputVersion: r.NextOutputVersion,
			Issues:            issues,
		})
	}
	return out, nil
}
