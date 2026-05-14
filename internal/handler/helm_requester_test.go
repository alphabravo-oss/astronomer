package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// When PSK is empty, the requester must not invoke forwardToOwner.
// Falling through to SendHelmRequest with no local agent surfaces
// "cluster agent not connected", which is the pre-fallback behaviour.
func TestTunnelHelmRequester_NoPSK_FallsThrough(t *testing.T) {
	hub := tunnel.NewHub(nil)
	r := NewTunnelHelmRequester(hub)
	// no SetInternalPSK call — psk stays empty.

	_, err := r.Do(context.Background(), "c1", protocol.MsgHelmInstall, protocol.HelmRequestPayload{
		ReleaseName: "rel",
		Namespace:   "ns",
	})
	if err == nil || !strings.Contains(err.Error(), "cluster agent not connected") {
		t.Fatalf("expected fall-through 'cluster agent not connected', got %v", err)
	}
}

// When the locator is nil (single-replica deployments) the fallback also
// has to fall through, regardless of PSK.
func TestTunnelHelmRequester_NoLocator_FallsThrough(t *testing.T) {
	hub := tunnel.NewHub(nil)
	r := NewTunnelHelmRequester(hub)
	r.SetInternalPSK("psk")

	_, err := r.Do(context.Background(), "c1", protocol.MsgHelmStatus, protocol.HelmRequestPayload{
		ReleaseName: "rel", Namespace: "ns",
	})
	if err == nil || !strings.Contains(err.Error(), "cluster agent not connected") {
		t.Fatalf("expected fall-through error, got %v", err)
	}
}

// When the locator entry points back at our own address (a stale
// self-pointer), the requester must NOT forward; it falls through to the
// local hub which 503s as "not connected".
//
// We don't have a fake redis to drive Locator.Lookup here, so this test
// is covered indirectly via TestTunnelHelmRequester_NoLocator_FallsThrough
// + the locator's own unit tests.

// End-to-end forward: spin up an httptest server that pretends to be the
// sibling pod, install a stub locator that points the requester at it,
// and verify the helm payload round-trips through HTTP.
func TestTunnelHelmRequester_ForwardsToSibling(t *testing.T) {
	// Sibling pod stand-in: returns a canned HelmResultPayload.
	var seen tunnel.InternalHelmRequest
	sibling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(tunnel.InternalPSKHeader) != "psk" {
			http.Error(w, "bad psk", http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &seen)
		reply := protocol.HelmResultPayload{
			Success:     true,
			ReleaseName: seen.Payload.ReleaseName,
			Namespace:   seen.Payload.Namespace,
			Status:      "deployed",
			Revision:    7,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer sibling.Close()
	addr := strings.TrimPrefix(sibling.URL, "http://")

	hub := tunnel.NewHub(nil)
	// Stub locator: we are "self:8000" and cluster c1 lives on addr.
	loc := tunnel.NewFakeLocatorForTest("self:8000", map[string]string{"c1": addr})
	hub.SetLocator(loc)

	r := NewTunnelHelmRequester(hub)
	r.SetInternalPSK("psk")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := r.Do(ctx, "c1", protocol.MsgHelmUpgrade, protocol.HelmRequestPayload{
		ReleaseName: "rel",
		Namespace:   "ns",
		ChartName:   "kube-prom-stack",
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if resp == nil || resp.Revision != 7 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if seen.MsgType != protocol.MsgHelmUpgrade {
		t.Errorf("sibling saw msg_type %q, want HELM_UPGRADE", seen.MsgType)
	}
	if seen.Payload.ChartName != "kube-prom-stack" {
		t.Errorf("sibling saw chart %q, want kube-prom-stack", seen.Payload.ChartName)
	}
}

// When the sibling reports a non-2xx (e.g. it lost the WS in flight),
// the requester surfaces a wrapped error.
func TestTunnelHelmRequester_ForwardSibling5xx(t *testing.T) {
	sibling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Cluster agent not connected"}`, http.StatusServiceUnavailable)
	}))
	defer sibling.Close()
	addr := strings.TrimPrefix(sibling.URL, "http://")

	hub := tunnel.NewHub(nil)
	loc := tunnel.NewFakeLocatorForTest("self:8000", map[string]string{"c1": addr})
	hub.SetLocator(loc)
	r := NewTunnelHelmRequester(hub)
	r.SetInternalPSK("psk")

	_, err := r.Do(context.Background(), "c1", protocol.MsgHelmInstall, protocol.HelmRequestPayload{})
	if err == nil {
		t.Fatal("expected error from sibling 503")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected wrapped 503 in error, got %v", err)
	}
}

// When the agent reports Success=false the requester surfaces the embedded
// error string (mirroring SendHelmRequest's local-hub behaviour).
func TestTunnelHelmRequester_AgentReportsFailure(t *testing.T) {
	sibling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := protocol.HelmResultPayload{
			Success: false,
			Error:   "chart not found",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer sibling.Close()
	addr := strings.TrimPrefix(sibling.URL, "http://")

	hub := tunnel.NewHub(nil)
	loc := tunnel.NewFakeLocatorForTest("self:8000", map[string]string{"c1": addr})
	hub.SetLocator(loc)
	r := NewTunnelHelmRequester(hub)
	r.SetInternalPSK("psk")

	resp, err := r.Do(context.Background(), "c1", protocol.MsgHelmInstall, protocol.HelmRequestPayload{})
	if err == nil || !strings.Contains(err.Error(), "chart not found") {
		t.Fatalf("expected helm failure surfaced, got err=%v resp=%+v", err, resp)
	}
}

// When the locator's address matches our own (self-pointer), the forward
// is skipped and we fall through to the local-hub path.
func TestTunnelHelmRequester_SelfPointer_FallsThrough(t *testing.T) {
	hub := tunnel.NewHub(nil)
	// Locator's own address matches the entry — i.e. we look ourselves up.
	loc := tunnel.NewFakeLocatorForTest("self:8000", map[string]string{"c1": "self:8000"})
	hub.SetLocator(loc)
	r := NewTunnelHelmRequester(hub)
	r.SetInternalPSK("psk")

	_, err := r.Do(context.Background(), "c1", protocol.MsgHelmInstall, protocol.HelmRequestPayload{})
	if err == nil || !strings.Contains(err.Error(), "cluster agent not connected") {
		t.Fatalf("expected local-hub fall-through, got %v", err)
	}
}
