package tunnel

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func k8sInternalHandlerRouter(h *InternalK8sHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/internal/tunnel/k8s/{cluster_id}", h.Handle)
	return r
}

func TestInternalK8sHandler_DisabledWhenPSKEmpty(t *testing.T) {
	h := NewInternalK8sHandler(NewHub(slog.Default()), "", slog.Default())
	body, _ := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodGet, Path: "/api/v1/pods"})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/c1", bytes.NewReader(body))
	w := httptest.NewRecorder()
	k8sInternalHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when psk empty, got %d", w.Code)
	}
}

func TestInternalK8sHandler_ForbidsBadPSK(t *testing.T) {
	h := NewInternalK8sHandler(NewHub(slog.Default()), "the-right-psk", slog.Default())
	body, _ := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodGet, Path: "/api/v1/pods"})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/c1", bytes.NewReader(body))
	req.Header.Set(InternalPSKHeader, "wrong")
	w := httptest.NewRecorder()
	k8sInternalHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on PSK mismatch, got %d", w.Code)
	}
}
