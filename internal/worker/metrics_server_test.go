package worker

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

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
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- StartMetricsServer(ctx, addr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get("http://" + addr)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
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
