package agent

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type observedHealthContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *observedHealthContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

// Inspect only the legacy ServeHealth shutdown closure; never print stacks or
// infer that arbitrary application handlers have drained.
func healthShutdownWaiters() int {
	for size := 64 << 10; size <= 1<<20; size *= 2 {
		buffer := make([]byte, size)
		n := runtime.Stack(buffer, true)
		if n < len(buffer) {
			return bytes.Count(buffer[:n], []byte("(*HealthReporter).ServeHealth.func"))
		}
	}
	return -1
}

func joinHealthShutdownWaiters(t *testing.T, before int, deadline time.Time) {
	t.Helper()
	for time.Now().Before(deadline) {
		current := healthShutdownWaiters()
		if current >= 0 && current <= before {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("owned legacy health shutdown waiter did not exit within fixture cleanup")
}

func joinHealthFixture(t *testing.T, name string, done <-chan struct{}, deadline time.Time) {
	t.Helper()
	timer := time.NewTimer(max(0, time.Until(deadline)))
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Errorf("owned %s did not exit within fixture cleanup", name)
	}
}

func TestAgentHealthBindFailureDoesNotRetainShutdownWaiter(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	before := healthShutdownWaiters()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		joinHealthShutdownWaiters(t, before, time.Now().Add(time.Second))
	})
	if before < 0 {
		t.Fatal("bounded shutdown waiter inventory unavailable")
	}
	ctx := &observedHealthContext{Context: base, observed: make(chan struct{})}
	hr := NewHealthReporter(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), 30, 60)
	hr.ServeHealth(ctx, listener.Addr().String())
	select {
	case <-ctx.observed:
		t.Fatal("failed bind created a context waiter after ServeHealth returned")
	case <-time.After(200 * time.Millisecond):
	}
}

type healthStartHandler struct {
	slog.Handler
	started chan struct{}
	once    sync.Once
}

func (handler *healthStartHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "starting health server" {
		handler.once.Do(func() { close(handler.started) })
	}
	return handler.Handler.Handle(ctx, record)
}

type blockedHealthMetric struct {
	entered  chan struct{}
	release  chan struct{}
	exited   chan struct{}
	once     sync.Once
	exitOnce sync.Once
	desc     *prometheus.Desc
}

func (collector *blockedHealthMetric) Describe(out chan<- *prometheus.Desc) {
	out <- collector.desc
}

func (collector *blockedHealthMetric) Collect(out chan<- prometheus.Metric) {
	defer collector.exitOnce.Do(func() { close(collector.exited) })
	collector.once.Do(func() { close(collector.entered) })
	<-collector.release
	out <- prometheus.MustNewConstMetric(collector.desc, prometheus.GaugeValue, 1)
}

func TestAgentHealthCancellationJoinsDrainAndClosesBlockedScrape(t *testing.T) {
	collector := &blockedHealthMetric{entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{}),
		desc: prometheus.NewDesc("astronomer_health_owned_shutdown_fixture", "Owned shutdown fixture", nil, nil)}
	ctx, cancel := context.WithCancel(context.Background())
	requestCtx, requestCancel := context.WithCancel(context.Background())
	returned, clientDone := make(chan struct{}), make(chan struct{})
	transport := &http.Transport{}
	registered, serverLaunched, clientLaunched := false, false, false
	var reserved net.Listener
	var releaseOnce sync.Once
	before := healthShutdownWaiters()
	// Install before registration or any goroutine. Expected failures and
	// setup errors release/cancel every fixture owner and join them before
	// unregistering the collector. Server.Close alone proves no handler join.
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(collector.release) })
		cancel()
		requestCancel()
		if reserved != nil {
			_ = reserved.Close()
		}
		deadline := time.Now().Add(9 * time.Second)
		if serverLaunched {
			joinHealthFixture(t, "ServeHealth", returned, deadline)
		}
		if clientLaunched {
			joinHealthFixture(t, "HTTP client", clientDone, deadline)
			joinHealthFixture(t, "Prometheus collector", collector.exited, deadline)
		}
		joinHealthShutdownWaiters(t, before, deadline)
		transport.CloseIdleConnections()
		if registered {
			prometheus.Unregister(collector)
		}
	})
	if before < 0 {
		t.Fatal("bounded shutdown waiter inventory unavailable")
	}
	if err := prometheus.Register(collector); err != nil {
		t.Fatal(err)
	}
	registered = true
	var err error
	reserved, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.Addr().String()
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}
	reserved = nil
	logger := &healthStartHandler{Handler: slog.NewTextHandler(io.Discard, nil), started: make(chan struct{})}
	hr := NewHealthReporter(nil, slog.New(logger), 30, 60)
	serverLaunched = true
	go func() { hr.ServeHealth(ctx, address); close(returned) }()
	select {
	case <-logger.started:
	case <-time.After(time.Second):
		t.Fatal("health listener did not start")
	}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+address+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	clientLaunched = true
	go func() {
		defer close(clientDone)
		client := &http.Client{Timeout: 8 * time.Second, Transport: transport}
		response, _ := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-collector.entered:
	case <-time.After(time.Second):
		t.Fatal("actual metrics scrape did not enter the owned collector")
	}
	cancel()
	select {
	case <-returned:
		t.Fatal("ServeHealth returned before joining its blocked shutdown owner")
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-returned:
	case <-time.After(6 * time.Second):
		t.Fatal("bounded health shutdown did not return")
	}
	select {
	case <-clientDone:
	case <-time.After(time.Second):
		t.Fatal("failed graceful shutdown did not close the scrape connection")
	}
}
