package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestTunnelK8sRequesterCapabilityAdmissionDoesNotTripCircuit(t *testing.T) {
	hub := tunnel.NewHub(slog.Default())
	hub.RegisterAgentForTest("read-only", "watch")
	requester := NewTunnelK8sRequesterWithBreaker(hub, 2, time.Minute)

	supported, err := requester.SupportsCapability(t.Context(), "read-only", protocol.AgentCapabilityMutate)
	if err != nil {
		t.Fatalf("SupportsCapability: %v", err)
	}
	if supported {
		t.Fatal("read-only agent unexpectedly supports mutation")
	}

	for i := 0; i < 5; i++ {
		_, err := requester.Do(t.Context(), "read-only", http.MethodPatch, "/api/v1/namespaces/default/configmaps/example", []byte(`{}`), nil)
		if !errors.Is(err, tunnel.ErrAgentCapabilityUnsupported) {
			t.Fatalf("attempt %d error = %v, want capability sentinel", i+1, err)
		}
	}
	if got := requester.breaker.state("read-only"); got != breakerClosed {
		t.Fatalf("breaker state = %s, want closed", got)
	}
}

func TestTunnelK8sRequesterCapabilityAdmissionAcrossServerReplicas(t *testing.T) {
	ownerHub := tunnel.NewHub(slog.Default())
	ownerHub.RegisterAgentForTest("remote", "watch")
	internal := tunnel.NewInternalK8sHandler(ownerHub, tunnel.InternalRequestKeyring{Current: "test-psk"}, slog.Default())
	router := chi.NewRouter()
	router.Get("/internal/tunnel/k8s/{cluster_id}/capabilities/{capability}", internal.HandleCapability)
	sibling := httptest.NewServer(router)
	t.Cleanup(sibling.Close)

	originHub := tunnel.NewHub(slog.Default())
	originHub.SetLocator(tunnel.NewFakeLocatorForTest("self:8000", map[string]string{
		"remote": strings.TrimPrefix(sibling.URL, "http://"),
	}))
	requester := NewTunnelK8sRequester(originHub)
	requester.SetInternalPSK("test-psk")

	for capability, want := range map[string]bool{"watch": true, protocol.AgentCapabilityMutate: false} {
		got, err := requester.SupportsCapability(t.Context(), "remote", capability)
		if err != nil {
			t.Fatalf("SupportsCapability(%q): %v", capability, err)
		}
		if got != want {
			t.Fatalf("SupportsCapability(%q) = %t, want %t", capability, got, want)
		}
	}
}
