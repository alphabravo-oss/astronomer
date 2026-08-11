package charlie

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type lifecycleFeature struct{ enabled atomic.Bool }

func (f *lifecycleFeature) BoolValue(context.Context, string, bool) bool { return f.enabled.Load() }

type lifecycleConnection struct {
	mu    sync.Mutex
	row   sqlc.CharlieConnection
	reads atomic.Int32
}

func (c *lifecycleConnection) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	c.reads.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.row, nil
}

func (c *lifecycleConnection) setModes(requested, verified Mode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.row.RequestedMode, c.row.VerifiedMode = string(requested), string(verified)
}

type lifecycleWork struct {
	runs      atomic.Int32
	stops     atomic.Int32
	runExited chan struct{}
}

func (w *lifecycleWork) Run(ctx context.Context) {
	w.runs.Add(1)
	<-ctx.Done()
	close(w.runExited)
}

func (w *lifecycleWork) Shutdown(context.Context) error {
	w.stops.Add(1)
	return nil
}

func runnableLifecycleFixture(t *testing.T) (*RuntimeLifecycle, *lifecycleFeature, *lifecycleConnection, *lifecycleWork, chan time.Time, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	features := &lifecycleFeature{}
	connection := &lifecycleConnection{row: sqlc.CharlieConnection{
		ID: uuid.New(), Active: true, OnboardingState: "active",
		RequestedMode: string(ModeReadOnly), VerifiedMode: string(ModeReadOnly),
	}}
	work := &lifecycleWork{runExited: make(chan struct{})}
	factories, closes := &atomic.Int32{}, &atomic.Int32{}
	lifecycle, err := NewRuntimeLifecycle(features, connection, func(context.Context) (ActivationWork, error) {
		factories.Add(1)
		return work, nil
	}, nil, func() { closes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time, 1)
	lifecycle.ticker = func(time.Duration) runtimeTicker { return &fakeRuntimeTicker{channel: ticks} }
	return lifecycle, features, connection, work, ticks, factories, closes
}

func TestRuntimeLifecycleColdDisabledCreatesNoWorkOrTimer(t *testing.T) {
	for _, test := range []struct {
		name      string
		mutate    func(*lifecycleFeature, *lifecycleConnection)
		wantReads int32
	}{
		{name: "feature disabled", mutate: func(*lifecycleFeature, *lifecycleConnection) {}, wantReads: 0},
		{name: "connection inactive", mutate: func(feature *lifecycleFeature, connection *lifecycleConnection) {
			feature.enabled.Store(true)
			connection.mu.Lock()
			connection.row.Active = false
			connection.mu.Unlock()
		}, wantReads: 1},
		{name: "wire mode disabled", mutate: func(feature *lifecycleFeature, connection *lifecycleConnection) {
			feature.enabled.Store(true)
			connection.setModes(ModeDisabled, ModeDisabled)
		}, wantReads: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			lifecycle, feature, connection, work, _, factories, closes := runnableLifecycleFixture(t)
			test.mutate(feature, connection)
			timers := atomic.Int32{}
			lifecycle.ticker = func(time.Duration) runtimeTicker {
				timers.Add(1)
				return &fakeRuntimeTicker{channel: make(chan time.Time)}
			}
			if err := lifecycle.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			if factories.Load() != 0 || timers.Load() != 0 || work.runs.Load() != 0 || work.stops.Load() != 0 {
				t.Fatalf("factory=%d timers=%d runs=%d stops=%d", factories.Load(), timers.Load(), work.runs.Load(), work.stops.Load())
			}
			if got := connection.reads.Load(); got != test.wantReads {
				t.Fatalf("connection reads=%d want=%d", got, test.wantReads)
			}
			if closes.Load() != 1 {
				t.Fatalf("inactive startup did not close stale transport: %d", closes.Load())
			}
		})
	}
}

