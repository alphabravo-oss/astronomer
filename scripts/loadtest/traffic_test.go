package main

import (
	"testing"
	"time"
)

func TestNewWorkloadLimiterDoesNotReleaseStartupBurst(t *testing.T) {
	t.Parallel()

	limiter := newWorkloadLimiter(500)
	if got := limiter.Burst(); got != 1 {
		t.Fatalf("workload limiter burst = %d, want 1", got)
	}

	now := time.Now()
	if !limiter.AllowN(now, 1) {
		t.Fatal("workload limiter rejected the first request")
	}
	if limiter.AllowN(now, 1) {
		t.Fatal("workload limiter released an undeclared second startup request")
	}
}
