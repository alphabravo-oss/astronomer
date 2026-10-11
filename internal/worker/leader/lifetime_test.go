package leader

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLeadershipLossCancelsAndJoinsWork(t *testing.T) {
	joined := false
	err := supervise(t.Context(), time.Millisecond, func(context.Context) error {
		return errors.New("database session terminated")
	}, func(ctx context.Context) error {
		<-ctx.Done()
		if !errors.Is(context.Cause(ctx), ErrLeadershipLost) {
			t.Errorf("cancellation cause = %v", context.Cause(ctx))
		}
		joined = true
		return ctx.Err()
	})
	if !errors.Is(err, ErrLeadershipLost) || !joined {
		t.Fatalf("err=%v joined=%v", err, joined)
	}
}

func TestLeadershipProbeTimeoutCancelsWork(t *testing.T) {
	err := supervise(t.Context(), time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, ErrLeadershipLost) {
		t.Fatalf("expected leadership loss, got %v", err)
	}
}

func TestLeadershipJoinsProbeBeforeReturning(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	want := errors.New("reconciler failed")
	err := supervise(t.Context(), time.Millisecond, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil
	}, func(context.Context) error {
		<-started
		return want
	})
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("returned before session probe stopped")
	}
}
