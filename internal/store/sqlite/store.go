// Package sqlite implements ports.Repos backed by a SQLite database.
//
// Schema lives in internal/store/sqlite/migrations/, applied in lexical order
// at Open(). All queries are sqlc-generated; hand-rolled SQL is a code-review
// red flag (see AGENTS.md).
package sqlite

import (
	"context"
	"database/sql"
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
	"github.com/felixgeelhaar/praxis/internal/store/sqlite/sqlcgen"

	// modernc.org/sqlite is registered as the "sqlite" sql/driver via
	// its package init. We never reference it by name; the blank
	// import exists solely to wire driver registration.
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const driverName = "sqlite"

// Open opens or creates the SQLite database at conn, runs migrations, and
// returns a fully-wired *ports.Repos.
//
// `conn` may be a file path, an empty string for ":memory:", or a full DSN
// (e.g. "file:praxis.db?cache=shared&_fk=1").
func Open(_ context.Context, logger *bolt.Logger, conn string) (*ports.Repos, error) {
	if conn == "" {
		conn = ":memory:"
	}
	db, err := sql.Open(driverName, conn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite ping: %w", err)
	}
	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite migrate: %w", err)
	}
	if logger != nil {
		logger.Info().Str("backend", "sqlite").Str("conn", conn).Msg("storage opened")
	}

	q := sqlcgen.New(db)
	return &ports.Repos{
		Capability:  &capabilityAdapter{q: q},
		Action:      &actionAdapter{q: q},
		Idempotency: &idempotencyAdapter{q: q},
		Audit:       &auditAdapter{q: q, db: db},
		Policy:      &policyAdapter{q: q},
		Outbox:      &outboxAdapter{q: q},
		Close:       db.Close,
	}, nil
}

func runMigrations(db *sql.DB) error {
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
		body, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		if _, err := db.Exec(string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", n, err)
		}
	}
	return nil
}

// --- helpers ---

const tsFmt = time.RFC3339Nano

func formatTS(t time.Time) string {
	return t.UTC().Format(tsFmt)
}

func parseTS(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(tsFmt, s)
}

func nullTS(t *time.Time) sql.NullString {
	if t == nil || t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTS(*t), Valid: true}
}

func parseNullTS(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := parseTS(ns.String)
	if err != nil {
		return nil
	}
	return &t
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func valStr(ns sql.NullString) string {
	if !ns.Valid {
		return ""
	}
	return ns.String
}

func mustJSON(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func parseJSONMap(s string) map[string]any {
	if s == "" || s == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}

func parseJSONStrings(s string) []string {
	if s == "" || s == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// --- capability adapter ---

type capabilityAdapter struct{ q *sqlcgen.Queries }

func (a *capabilityAdapter) Upsert(ctx context.Context, c domain.Capability) error {
	return a.q.UpsertCapability(ctx, sqlcgen.UpsertCapabilityParams{
		Name:                c.Name,
		Description:         nullStr(c.Description),
		InputSchema:         mustJSON(c.InputSchema),
		InputSchemaVersion:  defaultStr(c.InputSchemaVersion, "1"),
		OutputSchema:        mustJSON(c.OutputSchema),
		OutputSchemaVersion: defaultStr(c.OutputSchemaVersion, "1"),
		Permissions:         mustJSON(c.Permissions),
		Simulatable:         boolToInt(c.Simulatable),
		Idempotent:          boolToInt(c.Idempotent),
		RegisteredAt:        formatTS(time.Now()),
	})
}

// defaultStr returns s when non-empty, otherwise def. Used for the
// schema-version columns where the SQL default is '1' but Go zero
// values come through as the empty string.
func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (a *capabilityAdapter) Get(ctx context.Context, name string) (domain.Capability, error) {
	row, err := a.q.GetCapability(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Capability{}, ports.ErrNotFound
	}
	if err != nil {
		return domain.Capability{}, err
	}
	return domain.Capability{
		Name:                row.Name,
		Description:         valStr(row.Description),
		InputSchema:         parseJSONMap(row.InputSchema),
		InputSchemaVersion:  row.InputSchemaVersion,
		OutputSchema:        parseJSONMap(row.OutputSchema),
		OutputSchemaVersion: row.OutputSchemaVersion,
		Permissions:         parseJSONStrings(row.Permissions),
		Simulatable:         row.Simulatable != 0,
		Idempotent:          row.Idempotent != 0,
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
			Description:         valStr(r.Description),
			InputSchema:         parseJSONMap(r.InputSchema),
			InputSchemaVersion:  r.InputSchemaVersion,
			OutputSchema:        parseJSONMap(r.OutputSchema),
			OutputSchemaVersion: r.OutputSchemaVersion,
			Permissions:         parseJSONStrings(r.Permissions),
			Simulatable:         r.Simulatable != 0,
			Idempotent:          r.Idempotent != 0,
		})
	}
	return out, nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
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
	var resultJSON, errorJSON, policyJSON sql.NullString
	if act.Result != nil {
		resultJSON = nullStr(mustJSON(act.Result))
	}
	if act.Error != nil {
		errorJSON = nullStr(mustJSON(act.Error))
	}
	if act.PolicyDecision != nil {
		policyJSON = nullStr(mustJSON(act.PolicyDecision))
	}
	mode := string(act.Mode)
	if mode == "" {
		mode = string(domain.ModeSync)
	}
	return a.q.UpsertAction(ctx, sqlcgen.UpsertActionParams{
		ID:             act.ID,
		Capability:     act.Capability,
		Payload:        mustJSON(act.Payload),
		CallerType:     act.Caller.Type,
		CallerID:       act.Caller.ID,
		CallerName:     act.Caller.Name,
		Scope:          mustJSON(act.Scope),
		IdempotencyKey: act.IdempotencyKey,
		Status:         string(act.Status),
		Mode:           mode,
		Result:         resultJSON,
		Error:          errorJSON,
		PolicyDecision: policyJSON,
		ExecutedAt:     nullTS(act.ExecutedAt),
		CompletedAt:    nullTS(act.CompletedAt),
		CreatedAt:      formatTS(act.CreatedAt),
		UpdatedAt:      formatTS(act.UpdatedAt),
	})
}

func (a *actionAdapter) Get(ctx context.Context, id string) (domain.Action, error) {
	row, err := a.q.GetAction(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
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
	rows, err := a.q.ListPendingAsync(ctx, int64(limit))
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
		UpdatedAt: formatTS(time.Now()),
	})
}