func TestRuntimeLifecycleDynamicEnableAndModeFallQuiesceGeneration(t *testing.T) {
	lifecycle, features, connection, work, ticks, factories, closes := runnableLifecycleFixture(t)
	controlStarted, controlExited := make(chan struct{}), make(chan struct{})
	lifecycle.control = func(ctx context.Context) {
		close(controlStarted)
		<-ctx.Done()
		close(controlExited)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := lifecycle.Start(parent); err != nil {
		t.Fatal(err)
	}
	features.enabled.Store(true)
	if err := lifecycle.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for work.runs.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if factories.Load() != 1 || work.runs.Load() != 1 {
		t.Fatalf("factory=%d runs=%d", factories.Load(), work.runs.Load())
	}
	select {
	case <-controlStarted:
	case <-time.After(time.Second):
		t.Fatal("mode reconciler did not start with the runnable generation")
	}

	connection.setModes(ModeDisabled, ModeDisabled)
	ticks <- time.Now()
	select {
	case <-work.runExited:
	case <-time.After(time.Second):
		t.Fatal("mode fall did not cancel the running work context")
	}
	select {
	case <-controlExited:
	case <-time.After(time.Second):
		t.Fatal("mode fall did not cancel the mode reconciler context")
	}
	if work.stops.Load() != 1 || closes.Load() < 2 {
		t.Fatalf("stops=%d transport_closes=%d", work.stops.Load(), closes.Load())
	}
}

func TestConfigurationRuntimeLifecycleServesOperationalDisabledButStopsForEmergency(t *testing.T) {
	features := &lifecycleFeature{}
	features.enabled.Store(true)
	connection := &lifecycleConnection{row: sqlc.CharlieConnection{
		ID: uuid.New(), Active: true, OnboardingState: "active",
		RequestedMode: string(ModeDisabled), VerifiedMode: string(ModeDisabled),
	}}
	work := &lifecycleWork{runExited: make(chan struct{})}
	factories := &atomic.Int32{}
	lifecycle, err := NewConfigurationRuntimeLifecycle(features, connection, func(context.Context) (ActivationWork, error) {
		factories.Add(1)
		return work, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time, 1)
	lifecycle.ticker = func(time.Duration) runtimeTicker { return &fakeRuntimeTicker{channel: ticks} }
	if err := lifecycle.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for work.runs.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if factories.Load() != 1 || work.runs.Load() != 1 {
		t.Fatalf("configuration factory=%d runs=%d", factories.Load(), work.runs.Load())
	}
	connection.mu.Lock()
	connection.row.EmergencyDisabled = true
	connection.mu.Unlock()
	ticks <- time.Now()
	select {
	case <-work.runExited:
	case <-time.After(time.Second):
		t.Fatal("emergency stop did not close configuration discovery")
	}
	if work.stops.Load() != 1 {
		t.Fatalf("configuration stops=%d", work.stops.Load())
	}
}

func TestRuntimeLifecycleConcurrentDisableDoesNotPublishLateWork(t *testing.T) {
	features := &lifecycleFeature{}
	features.enabled.Store(true)
	connection := &lifecycleConnection{row: sqlc.CharlieConnection{
		ID: uuid.New(), Active: true, OnboardingState: "active",
		RequestedMode: string(ModeReadOnly), VerifiedMode: string(ModeReadOnly),
	}}
	started, release := make(chan struct{}), make(chan struct{})
	work := &lifecycleWork{runExited: make(chan struct{})}
	lifecycle, err := NewRuntimeLifecycle(features, connection, func(context.Context) (ActivationWork, error) {
		close(started)
		<-release
		return work, nil
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	lifecycle.mu.Lock()
	lifecycle.parent = parent
	lifecycle.mu.Unlock()
	enabled := make(chan error, 1)
	go func() { enabled <- lifecycle.Activate(context.Background()) }()
	<-started
	disabled := make(chan error, 1)
	go func() { disabled <- lifecycle.Shutdown(context.Background()) }()
	features.enabled.Store(false)
	close(release)
	if err := <-enabled; err != nil {
		t.Fatal(err)
	}
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	if work.runs.Load() > 1 || work.stops.Load() != 1 {
		t.Fatalf("runs=%d stops=%d", work.runs.Load(), work.stops.Load())
	}
}
