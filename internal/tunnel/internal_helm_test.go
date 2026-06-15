package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// helmHandlerRouter wraps the handler in a chi router so URLParam lookups
// resolve (same pattern the proxy tests use).
func helmHandlerRouter(h *InternalHelmHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/internal/tunnel/helm/{cluster_id}", h.Handle)
	return r
}

func TestInternalHelmHandler_DisabledWhenPSKEmpty(t *testing.T) {
	h := NewInternalHelmHandler(NewHub(slog.Default()), "", slog.Default())
	body, _ := json.Marshal(InternalHelmRequest{MsgType: protocol.MsgHelmInstall})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/helm/c1", bytes.NewReader(body))
	w := httptest.NewRecorder()
	helmHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when psk empty, got %d", w.Code)
	}
}

// TestInternalHelmHandler_ForbidsValidPSKWithoutSiblingSource is the H4
// defense-in-depth negative test for the helm door: a valid PSK without
// the in-band sibling-pod source marker (what an external caller reaching
// the handler through a misconfigured catch-all would present) is rejected.
func TestInternalHelmHandler_ForbidsValidPSKWithoutSiblingSource(t *testing.T) {
	h := NewInternalHelmHandler(NewHub(slog.Default()), "the-right-psk", slog.Default())
	body, _ := json.Marshal(InternalHelmRequest{MsgType: protocol.MsgHelmUninstall})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/helm/c1", bytes.NewReader(body))
	// Valid PSK, but NO X-Astronomer-Internal-Source header.
	req.Header.Set(InternalPSKHeader, "the-right-psk")
	w := httptest.NewRecorder()
	helmHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for valid PSK without sibling-source marker, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInternalHelmHandler_ForbidsBadPSK(t *testing.T) {
	h := NewInternalHelmHandler(NewHub(slog.Default()), "the-right-psk", slog.Default())
	body, _ := json.Marshal(InternalHelmRequest{MsgType: protocol.MsgHelmInstall})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/helm/c1", bytes.NewReader(body))
	req.Header.Set(InternalSourceHeader, InternalSourceValue)
	req.Header.Set(InternalPSKHeader, "wrong")
	w := httptest.NewRecorder()
	helmHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on PSK mismatch, got %d", w.Code)
	}
}

func TestInternalHelmHandler_RejectsBadMsgType(t *testing.T) {
	h := NewInternalHelmHandler(NewHub(slog.Default()), "psk", slog.Default())
	body, _ := json.Marshal(InternalHelmRequest{MsgType: protocol.MsgK8sRequest})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/helm/c1", bytes.NewReader(body))
	req.Header.Set(InternalSourceHeader, InternalSourceValue)
	req.Header.Set(InternalPSKHeader, "psk")
	w := httptest.NewRecorder()
	helmHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-helm msg_type, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInternalHelmHandler_NoAgentReturns503(t *testing.T) {
	hub := NewHub(slog.Default())
	h := NewInternalHelmHandler(hub, "psk", slog.Default())
	body, _ := json.Marshal(InternalHelmRequest{
		MsgType: protocol.MsgHelmInstall,
		Payload: protocol.HelmRequestPayload{ReleaseName: "rel", Namespace: "ns"},
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/helm/no-such-cluster", bytes.NewReader(body))
	req.Header.Set(InternalSourceHeader, InternalSourceValue)
	req.Header.Set(InternalPSKHeader, "psk")
	w := httptest.NewRecorder()
	helmHandlerRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no agent, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Cluster agent not connected") {
		t.Errorf("expected disconnect message, got %q", w.Body.String())
	}
}

// End-to-end happy path: a fake agent is registered in the hub, the
// handler dispatches HELM_INSTALL via SendToAgent, and a goroutine
// simulates the agent's HELM_RESULT response.
func TestInternalHelmHandler_RoundTrip(t *testing.T) {
	hub := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: "c-helm",
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
		cancel:    func() {},
	}
	hub.agents.Set("c-helm", agent)

	h := NewInternalHelmHandler(hub, "psk", slog.Default())

	// Drain the outbound message and reply via the stream the handler
	// just created.
	go func() {
		msg := <-agent.sendCh
		if msg.Type != protocol.MsgHelmInstall {
			t.Errorf("unexpected msg type forwarded to agent: %s", msg.Type)
			return
		}
		respBody, _ := json.Marshal(protocol.HelmResultPayload{
			Success:     true,
			ReleaseName: "rel",
			Namespace:   "ns",
			Status:      "deployed",
			Revision:    1,
		})
		stream, ok := agent.Streams.GetStream(msg.StreamID)
		if !ok {
			t.Errorf("stream %s not found", msg.StreamID)
			return
		}
		stream.DataCh <- respBody
	}()

	body, _ := json.Marshal(InternalHelmRequest{
		MsgType: protocol.MsgHelmInstall,
		Payload: protocol.HelmRequestPayload{ReleaseName: "rel", Namespace: "ns"},
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/helm/c-helm", bytes.NewReader(body))
	req.Header.Set(InternalSourceHeader, InternalSourceValue)
	req.Header.Set(InternalPSKHeader, "psk")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	helmHandlerRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got protocol.HelmResultPayload
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Success || got.Status != "deployed" {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestInternalHelmHandler_TimeoutReturns504(t *testing.T) {
	hub := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: "c-slow",
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
		cancel:    func() {},
	}
	hub.agents.Set("c-slow", agent)
	h := NewInternalHelmHandler(hub, "psk", slog.Default())

	body, _ := json.Marshal(InternalHelmRequest{
		MsgType: protocol.MsgHelmStatus,
		Payload: protocol.HelmRequestPayload{ReleaseName: "rel", Namespace: "ns"},
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/helm/c-slow", bytes.NewReader(body))
	req.Header.Set(InternalSourceHeader, InternalSourceValue)
	req.Header.Set(InternalPSKHeader, "psk")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	helmHandlerRouter(h).ServeHTTP(w, req)
	// No agent reply within 50ms → 504 Gateway Timeout (matches
	// InternalK8sHandler's behaviour).
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", w.Code, w.Body.String())
	}
}
