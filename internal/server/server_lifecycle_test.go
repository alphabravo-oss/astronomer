package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

func TestShutdownHooksRunInRegistrationOrderAndAggregateErrors(t *testing.T) {
	srv := New(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var order []string
	srv.AddShutdownHook("audit", func(context.Context) error {
		order = append(order, "audit")
		return errors.New("flush failed")
	})
	srv.AddShutdownHook("telemetry", func(context.Context) error {
		order = append(order, "telemetry")
		return nil
	})

	err := srv.Shutdown(t.Context())
	if !reflect.DeepEqual(order, []string{"audit", "telemetry"}) {
		t.Fatalf("hook order = %v", order)
	}
	if err == nil || !strings.Contains(err.Error(), "audit: flush failed") {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestAddShutdownHookIgnoresNil(t *testing.T) {
	srv := New(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.AddShutdownHook("nil", nil)
	if err := srv.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

type injectedTunnelWorker struct {
	started  chan struct{}
	exit     chan struct{}
	err      error
	stopOnce sync.Once
}

func newInjectedTunnelWorker(err error) *injectedTunnelWorker {
	return &injectedTunnelWorker{started: make(chan struct{}), exit: make(chan struct{}), err: err}
}

func (w *injectedTunnelWorker) Run(context.Context) error {
	close(w.started)
	<-w.exit
	return w.err
}

func (w *injectedTunnelWorker) Shutdown() { w.stopOnce.Do(func() { close(w.exit) }) }

func TestCriticalWorkerCrashTerminatesServer(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(&config.Config{}, log)
	srv.runtime = newRuntimeSupervisor(log)
	if err := srv.runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	crash := errors.New("injected worker crash")
	worker := newInjectedTunnelWorker(crash)
	srv.tunnelWorker = worker
	result := make(chan error, 1)
	go func() { result <- srv.Start("127.0.0.1:0") }()
	select {
	case <-worker.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	worker.Shutdown()
	select {
	case err := <-result:
		if !errors.Is(err, crash) {
			t.Fatalf("Start() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not surface critical worker crash")
	}
	if srv.runtime.Healthy() {
		t.Fatal("runtime remained healthy after worker crash")
	}
	_ = srv.Shutdown(t.Context())
}

func TestWorkerExitDuringShutdownIsIntentional(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(&config.Config{}, log)
	srv.runtime = newRuntimeSupervisor(log)
	if err := srv.runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	worker := newInjectedTunnelWorker(nil)
	srv.tunnelWorker = worker
	result := make(chan error, 1)
	go func() { result <- srv.Start("127.0.0.1:0") }()
	select {
	case <-worker.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	if err := srv.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Start() during normal shutdown = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server listener did not stop")
	}
	select {
	case err := <-srv.runtime.Failures():
		t.Fatalf("intentional worker exit published failure: %v", err)
	default:
	}
}

func TestShutdownJoinsRuntimeBeforeDrainAndIsIdempotent(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(&config.Config{}, log)
	srv.runtime = newRuntimeSupervisor(log)
	if err := srv.runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	loopStopped := make(chan struct{})
	if err := srv.runtime.GoLoop("publisher", true, func(ctx context.Context) {
		<-ctx.Done()
		close(loopStopped)
	}); err != nil {
		t.Fatal(err)
	}
	hookCalls := 0
	srv.AddShutdownHook("audit", func(context.Context) error {
		hookCalls++
		select {
		case <-loopStopped:
			return nil
		default:
			return errors.New("runtime was not joined before audit drain")
		}
	})
	if err := srv.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := srv.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hookCalls != 1 {
		t.Fatalf("shutdown hook calls = %d, want 1", hookCalls)
	}
}

func TestShutdownDeadlineLeavesDependenciesOpenForHungLoop(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(&config.Config{}, log)
	srv.runtime = newRuntimeSupervisor(log)
	if err := srv.runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	if err := srv.runtime.Go("hung-publisher", true, func(context.Context) error {
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hookCalled, resourceClosed := false, false
	srv.AddShutdownHook("audit", func(context.Context) error {
		hookCalled = true
		return nil
	})
	srv.addResourceCloser("database", func(context.Context) error {
		resourceClosed = true
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := srv.Shutdown(ctx)
	if err == nil || !strings.Contains(err.Error(), "hung-publisher") {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if !hookCalled {
		t.Fatal("drain hook did not run while dependencies were live")
	}
	if resourceClosed {
		t.Fatal("dependency closed while a runtime loop was still running")
	}
	close(release)
}
