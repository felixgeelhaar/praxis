// Package limiter applies per-capability rate limits before any vendor
// side effect. Backed by fortify/ratelimit (token bucket). When a
// capability declares no RateLimit, Allow is a no-op.
//
// Keys are namespaced as `<capability>::<caller_type>:<caller_id>` so a
// burst from one user does not steal tokens from another. A
// capability-level fallback key (`<capability>::*`) catches unauthenticated
// callers.
package limiter

import (
	"context"
	"sync"
	"time"

	"github.com/felixgeelhaar/fortify/ratelimit"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

// Limiter holds one fortify limiter per capability that has a RateLimit
// declared. Lookups are cheap; missing capabilities short-circuit to allow.
type Limiter struct {
	mu       sync.RWMutex
	limiters map[string]ratelimit.RateLimiter
}

// New returns an empty Limiter. Capabilities are registered on demand the
// first time Allow is called for them.
func New() *Limiter {
	return &Limiter{limiters: make(map[string]ratelimit.RateLimiter)}
}

// Allow reports whether the action may proceed under the given capability's
// rate limit. Returns true (and a zero retry-after) when the capability has
// no limit configured.
func (l *Limiter) Allow(ctx context.Context, cap domain.Capability, action domain.Action) (allowed bool, retryAfter time.Duration) {
	if cap.RateLimit == nil || cap.RateLimit.Rate <= 0 {
		return true, 0
	}
	rl := l.getOrCreate(cap)
	key := callerKey(action)
	if rl.Allow(ctx, key) {
		return true, 0
	}
	return false, retryAfterFor(*cap.RateLimit)
}

func (l *Limiter) getOrCreate(cap domain.Capability) ratelimit.RateLimiter {
	l.mu.RLock()
	rl, ok := l.limiters[cap.Name]
	l.mu.RUnlock()
	if ok {
		return rl
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if rl, ok = l.limiters[cap.Name]; ok {
		return rl
	}

	rc := *cap.RateLimit
	burst := rc.Burst
	if burst <= 0 {
		burst = rc.Rate
	}
	interval := time.Duration(rc.Interval)
	if interval <= 0 {
		interval = time.Second
	}
	rl = ratelimit.New(&ratelimit.Config{
		Rate:     rc.Rate,
		Burst:    burst,
		Interval: interval,
	})
	l.limiters[cap.Name] = rl
	return rl
}

func callerKey(a domain.Action) string {
	if a.Caller.ID == "" {
		return "*"
	}
	return a.Caller.Type + ":" + a.Caller.ID
}

func retryAfterFor(rc domain.RateLimitConfig) time.Duration {
	interval := time.Duration(rc.Interval)
	if interval <= 0 {
		interval = time.Second
	}
	if rc.Rate <= 0 {
		return interval
	}
	// One token replenishes after Interval/Rate.
	return interval / time.Duration(rc.Rate)
}
