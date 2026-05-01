// axikernel.go wires the HTTP surface for Praxis. It mirrors the Mnemos
// pattern: one place where routes are declared, errors are mapped to HTTP
// status codes, and authentication is enforced.
//
// Endpoints (per docs/integrations.md):
//
//	GET  /healthz
//	GET  /metrics
//	GET  /v1/capabilities
//	GET  /v1/capabilities/{name}
//	POST /v1/actions
//	GET  /v1/actions/{id}
//	POST /v1/actions/{id}/dry-run
//	GET  /v1/audit
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/audit"
	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/executor"
	"github.com/felixgeelhaar/praxis/internal/outcome"
	"github.com/felixgeelhaar/praxis/internal/plugin"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

type kernelDeps struct {
	logger        *bolt.Logger
	exec          *executor.Executor
	registry      *capability.Registry
	repos         *ports.Repos
	auditSvc      *audit.Service
	pluginManager *plugin.Manager
	emitter       *outcome.Emitter
	apiToken      string
}

type metrics struct {
	actionsTotal     atomic.Uint64
	actionsFailed    atomic.Uint64
	actionsRejected  atomic.Uint64
	actionsSimulated atomic.Uint64
	requestDurMs     atomic.Uint64
	requestCount     atomic.Uint64
	pluginLoad       sync.Map // result string -> *atomic.Uint64
	auditPurge       sync.Map // "{orgID}\x00{result}" -> *atomic.Uint64
}

// incPluginLoad bumps the result-labelled plugin-load counter. Safe to
// call concurrently from the bootstrap goroutine that loads plugins.
func (m *metrics) incPluginLoad(result string) {
	v, _ := m.pluginLoad.LoadOrStore(result, &atomic.Uint64{})
	v.(*atomic.Uint64).Add(1)
}

// addAuditPurge records `count` audit rows deleted for the given org,
// labelled by result (ok|error). Called by the retention scheduler's
// OnPurge hook. The org_id and result are encoded as one map key with
// a NUL separator since neither label can contain NUL — no collision
// risk and one map lookup per increment.
func (m *metrics) addAuditPurge(orgID, result string, count int64) {
	key := orgID + "\x00" + result
	v, _ := m.auditPurge.LoadOrStore(key, &atomic.Uint64{})
	v.(*atomic.Uint64).Add(uint64(count))
}

