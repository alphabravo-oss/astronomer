package middleware

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

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
	logger := zerolog.New(&buf)

	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(RequestLogger(logger))
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
	if payload["route"] != "/items/{id}" || payload["method"] != http.MethodGet {
		t.Fatalf("route/method = %v %v, want GET /items/{id}", payload["method"], payload["route"])
	}
	if payload["status"] != float64(http.StatusCreated) {
		t.Fatalf("status = %v, want %d", payload["status"], http.StatusCreated)
	}
	if _, ok := payload["duration_ms"]; !ok {
		t.Fatalf("duration_ms missing: %v", payload)
	}
	if _, ok := payload["principal"]; ok {
		t.Fatalf("anonymous request must not carry a principal: %v", payload)
	}
}

func TestRequestLoggerEmitsActorClusterAndOperationIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)

	user := &reqctx.User{ID: "user-123", AuthMethod: "jwt"}
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(RequestLogger(logger))
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := reqctx.WithUser(r.Context(), user)
			setRequestLogActor(ctx, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	router.Post("/clusters/{cluster_id}/workloads/operations/{id}/retry/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	req := httptest.NewRequest(http.MethodPost, "/clusters/cluster-123/workloads/operations/op-456/retry/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if payload["principal"] != "user-123" {
		t.Fatalf("principal = %v, want user-123", payload["principal"])
	}
	if payload["auth_method"] != "jwt" {
		t.Fatalf("auth_method = %v, want jwt", payload["auth_method"])
	}
	if payload["cluster_id"] != "cluster-123" {
		t.Fatalf("cluster_id = %v, want cluster-123", payload["cluster_id"])
	}
	if payload["operation_id"] != "op-456" {
		t.Fatalf("operation_id = %v, want op-456", payload["operation_id"])
	}
}

func TestRequestLoggerPreservesHijacker(t *testing.T) {
	handler := RequestLogger(zerolog.Nop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
