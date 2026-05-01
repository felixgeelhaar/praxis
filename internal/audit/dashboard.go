package audit

import (
	"context"
	"sort"
	"time"

	"github.com/felixgeelhaar/praxis/internal/ports"
)

// Dashboard summarises audit events into the metrics an operator needs at
// a glance: capability usage, policy denials, throttling, and error rates.
//
// All counts are scoped to the supplied AuditQuery — typically a time
// window (From / To) so a dashboard for "last 24h" stays bounded.
type Dashboard struct {
	WindowFrom      time.Time          `json:"window_from,omitempty"`
	WindowTo        time.Time          `json:"window_to,omitempty"`
	TotalEvents     int                `json:"total_events"`
	TotalActions    int                `json:"total_actions"`
	ByCapability    []CapabilityUsage  `json:"by_capability"`
	ByCallerType    map[string]int     `json:"by_caller_type"`
	PolicyDenials   int                `json:"policy_denials"`
	Throttled       int                `json:"throttled"`
	Failed          int                `json:"failed"`
	Succeeded       int                `json:"succeeded"`
	Simulated       int                `json:"simulated"`
	Compensated     int                `json:"compensated"`
	ErrorRatePerCap map[string]float64 `json:"error_rate_per_capability"`
}

// CapabilityUsage aggregates per-capability counts.
type CapabilityUsage struct {
	Capability string `json:"capability"`
	Total      int    `json:"total"`
	Succeeded  int    `json:"succeeded"`
	Failed     int    `json:"failed"`
	Rejected   int    `json:"rejected"`
	Simulated  int    `json:"simulated"`
	Throttled  int    `json:"throttled"`
}

// BuildDashboard scans audit events through the supplied query and rolls
// them up into a Dashboard. Implementation is in-memory aggregation —
// adequate for the typical Phase-1 audit volumes; a Phase-3 follow-up
// will push the rollup into the storage backend for very large windows.
func BuildDashboard(ctx context.Context, repo ports.AuditRepo, q ports.AuditQuery) (Dashboard, error) {
	events, err := repo.Search(ctx, q)
	if err != nil {
		return Dashboard{}, err
	}

	d := Dashboard{
		ByCallerType:    map[string]int{},
		ErrorRatePerCap: map[string]float64{},
	}
	if q.From > 0 {
		d.WindowFrom = time.Unix(q.From, 0).UTC()
	}
	if q.To > 0 {
		d.WindowTo = time.Unix(q.To, 0).UTC()
	}
	d.TotalEvents = len(events)

	// Roll up per capability and per caller. We count one Action per ID
	// and aggregate terminal kinds from there so a chatty action with many
	// audit rows doesn't double-count.
	byAction := map[string]aggAction{}
	for _, e := range events {
		cap, _ := e.Detail["capability"].(string)
		callerType, _ := e.Detail["caller_type"].(string)
		if callerType != "" {
			d.ByCallerType[callerType]++
		}

		a, ok := byAction[e.ActionID]
		if !ok {
			a = aggAction{capability: cap}
		}
		switch e.Kind {
		case KindSucceeded:
			a.terminal = "succeeded"
		case KindFailed:
			a.terminal = "failed"
		case KindSimulated:
			a.terminal = "simulated"
		case KindRejected:
			a.terminal = "rejected"
			if c, _ := e.Detail["code"].(string); c == "policy_denied" {
				a.policyDenied = true
			}
		case KindThrottled:
			a.throttled = true
		case KindCompensated:
			a.compensated = true
		}
		byAction[e.ActionID] = a
	}

	caps := map[string]*CapabilityUsage{}
	for _, a := range byAction {
		d.TotalActions++
		c, ok := caps[a.capability]
		if !ok {
			c = &CapabilityUsage{Capability: a.capability}
			caps[a.capability] = c
		}
		c.Total++
		switch a.terminal {
		case "succeeded":
			c.Succeeded++
			d.Succeeded++
		case "failed":
			c.Failed++
			d.Failed++
		case "simulated":
			c.Simulated++
			d.Simulated++
		case "rejected":
			c.Rejected++
		}
		if a.policyDenied {
			d.PolicyDenials++
		}
		if a.throttled {
			c.Throttled++
			d.Throttled++
		}
		if a.compensated {
			d.Compensated++
		}
	}

	d.ByCapability = make([]CapabilityUsage, 0, len(caps))
	for _, c := range caps {
		d.ByCapability = append(d.ByCapability, *c)
		if c.Total > 0 {
			d.ErrorRatePerCap[c.Capability] = float64(c.Failed) / float64(c.Total)
		}
	}
	sort.Slice(d.ByCapability, func(i, j int) bool {
		return d.ByCapability[i].Total > d.ByCapability[j].Total
	})

	return d, nil
}

type aggAction struct {
	capability   string
	terminal     string
	policyDenied bool
	throttled    bool
	compensated  bool
}
