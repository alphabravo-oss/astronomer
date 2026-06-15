package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestStartMetricsServerServesMetrics(t *testing.T) {
	oldInstanceID := observability.InstanceID()
	observability.SetInstanceID("test-server-instance")
	t.Cleanup(func() {
		observability.SetInstanceID(oldInstanceID)
	})

	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected health status %d, got %d", http.StatusOK, rec.Code)
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
		errCh <- StartMetricsServer(ctx, addr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get("http://" + addr)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				t.Fatalf("read metrics body: %v", readErr)
			}
			text := string(body)
			for _, needle := range []string{
				"astronomer_http_requests_total",
				"astronomer_http_request_duration_seconds",
				"astronomer_http_in_flight_requests",
				`astronomer_instance_id="test-server-instance"`,
				// chi normalizes the trailing slash; both `r.Get("/health", …)`
				// and `r.Get("/health/", …)` land under the "/health" pattern.
				`route_template="/health"`,
				`status_class="2xx"`,
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
