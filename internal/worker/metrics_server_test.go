package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping() error { return f.err }

// TestWorkerHealthz_C02 covers the /healthz probe the chart's worker liveness/
// readiness checks hit: 200 when Redis is reachable, 503 when the asynq ping
// fails.
func TestWorkerHealthz_C02(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		srv := httptest.NewServer(newWorkerMux(fakePinger{}))
		defer srv.Close()
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatalf("get /healthz: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("healthy /healthz = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("redis_down", func(t *testing.T) {
		srv := httptest.NewServer(newWorkerMux(fakePinger{err: errors.New("dial tcp: connection refused")}))
		defer srv.Close()
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatalf("get /healthz: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("redis-down /healthz = %d, want 503", resp.StatusCode)
		}
	})
}

func TestStartMetricsServerServesMetrics(t *testing.T) {
	oldInstanceID := observability.InstanceID()
	observability.SetInstanceID("test-worker-instance")
	t.Cleanup(func() {
		observability.SetInstanceID(oldInstanceID)
	})

	registerQueueMetrics()
	updateQueueMetrics([]*asynq.QueueInfo{
		{
			Queue:   "default",
			Pending: 2,
			Active:  1,
			Size:    3,
			Latency: 5 * time.Second,
		},
	})
	handler := instrumentTask("reconcile.cluster", func(context.Context, *asynq.Task) error {
		return nil
	})
	if err := handler(context.Background(), asynq.NewTask("reconcile.cluster", nil)); err != nil {
		t.Fatalf("instrumented task returned error: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- StartMetricsServer(ctx, addr, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				t.Fatalf("read metrics body: %v", readErr)
			}
			text := string(body)
			for _, needle := range []string{
				"astronomer_worker_jobs_total",
				"astronomer_worker_job_duration_seconds",
				"astronomer_worker_queue_depth",
				"astronomer_worker_queue_latency_seconds",
				`astronomer_instance_id="test-worker-instance"`,
				`job="reconcile.cluster"`,
				`queue="default"`,
			} {
				if !strings.Contains(text, needle) {
					t.Fatalf("expected metrics scrape to contain %q", needle)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("metrics server did not become ready: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("StartMetricsServer() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("metrics server did not shut down")
	}
}
