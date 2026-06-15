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

// TestInternalK8sHandler_ForbidsValidPSKWithoutSiblingSource is the H4
// defense-in-depth negative test: an external-looking request that carries
// a CORRECT PSK but lacks the in-band sibling-pod source marker
// (InternalSourceHeader) — exactly what an attacker reaching the handler
// through a misconfigured external ingress/catch-all would look like — is
// still rejected. The source marker is set only by sibling-pod requesters
// (k8s_requester.go), never by browser traffic crossing any shipped
// ingress, so a leaked-PSK attacker over an external route fails closed.
func TestInternalK8sHandler_ForbidsValidPSKWithoutSiblingSource(t *testing.T) {
	h := NewInternalK8sHandler(NewHub(slog.Default()), "the-right-psk", slog.Default())
	body, _ := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodDelete, Path: "/api/v1/namespaces/kube-system/pods/etcd"})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/c1", bytes.NewReader(body))
	// Valid PSK, but NO X-Astronomer-Internal-Source header.
	req.Header.Set(InternalPSKHeader, "the-right-psk")
	w := httptest.NewRecorder()
	k8sInternalHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for valid PSK without sibling-source marker, got %d: %s", w.Code, w.Body.String())
	}
}

// TestInternalK8sHandler_ForbidsForgedSiblingSource confirms the marker is
// compared exactly: a wrong value (e.g. a guessed/typo'd one) is rejected
// even with a valid PSK.
func TestInternalK8sHandler_ForbidsForgedSiblingSource(t *testing.T) {
	h := NewInternalK8sHandler(NewHub(slog.Default()), "the-right-psk", slog.Default())
	body, _ := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodGet, Path: "/api/v1/pods"})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/c1", bytes.NewReader(body))
	req.Header.Set(InternalPSKHeader, "the-right-psk")
	req.Header.Set(InternalSourceHeader, "not-the-marker")
	w := httptest.NewRecorder()
	k8sInternalHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for forged sibling-source marker, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInternalK8sHandler_ForbidsBadPSK(t *testing.T) {
	h := NewInternalK8sHandler(NewHub(slog.Default()), "the-right-psk", slog.Default())
	body, _ := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodGet, Path: "/api/v1/pods"})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/c1", bytes.NewReader(body))
	req.Header.Set(InternalSourceHeader, InternalSourceValue)
	req.Header.Set(InternalPSKHeader, "wrong")
	w := httptest.NewRecorder()
	k8sInternalHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on PSK mismatch, got %d", w.Code)
	}
}
