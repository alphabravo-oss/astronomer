package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSupervisorCriticalExitFailsHealth(t *testing.T) {
	s := newRuntimeSupervisor(nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := errors.New("worker crashed")
	if err := s.Go("tunnel-worker", true, func(context.Context) error { return want }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-s.Failures():
		if !strings.Contains(err.Error(), "tunnel-worker") || !errors.Is(err, want) {
			t.Fatalf("failure = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("critical failure was not published")
	}
	if s.Healthy() {
		t.Fatal("supervisor remained healthy after critical exit")
	}
	if got := s.HealthError(); !strings.Contains(got, "worker crashed") {
		t.Fatalf("health error = %q", got)
	}
}

func TestRuntimeSupervisorIntentionalStopJoinsLoop(t *testing.T) {
	s := newRuntimeSupervisor(nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	if err := s.Go("reconciler", true, func(ctx context.Context) error {
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("loop was not joined")
	}
	select {
	case err := <-s.Failures():
		t.Fatalf("intentional stop published failure: %v", err)
	default:
	}
}

func TestRuntimeSupervisorStopReportsHungLoop(t *testing.T) {
	s := newRuntimeSupervisor(nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	if err := s.Go("hung", true, func(context.Context) error {
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := s.Stop(ctx)
	if err == nil || !strings.Contains(err.Error(), "hung") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v", err)
	}
	close(release)
}
