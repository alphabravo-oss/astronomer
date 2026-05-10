package middleware

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

type hijackableRequestLogRecorder struct {
	*httptest.ResponseRecorder
}

func (w *hijackableRequestLogRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	serverConn, clientConn := net.Pipe()
	rw := bufio.NewReadWriter(bufio.NewReader(serverConn), bufio.NewWriter(serverConn))
	return clientConn, rw, nil
}

func (w *hijackableRequestLogRecorder) Flush() {}

func TestRequestLoggerEmitsStructuredHTTPEvent(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	slog.SetDefault(logger)
	defer slog.SetDefault(previous)

	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(RequestLogger)
	router.Get("/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodGet, "/items/123", nil)
	req.Header.Set("X-Correlation-Id", "corr-123")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if payload["event"] != "http_request" {
		t.Fatalf("event = %v, want http_request", payload["event"])
	}
	if payload["correlation_id"] != "corr-123" {
		t.Fatalf("correlation_id = %v, want corr-123", payload["correlation_id"])
	}
	if payload["route_template"] != "/items/{id}" {
		t.Fatalf("route_template = %v, want /items/{id}", payload["route_template"])
	}
	if payload["status_code"] != float64(http.StatusCreated) {
		t.Fatalf("status_code = %v, want %d", payload["status_code"], http.StatusCreated)
	}
}

func TestRequestLoggerPreservesHijacker(t *testing.T) {
	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("wrapped response writer does not implement http.Hijacker")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatalf("Hijack failed: %v", err)
		}
		_ = conn.Close()
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws/agent/tunnel/test/", nil)
	rec := &hijackableRequestLogRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(rec, req)
}