func (a *actionAdapter) PutResult(ctx context.Context, id string, r domain.Result) error {
	var errJSON sql.NullString
	if r.Error != nil {
		errJSON = nullStr(mustJSON(r.Error))
	}
	completed := r.CompletedAt
	if completed.IsZero() {
		completed = time.Now()
	}
	return a.q.PutActionResult(ctx, sqlcgen.PutActionResultParams{
		ID:          id,
		Status:      string(r.Status),
		Result:      nullStr(mustJSON(r.Output)),
		Error:       errJSON,
		CompletedAt: sql.NullString{String: formatTS(completed), Valid: true},
		UpdatedAt:   formatTS(time.Now()),
	})
}

func actionFromRow(r sqlcgen.Action) domain.Action {
	created, _ := parseTS(r.CreatedAt)
	updated, _ := parseTS(r.UpdatedAt)
	a := domain.Action{
		ID:             r.ID,
		Capability:     r.Capability,
		Payload:        parseJSONMap(r.Payload),
		Caller:         domain.CallerRef{Type: r.CallerType, ID: r.CallerID, Name: r.CallerName},
		Scope:          parseJSONStrings(r.Scope),
		IdempotencyKey: r.IdempotencyKey,
		Status:         domain.ActionStatus(r.Status),
		Mode:           domain.ActionMode(r.Mode),
		ExecutedAt:     parseNullTS(r.ExecutedAt),
		CompletedAt:    parseNullTS(r.CompletedAt),
		CreatedAt:      created,
		UpdatedAt:      updated,
	}
	if r.Result.Valid {
		a.Result = parseJSONMap(r.Result.String)
	}
	if r.Error.Valid {
		var ae domain.ActionError
		if err := json.Unmarshal([]byte(r.Error.String), &ae); err == nil {
			a.Error = &ae
		}
	}
	if r.PolicyDecision.Valid {
		var pd domain.PolicyDecision
		if err := json.Unmarshal([]byte(r.PolicyDecision.String), &pd); err == nil {
			a.PolicyDecision = &pd
		}
	}
	return a
}

// --- idempotency adapter ---

type idempotencyAdapter struct{ q *sqlcgen.Queries }

