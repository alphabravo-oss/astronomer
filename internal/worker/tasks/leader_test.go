package tasks

import (
	"context"
	"errors"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type fakeLeader struct {
	held          bool
	err           error
	releaseCalled bool
}

func (f *fakeLeader) TryLeader(_ context.Context, _ string) (func(), bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	return func() { f.releaseCalled = true }, f.held, nil
}

func TestRunPeriodicTaskWithLeader_NoLeaderConfigured(t *testing.T) {
	resetRuntime()

	called := false
	if err := runPeriodicTaskWithLeader(context.Background(), "job", func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("runPeriodicTaskWithLeader: %v", err)
	}
	if !called {
		t.Fatal("expected wrapped function to run without leader elector")
	}
}

func TestRunPeriodicTaskWithLeader_NotHeldSkipsWork(t *testing.T) {
	defer resetRuntime()
	fl := &fakeLeader{held: false}
	ConfigureRuntime(RuntimeDependencies{Leader: fl})

	called := false
	if err := runPeriodicTaskWithLeader(context.Background(), "job", func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("runPeriodicTaskWithLeader: %v", err)
	}
	if called {
		t.Fatal("expected wrapped function to be skipped when leadership not held")
	}
	if fl.releaseCalled {
		t.Fatal("release should not be called when lock not held")
	}
	if got := gaugeValue(t, "job"); got != 0 {
		t.Fatalf("leader gauge = %v, want 0", got)
	}
}

func TestRunPeriodicTaskWithLeader_HeldRunsAndReleases(t *testing.T) {
	defer resetRuntime()
	fl := &fakeLeader{held: true}
	ConfigureRuntime(RuntimeDependencies{Leader: fl})

	called := false
	if err := runPeriodicTaskWithLeader(context.Background(), "job", func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("runPeriodicTaskWithLeader: %v", err)
	}
	if !called {
		t.Fatal("expected wrapped function to run when leadership is held")
	}
	if !fl.releaseCalled {
		t.Fatal("expected release to be called")
	}
	if got := gaugeValue(t, "job"); got != 0 {
		t.Fatalf("leader gauge after release = %v, want 0", got)
	}
}

func TestRunPeriodicTaskWithLeader_PropagatesAcquireError(t *testing.T) {
	defer resetRuntime()
	fl := &fakeLeader{err: errors.New("boom")}
	ConfigureRuntime(RuntimeDependencies{Leader: fl})

	err := runPeriodicTaskWithLeader(context.Background(), "job", func() error { return nil })
	if err == nil {
		t.Fatal("expected error")
	}
	if got := gaugeValue(t, "job"); got != 0 {
		t.Fatalf("leader gauge after error = %v, want 0", got)
	}
}

func gaugeValue(t *testing.T, job string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := workerLeaderHeld.WithLabelValues(observability.MetricValues(job)...).Write(m); err != nil {
		t.Fatalf("read gauge: %v", err)
	}
	if m.Gauge == nil || m.Gauge.Value == nil {
		t.Fatal("gauge value missing")
	}
	return m.GetGauge().GetValue()
}
