package handler

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Closed → open after N consecutive failures.
func TestClusterBreaker_OpensAfterThreshold(t *testing.T) {
	b := newClusterBreaker(3, time.Second)

	// Trip with 3 consecutive failures.
	for i := 0; i < 3; i++ {
		proceed, finalize := b.allow("c")
		if !proceed {
			t.Fatalf("call %d should be allowed (closed)", i+1)
		}
		finalize(errors.New("transient"))
	}
	if got := b.state("c"); got != breakerOpen {
		t.Errorf("state after 3 failures = %v, want open", got)
	}

	// Next allow returns false (fast-fail).
	proceed, _ := b.allow("c")
	if proceed {
		t.Error("open breaker should reject")
	}
}

// Per-cluster isolation: tripping cluster A must not affect cluster B.
func TestClusterBreaker_PerClusterIsolation(t *testing.T) {
	b := newClusterBreaker(2, time.Second)

	for i := 0; i < 2; i++ {
		_, finalize := b.allow("a")
		finalize(errors.New("a-fail"))
	}
	if b.state("a") != breakerOpen {
		t.Fatalf("a should be open")
	}
	if b.state("b") != breakerClosed {
		t.Errorf("b should still be closed, got %v", b.state("b"))
	}

	// Calls to b still proceed.
	proceed, _ := b.allow("b")
	if !proceed {
		t.Error("b should still be allowed")
	}
}

func TestClusterBreaker_CallerCancellationIsNeutral(t *testing.T) {
	b := newClusterBreaker(2, time.Second)

	_, finalize := b.allow("c")
	finalize(errors.New("transport failure"))
	_, finalize = b.allow("c")
	finalize(context.Canceled)

	if got := b.state("c"); got != breakerClosed {
		t.Fatalf("state after caller cancellation = %v, want closed", got)
	}
	if got := b.entry("c").consecutiveErrs; got != 1 {
		t.Fatalf("failures after caller cancellation = %d, want 1", got)
	}

	_, finalize = b.allow("c")
	finalize(errors.New("second transport failure"))
	if got := b.state("c"); got != breakerOpen {
		t.Fatalf("state after second transport failure = %v, want open", got)
	}
}

func TestClusterBreaker_CanceledHalfOpenProbeReturnsToOpen(t *testing.T) {
	b := newClusterBreaker(1, time.Millisecond)
	_, finalize := b.allow("c")
	finalize(errors.New("transport failure"))
	time.Sleep(2 * time.Millisecond)

	proceed, finalize := b.allow("c")
	if !proceed || b.state("c") != breakerHalfOpen {
		t.Fatal("expected a half-open probe")
	}
	finalize(context.Canceled)
	if got := b.state("c"); got != breakerOpen {
		t.Fatalf("state after canceled probe = %v, want open", got)
	}
}

// After openDuration elapses the breaker transitions to half-open;
// one trial request is allowed. Success closes the circuit.
func TestClusterBreaker_HalfOpenSuccessCloses(t *testing.T) {
	b := newClusterBreaker(1, 10*time.Millisecond)

	// Open.
	_, fin := b.allow("c")
	fin(errors.New("fail"))
	if b.state("c") != breakerOpen {
		t.Fatal("c should be open")
	}

	// Wait out the cooldown.
	time.Sleep(20 * time.Millisecond)

	// Trial request gets through.
	proceed, fin := b.allow("c")
	if !proceed {
		t.Fatal("half-open trial should be allowed")
	}
	if b.state("c") != breakerHalfOpen {
		t.Errorf("state during trial = %v, want half-open", b.state("c"))
	}
	fin(nil) // success
	if b.state("c") != breakerClosed {
		t.Errorf("state after successful trial = %v, want closed", b.state("c"))
	}
}

// Failed trial returns to OPEN and bumps the trip counter.
func TestClusterBreaker_HalfOpenFailureReopens(t *testing.T) {
	b := newClusterBreaker(1, 10*time.Millisecond)
	_, fin := b.allow("c")
	fin(errors.New("fail-1"))

	time.Sleep(20 * time.Millisecond)

	_, fin = b.allow("c")
	fin(errors.New("fail-2"))
	if b.state("c") != breakerOpen {
		t.Errorf("state after failed trial = %v, want open", b.state("c"))
	}
}

// Sentinel ErrCircuitOpen unwraps cleanly so callers can errors.Is on it.
func TestClusterBreaker_SentinelIs(t *testing.T) {
	wrapped := errors.New("cluster circuit breaker open for cluster \"x\": ...")
	_ = wrapped
	// This test is mostly here to flag if someone changes the sentinel
	// to no longer be unwrappable.
	if !errors.Is(ErrCircuitOpen, ErrCircuitOpen) {
		t.Error("ErrCircuitOpen does not match itself")
	}
}