func (a *idempotencyAdapter) Lookup(ctx context.Context, key string) (*domain.Result, error) {
	row, err := a.q.LookupIdempotency(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.ExpiresAt.Valid {
		exp, perr := parseTS(row.ExpiresAt.String)
		if perr == nil && !exp.IsZero() && time.Now().After(exp) {
			return nil, ports.ErrNotFound
		}
	}
	var res domain.Result
	if err := json.Unmarshal([]byte(row.Result), &res); err != nil {
		return nil, fmt.Errorf("decode idempotency result: %w", err)
	}
	return &res, nil
}

func (a *idempotencyAdapter) Remember(ctx context.Context, key string, r domain.Result, ttl int64) error {
	var exp sql.NullString
	if ttl > 0 {
		exp = sql.NullString{String: formatTS(time.Now().Add(time.Duration(ttl) * time.Second)), Valid: true}
	}
	return a.q.RememberIdempotency(ctx, sqlcgen.RememberIdempotencyParams{
		Key:       key,
		ActionID:  r.ActionID,
		Result:    mustJSON(r),
		ExpiresAt: exp,
		CreatedAt: formatTS(time.Now()),
	})
}

// --- audit adapter ---

type auditAdapter struct {
	q  *sqlcgen.Queries
	db *sql.DB
}

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
		Detail:     mustJSON(e.Detail),
		CreatedAt:  formatTS(e.CreatedAt),
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
		params.Capability = sql.NullString{String: q.Capability, Valid: true}
	}
	if q.CallerType != "" {
		params.CallerType = sql.NullString{String: q.CallerType, Valid: true}
	}
	if q.OrgID != "" {
		params.OrgID = sql.NullString{String: q.OrgID, Valid: true}
	}
	if q.From > 0 {
		params.FromTs = sql.NullString{String: formatTS(time.Unix(q.From, 0)), Valid: true}
	}
	if q.To > 0 {
		params.ToTs = sql.NullString{String: formatTS(time.Unix(q.To, 0)), Valid: true}
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
		Before: formatTS(time.Unix(before, 0)),
		OrgID:  orgID,
	})
}

func auditFromRow(r sqlcgen.AuditEvent) domain.AuditEvent {
	created, _ := parseTS(r.CreatedAt)
	return domain.AuditEvent{
		ID:        r.ID,
		ActionID:  r.ActionID,
		Kind:      r.Kind,
		OrgID:     r.OrgID,
		TeamID:    r.TeamID,
		Detail:    parseJSONMap(r.Detail),
		CreatedAt: created,
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
		created, _ := parseTS(r.CreatedAt)
		updated, _ := parseTS(r.UpdatedAt)
		out = append(out, domain.PolicyRule{
			ID:         r.ID,
			Capability: valStr(r.Capability),
			CallerType: valStr(r.CallerType),
			Scope:      parseJSONStrings(r.Scope),
			Decision:   r.Decision,
			Reason:     valStr(r.Reason),
			CreatedAt:  created,
			UpdatedAt:  updated,
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
	return a.q.UpsertPolicyRule(ctx, sqlcgen.UpsertPolicyRuleParams{
		ID:         rule.ID,
		Capability: nullStr(rule.Capability),
		CallerType: nullStr(rule.CallerType),
		Scope:      mustJSON(rule.Scope),
		Decision:   rule.Decision,
		Reason:     nullStr(rule.Reason),
		CreatedAt:  formatTS(rule.CreatedAt),
		UpdatedAt:  formatTS(rule.UpdatedAt),
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
		Payload:     mustJSON(e.Event),
		NextAttempt: formatTS(e.NextAttempt),
		CreatedAt:   formatTS(e.CreatedAt),
	})
}

func (a *outboxAdapter) NextBatch(ctx context.Context, limit int, before time.Time) ([]ports.OutboxEnvelope, error) {
	rows, err := a.q.NextOutcomeBatch(ctx, sqlcgen.NextOutcomeBatchParams{
		NextAttempt: formatTS(before),
		Limit:       int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ports.OutboxEnvelope, 0, len(rows))
	for _, r := range rows {
		next, _ := parseTS(r.NextAttempt)
		created, _ := parseTS(r.CreatedAt)
		var ev domain.MnemosEvent
		_ = json.Unmarshal([]byte(r.Payload), &ev)
		out = append(out, ports.OutboxEnvelope{
			ID:          r.ID,
			ActionID:    r.ActionID,
			Event:       ev,
			Attempts:    int(r.Attempts),
			NextAttempt: next,
			DeliveredAt: parseNullTS(r.DeliveredAt),
			LastError:   valStr(r.LastError),
			CreatedAt:   created,
		})
	}
	return out, nil
}

func (a *outboxAdapter) MarkDelivered(ctx context.Context, id string, deliveredAt time.Time) error {
	return a.q.MarkOutcomeDelivered(ctx, sqlcgen.MarkOutcomeDeliveredParams{
		ID:          id,
		DeliveredAt: sql.NullString{String: formatTS(deliveredAt), Valid: true},
	})
}

func (a *outboxAdapter) BumpAttempt(ctx context.Context, id string, nextAttempt time.Time, lastError string) error {
	return a.q.BumpOutcomeAttempt(ctx, sqlcgen.BumpOutcomeAttemptParams{
		ID:          id,
		NextAttempt: formatTS(nextAttempt),
		LastError:   nullStr(lastError),
	})
}
