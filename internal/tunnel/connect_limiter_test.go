package tunnel

import (
	"testing"
	"time"
)

func TestConnectFailureLimiterThrottlesAfterN(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewConnectFailureLimiter(3, time.Minute, func() time.Time { return base })

	const ip = "203.0.113.7"
	// Below the threshold: never blocked.
	for i := 0; i < 2; i++ {
		lim.Fail(ip)
		if blocked, _ := lim.Blocked(ip); blocked {
			t.Fatalf("blocked after %d failures, want not blocked below limit 3", i+1)
		}
	}
	// Hitting the threshold blocks with a positive retry-after.
	lim.Fail(ip)
	blocked, retry := lim.Blocked(ip)
	if !blocked {
		t.Fatal("expected blocked at the failure threshold")
	}
	if retry <= 0 {
		t.Fatalf("expected positive retry-after at threshold, got %v", retry)
	}
	// A different IP is unaffected — per-IP isolation.
	if blocked, _ := lim.Blocked("198.51.100.9"); blocked {
		t.Fatal("an unrelated IP must not be throttled")
	}
}

func TestConnectFailureLimiterResetClearsHistory(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewConnectFailureLimiter(3, time.Minute, func() time.Time { return base })

	const ip = "203.0.113.7"
	for i := 0; i < 3; i++ {
		lim.Fail(ip)
	}
	if blocked, _ := lim.Blocked(ip); !blocked {
		t.Fatal("precondition: expected blocked before reset")
	}
	// A successful connect resets the IP — the healthy-agent guarantee.
	lim.Reset(ip)
	if blocked, _ := lim.Blocked(ip); blocked {
		t.Fatal("expected not blocked after Reset")
	}
	if _, ok := lim.buckets[ip]; ok {
		t.Fatal("Reset must delete the bucket entry, not just zero it")
	}
}

func TestConnectFailureLimiterWindowRollAndEvict(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	lim := NewConnectFailureLimiter(3, time.Minute, clock)

	const ip = "203.0.113.7"
	for i := 0; i < 3; i++ {
		lim.Fail(ip)
	}
	if blocked, _ := lim.Blocked(ip); !blocked {
		t.Fatal("precondition: blocked within window")
	}

	// Advance past the window: the stale bucket no longer blocks.
	now = now.Add(2 * time.Minute)
	if blocked, _ := lim.Blocked(ip); blocked {
		t.Fatal("expected not blocked after window rolled over")
	}
	// evictExpired reaps the stale bucket so the map stays bounded.
	if n := lim.evictExpired(); n != 1 {
		t.Fatalf("evictExpired removed %d buckets, want 1", n)
	}
	if len(lim.buckets) != 0 {
		t.Fatalf("expected empty bucket map after eviction, got %d", len(lim.buckets))
	}
	// A fresh failure after the roll starts a brand-new window (not blocked).
	lim.Fail(ip)
	if blocked, _ := lim.Blocked(ip); blocked {
		t.Fatal("a single failure in a fresh window must not block")
	}
}

func TestConnectFailureLimiterDefaults(t *testing.T) {
	lim := NewConnectFailureLimiter(0, 0, nil)
	if lim.limit != 50 {
		t.Fatalf("limit default = %d, want 50", lim.limit)
	}
	if lim.window != 5*time.Minute {
		t.Fatalf("window default = %v, want 5m", lim.window)
	}
	if lim.now == nil {
		t.Fatal("now must default to a non-nil clock")
	}
}

func TestConnectFailureLimiterNilSafe(t *testing.T) {
	var lim *ConnectFailureLimiter
	if blocked, _ := lim.Blocked("x"); blocked {
		t.Fatal("nil limiter must report not-blocked")
	}
	lim.Fail("x")  // must not panic
	lim.Reset("x") // must not panic
}