func newMux(deps kernelDeps, m *metrics) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})

	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		delivered, failures := deps.emitter.Stats()
		fmt.Fprintf(w, "# HELP praxis_actions_total Number of actions processed by status.\n")
		fmt.Fprintf(w, "# TYPE praxis_actions_total counter\n")
		fmt.Fprintf(w, "praxis_actions_total{status=\"succeeded\"} %d\n", m.actionsTotal.Load()-m.actionsFailed.Load()-m.actionsRejected.Load()-m.actionsSimulated.Load())
		fmt.Fprintf(w, "praxis_actions_total{status=\"failed\"} %d\n", m.actionsFailed.Load())
		fmt.Fprintf(w, "praxis_actions_total{status=\"rejected\"} %d\n", m.actionsRejected.Load())
		fmt.Fprintf(w, "praxis_actions_total{status=\"simulated\"} %d\n", m.actionsSimulated.Load())
		fmt.Fprintf(w, "# HELP praxis_outcome_emit_total Mnemos delivery counters.\n")
		fmt.Fprintf(w, "# TYPE praxis_outcome_emit_total counter\n")
		fmt.Fprintf(w, "praxis_outcome_emit_total{result=\"delivered\"} %d\n", delivered)
		fmt.Fprintf(w, "praxis_outcome_emit_total{result=\"failed\"} %d\n", failures)
		fmt.Fprintf(w, "# HELP praxis_plugin_load_total Plugin load attempts by outcome.\n")
		fmt.Fprintf(w, "# TYPE praxis_plugin_load_total counter\n")
		m.pluginLoad.Range(func(k, v any) bool {
			fmt.Fprintf(w, "praxis_plugin_load_total{result=%q} %d\n", k.(string), v.(*atomic.Uint64).Load())
			return true
		})
		fmt.Fprintf(w, "# HELP praxis_audit_purge_total Audit events purged by retention sweep.\n")
		fmt.Fprintf(w, "# TYPE praxis_audit_purge_total counter\n")
		m.auditPurge.Range(func(k, v any) bool {
			parts := strings.SplitN(k.(string), "\x00", 2)
			if len(parts) != 2 {
				return true
			}
			fmt.Fprintf(w, "praxis_audit_purge_total{org_id=%q,result=%q} %d\n",
				parts[0], parts[1], v.(*atomic.Uint64).Load())
			return true
		})
		count := m.requestCount.Load()
		var avg uint64
		if count > 0 {
			avg = m.requestDurMs.Load() / count
		}
		fmt.Fprintf(w, "# HELP praxis_request_duration_ms_avg Average HTTP request duration.\n")
		fmt.Fprintf(w, "praxis_request_duration_ms_avg %d\n", avg)
	})

	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if deps.apiToken != "" {
				want := "Bearer " + deps.apiToken
				if r.Header.Get("Authorization") != want {
					writeJSON(w, http.StatusUnauthorized, errResponse("unauthorized"))
					return
				}
			}
			h(w, r)
		}
	}

	traced := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			h(w, r)
			dur := time.Since(start).Milliseconds()
			m.requestDurMs.Add(uint64(dur))
			m.requestCount.Add(1)
			deps.logger.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int64("dur_ms", dur).
				Msg("http")
		}
	}

	mux.Handle("GET /v1/capabilities", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		caller := callerFromHeaders(r)
		caps, err := deps.exec.ListCapabilitiesForCaller(r.Context(), caller)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResponse(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"capabilities": caps})
	})))

	mux.Handle("GET /v1/capabilities/{name}", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		caller := callerFromHeaders(r)
		cap, err := deps.registry.GetCapabilityForCaller(name, caller)
		if err != nil {
			writeJSON(w, http.StatusNotFound, errResponse("capability not found"))
			return
		}
		writeJSON(w, http.StatusOK, cap)
	})))

	mux.Handle("GET /v1/capabilities/{name}/changelog", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		writeJSON(w, http.StatusOK, map[string]any{
			"name":    name,
			"entries": deps.registry.History(name),
		})
	})))

	mux.Handle("POST /v1/actions", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		action, err := decodeAction(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errResponse(err.Error()))
			return
		}
		m.actionsTotal.Add(1)
		res, execErr := deps.exec.Execute(r.Context(), action)
		switch res.Status {
		case domain.StatusFailed:
			m.actionsFailed.Add(1)
		case domain.StatusRejected:
			m.actionsRejected.Add(1)
		}
		status := http.StatusOK
		switch {
		case execErr != nil:
			status = httpStatusForExecError(execErr, res)
		case action.Mode == domain.ModeAsync && res.Status == domain.StatusValidated:
			// Accepted for async processing — caller polls GET /v1/actions/{id}.
			status = http.StatusAccepted
		}
		writeJSON(w, status, res)
	})))

	mux.Handle("GET /v1/actions/{id}", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		a, err := deps.repos.Action.Get(r.Context(), id)
		if errors.Is(err, ports.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errResponse("action not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResponse(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, a)
	})))

	mux.Handle("POST /v1/actions/{id}/revert", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		res, err := deps.exec.Revert(r.Context(), id)
		status := http.StatusOK
		if err != nil {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, res)
	})))

	mux.Handle("POST /v1/actions/{id}/dry-run", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		action, err := decodeAction(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errResponse(err.Error()))
			return
		}
		if id := r.PathValue("id"); id != "" {
			action.ID = id
		}
		m.actionsTotal.Add(1)
		m.actionsSimulated.Add(1)
		sim, drErr := deps.exec.DryRun(r.Context(), action)
		if drErr != nil {
			writeJSON(w, http.StatusBadRequest, errResponse(drErr.Error()))
			return
		}
		writeJSON(w, http.StatusOK, sim)
	})))

	mux.Handle("GET /v1/dashboards/usage", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		q := ports.AuditQuery{
			Capability: r.URL.Query().Get("capability"),
			CallerType: r.URL.Query().Get("caller_type"),
		}
		if from := r.URL.Query().Get("from"); from != "" {
			if v, err := strconv.ParseInt(from, 10, 64); err == nil {
				q.From = v
			}
		}
		if to := r.URL.Query().Get("to"); to != "" {
			if v, err := strconv.ParseInt(to, 10, 64); err == nil {
				q.To = v
			}
		}
		dash, err := audit.BuildDashboard(r.Context(), deps.repos.Audit, q)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResponse(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, dash)
	})))

	mux.Handle("GET /v1/audit", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		q := ports.AuditQuery{
			Capability: r.URL.Query().Get("capability"),
			CallerType: r.URL.Query().Get("caller_type"),
			OrgID:      r.URL.Query().Get("org_id"),
		}
		if from := r.URL.Query().Get("from"); from != "" {
			if v, err := strconv.ParseInt(from, 10, 64); err == nil {
				q.From = v
			}
		}
		if to := r.URL.Query().Get("to"); to != "" {
			if v, err := strconv.ParseInt(to, 10, 64); err == nil {
				q.To = v
			}
		}
		caller := callerFromHeaders(r)
		results, err := deps.auditSvc.SearchForCaller(r.Context(), q, caller)
		if err != nil {
			if errors.Is(err, audit.ErrCrossTenantAccess) {
				writeJSON(w, http.StatusForbidden, errResponse(err.Error()))
				return
			}
			writeJSON(w, http.StatusInternalServerError, errResponse(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": results})
	})))

	mux.Handle("GET /v1/audit/export", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		format, err := audit.ParseFormat(r.URL.Query().Get("format"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errResponse(err.Error()))
			return
		}
		q := ports.AuditQuery{
			Capability: r.URL.Query().Get("capability"),
			CallerType: r.URL.Query().Get("caller_type"),
		}
		if from := r.URL.Query().Get("from"); from != "" {
			if v, err := strconv.ParseInt(from, 10, 64); err == nil {
				q.From = v
			}
		}
		if to := r.URL.Query().Get("to"); to != "" {
			if v, err := strconv.ParseInt(to, 10, 64); err == nil {
				q.To = v
			}
		}
		var redactor *audit.Redactor
		if r.URL.Query().Get("redact") == "true" {
			redactor = audit.NewDefaultRedactor()
		}
		exporter := audit.NewExporter(deps.repos.Audit, redactor)
		switch format {
		case audit.FormatJSON:
			w.Header().Set("Content-Type", "application/json")
		case audit.FormatCSV:
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", `attachment; filename="praxis-audit.csv"`)
		}
		if err := exporter.Export(r.Context(), w, format, q); err != nil {
			deps.logger.Error().Err(err).Msg("audit export")
		}
	})))

	mux.Handle("GET /v1/audit/{action_id}", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("action_id")
		caller := callerFromHeaders(r)
		results, err := deps.auditSvc.ListForActionByCaller(r.Context(), id, caller)
		if err != nil {
			if errors.Is(err, audit.ErrCrossTenantAccess) {
				writeJSON(w, http.StatusForbidden, errResponse(err.Error()))
				return
			}
			writeJSON(w, http.StatusInternalServerError, errResponse(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"action_id": id, "events": results})
	})))

	mux.Handle("GET /v1/plugins", traced(authed(func(w http.ResponseWriter, _ *http.Request) {
		if deps.pluginManager == nil {
			writeJSON(w, http.StatusOK, map[string]any{"plugins": []any{}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plugins": pluginViews(deps.pluginManager.Snapshot())})
	})))

	mux.Handle("POST /v1/plugins/{name}/reload", traced(authed(func(w http.ResponseWriter, r *http.Request) {
		if deps.pluginManager == nil {
			writeJSON(w, http.StatusServiceUnavailable, errResponse("plugin manager not configured"))
			return
		}
		name := r.PathValue("name")
		if err := deps.pluginManager.ReloadOne(r.Context(), name); err != nil {
			if errors.Is(err, plugin.ErrPluginNotLoaded) {
				writeJSON(w, http.StatusNotFound, errResponse(err.Error()))
				return
			}
			writeJSON(w, http.StatusInternalServerError, errResponse(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "reloaded": true})
	})))

	return mux
}

// pluginView is the wire shape returned by GET /v1/plugins. Mirrors the
// fields a CLI table renderer needs: name, version, ABI, and the
// artefact digest so operators can confirm hosts are running the same
// binary without diffing content.
type pluginView struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	ABI         string `json:"abi"`
	Dir         string `json:"dir"`
	ArtifactSHA string `json:"artifact_sha256"`
}

func pluginViews(events []plugin.LoadEvent) []pluginView {
	out := make([]pluginView, 0, len(events))
	for _, ev := range events {
		out = append(out, pluginView{
			Name:        ev.Name,
			Version:     ev.Version,
			ABI:         ev.ABI,
			Dir:         ev.Dir,
			ArtifactSHA: ev.ArtifactSHA,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errResponse(msg string) map[string]any {
	return map[string]any{"error": msg}
}

// callerFromHeaders builds a CallerRef from optional X-Praxis-* headers so
// tenant-private capabilities resolve correctly on read endpoints (Phase 3
// M3.3). Empty headers degrade to an anonymous (global-only) caller.
func callerFromHeaders(r *http.Request) domain.CallerRef {
	return domain.CallerRef{
		Type:   r.Header.Get("X-Praxis-Caller-Type"),
		ID:     r.Header.Get("X-Praxis-Caller-ID"),
		Name:   r.Header.Get("X-Praxis-Caller-Name"),
		OrgID:  r.Header.Get("X-Praxis-Org-ID"),
		TeamID: r.Header.Get("X-Praxis-Team-ID"),
	}
}

func decodeAction(r *http.Request) (domain.Action, error) {
	var a domain.Action
	if r.Body == nil {
		return a, fmt.Errorf("empty body")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, fmt.Errorf("decode body: %w", err)
	}
	if a.ID == "" {
		a.ID = generateID()
	}
	if a.IdempotencyKey == "" {
		a.IdempotencyKey = a.ID
	}
	a.Status = domain.StatusPending
	a.CreatedAt = time.Now()
	a.UpdatedAt = a.CreatedAt
	return a, nil
}

func httpStatusForExecError(err error, res domain.Result) int {
	if res.Status == domain.StatusRejected {
		switch {
		case strings.HasPrefix(err.Error(), "unknown_capability"):
			return http.StatusNotFound
		case strings.HasPrefix(err.Error(), "validation_failed"):
			return http.StatusBadRequest
		case strings.HasPrefix(err.Error(), "policy_denied"):
			return http.StatusForbidden
		default:
			return http.StatusBadRequest
		}
	}
	if res.Status == domain.StatusFailed {
		if res.Error != nil && res.Error.Retryable {
			return http.StatusServiceUnavailable
		}
		return http.StatusInternalServerError
	}
	return http.StatusInternalServerError
}

func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("act-%d", time.Now().UnixNano())
	}
	return "act-" + hex.EncodeToString(b)
}
