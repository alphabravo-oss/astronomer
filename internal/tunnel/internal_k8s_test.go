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
	r.Get("/internal/tunnel/k8s/{cluster_id}/capabilities/{capability}", h.HandleCapability)
	return r
}

func TestInternalK8sHandlerCapabilityUsesConnectAdmissionWithoutAgentMessage(t *testing.T) {
	hub := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: "c1",
		Capabilities: map[string]struct{}{
			"watch": {},
		},
		sendCh: make(chan *protocol.Message, 1),
	}
	hub.agents.Set(agent.ClusterID, agent)
	h := NewInternalK8sHandler(hub, InternalRequestKeyring{Current: "the-right-psk"}, slog.Default())

	for capability, want := range map[string]bool{"watch": true, protocol.AgentCapabilityMutate: false} {
		req := httptest.NewRequest(http.MethodGet, "/internal/tunnel/k8s/c1/capabilities/"+capability, nil)
		if err := SignInternalK8sRequest(req, "the-right-psk", "c1", nil); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		k8sInternalHandlerRouter(h).ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("capability %q status = %d: %s", capability, w.Code, w.Body.String())
		}
		var response InternalAgentCapabilityResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Supported != want {
			t.Fatalf("capability %q supported = %t, want %t", capability, response.Supported, want)
		}
	}
	if len(agent.sendCh) != 0 {
		t.Fatalf("capability lookup sent %d messages to agent, want 0", len(agent.sendCh))
	}
}

func TestInternalK8sHandler_SendFailureReturnsServiceUnavailable(t *testing.T) {
	hub := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: "c-send-full",
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, 1),
	}
	agent.sendCh <- &protocol.Message{Type: protocol.MsgHeartbeat}
	hub.agents.Set(agent.ClusterID, agent)
	h := NewInternalK8sHandler(hub, InternalRequestKeyring{Current: "the-right-psk"}, slog.Default())

	payload := protocol.K8sRequestPayload{Method: http.MethodGet, Path: "/api/v1/events"}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/"+agent.ClusterID, bytes.NewReader(body))
	if err := SignInternalK8sRequest(req, "the-right-psk", agent.ClusterID, body); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	k8sInternalHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("send failure status = %d, want 503: %s", w.Code, w.Body.String())
	}
}

func TestInternalK8sHandler_DisabledWhenPSKEmpty(t *testing.T) {
	h := NewInternalK8sHandler(NewHub(slog.Default()), InternalRequestKeyring{}, slog.Default())
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
	h := NewInternalK8sHandler(NewHub(slog.Default()), InternalRequestKeyring{Current: "the-right-psk"}, slog.Default())
	body, _ := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodDelete, Path: "/api/v1/namespaces/kube-system/pods/etcd"})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/c1", bytes.NewReader(body))
	if err := SignInternalK8sRequest(req, "the-right-psk", "c1", body); err != nil {
		t.Fatal(err)
	}
	// Valid envelope, but NO X-Astronomer-Internal-Source header.
	req.Header.Del(InternalSourceHeader)
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
	h := NewInternalK8sHandler(NewHub(slog.Default()), InternalRequestKeyring{Current: "the-right-psk"}, slog.Default())
	body, _ := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodGet, Path: "/api/v1/pods"})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/c1", bytes.NewReader(body))
	if err := SignInternalK8sRequest(req, "the-right-psk", "c1", body); err != nil {
		t.Fatal(err)
	}
	req.Header.Set(InternalSourceHeader, "not-the-marker")
	w := httptest.NewRecorder()
	k8sInternalHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for forged sibling-source marker, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInternalK8sHandler_ForbidsBadPSK(t *testing.T) {
	h := NewInternalK8sHandler(NewHub(slog.Default()), InternalRequestKeyring{Current: "the-right-psk"}, slog.Default())
	body, _ := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodGet, Path: "/api/v1/pods"})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/c1", bytes.NewReader(body))
	if err := SignInternalK8sRequest(req, "wrong", "c1", body); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	k8sInternalHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on PSK mismatch, got %d", w.Code)
	}
}
