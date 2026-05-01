package limiter_test

import (
	"context"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/limiter"
)

func TestAllow_NoConfig_AlwaysAllows(t *testing.T) {
	l := limiter.New()
	cap := domain.Capability{Name: "x"}
	for i := 0; i < 100; i++ {
		ok, _ := l.Allow(context.Background(), cap, action("u-1"))
		if !ok {
			t.Fatalf("expected allow without rate limit (iter %d)", i)
		}
	}
}

func TestAllow_BurstThenRefuse(t *testing.T) {
	l := limiter.New()
	cap := domain.Capability{
		Name: "x",
		RateLimit: &domain.RateLimitConfig{
			Rate:     2,
			Burst:    2,
			Interval: int64(time.Second),
		},
	}
	a := action("u-1")
	if ok, _ := l.Allow(context.Background(), cap, a); !ok {
		t.Fatal("first allow should pass")
	}
	if ok, _ := l.Allow(context.Background(), cap, a); !ok {
		t.Fatal("second allow should pass (within burst)")
	}
	ok, retry := l.Allow(context.Background(), cap, a)
	if ok {
		t.Fatal("third allow should be refused")
	}
	if retry <= 0 {
		t.Errorf("retry-after should be > 0, got %s", retry)
	}
}

func TestAllow_PerCallerIsolation(t *testing.T) {
	l := limiter.New()
	cap := domain.Capability{
		Name:      "x",
		RateLimit: &domain.RateLimitConfig{Rate: 1, Burst: 1, Interval: int64(time.Second)},
	}
	if ok, _ := l.Allow(context.Background(), cap, action("u-1")); !ok {
		t.Fatal("u-1 first allow should pass")
	}
	if ok, _ := l.Allow(context.Background(), cap, action("u-2")); !ok {
		t.Fatal("u-2 first allow should pass — separate bucket")
	}
	if ok, _ := l.Allow(context.Background(), cap, action("u-1")); ok {
		t.Fatal("u-1 second allow should be refused")
	}
}

func TestAllow_DifferentCapabilitiesIndependent(t *testing.T) {
	l := limiter.New()
	capA := domain.Capability{Name: "a", RateLimit: &domain.RateLimitConfig{Rate: 1, Burst: 1, Interval: int64(time.Second)}}
	capB := domain.Capability{Name: "b", RateLimit: &domain.RateLimitConfig{Rate: 1, Burst: 1, Interval: int64(time.Second)}}
	a := action("u-1")
	if ok, _ := l.Allow(context.Background(), capA, a); !ok {
		t.Fatal("capA first allow should pass")
	}
	if ok, _ := l.Allow(context.Background(), capB, a); !ok {
		t.Fatal("capB first allow should pass — separate cap bucket")
	}
}

func action(id string) domain.Action {
	return domain.Action{Caller: domain.CallerRef{Type: "user", ID: id}}
}
