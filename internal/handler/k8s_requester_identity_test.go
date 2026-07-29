package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestForwardToOwnerPreservesCallerIdentity is the HA-hop regression.
//
// The requester runs on a replica that does NOT own the cluster's WS, so Do
// falls through to forwardToOwner, which REBUILDS the K8sRequestPayload from
// scratch and POSTs it to the owning sibling. §10.3 is explicit that this hop is
// TRANSPORT, not origin: the user-originated request is still user-originated
// when it lands on the owner.
//
// PRE-FIX BEHAVIOUR: forwardToOwner constructed
// `protocol.K8sRequestPayload{Method, Path, Headers}` with no identity at all.
// Once identity existed on the direct path, every request that crossed a pod
// boundary would have silently arrived unattributed — i.e. on a two-replica
// install roughly half the traffic, intermittently, which is the worst possible
// shape for a bug in an attribution feature.
func TestForwardToOwnerPreservesCallerIdentity(t *testing.T) {
	userID := uuid.New()

	var received protocol.K8sRequestPayload
	sibling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("sibling: decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.K8sResponsePayload{StatusCode: 200})
	}))
	defer sibling.Close()

	hub := tunnel.NewHub(slog.Default())
	// The locator points at the sibling and reports a DIFFERENT self address, so
	// the requester treats this pod as a non-owner and forwards.
	hub.SetLocator(tunnel.NewFakeLocatorForTest("self:9000", map[string]string{
		"cluster-1": strings.TrimPrefix(sibling.URL, "http://"),
	}))
	r := NewTunnelK8sRequester(hub)
	r.SetInternalPSK("test-psk")

	ctx := middleware.SetAuthenticatedUserForTest(t.Context(), &middleware.AuthenticatedUser{ID: userID.String()})
	if _, err := r.Do(ctx, "cluster-1", http.MethodGet, "/api/v1/pods", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if got, want := received.User, callerid.UserSubject(userID); got != want {
		t.Fatalf("identity across the HA hop = %q, want %q", got, want)
	}
	if !received.IsUser() {
		t.Fatalf("origin across the HA hop = %q, want user", received.Origin)
	}
}

// TestDoStampsMachineOriginForMarkedContexts covers the server-internal side of
// §8 item 3: a background caller that positively marks itself (here the §10.5
// agent-lifecycle probes) is stamped machine, and — critically — a background
// caller that marks nothing is stamped NEITHER.
func TestDoStampsMachineOriginForMarkedContexts(t *testing.T) {
	var received protocol.K8sRequestPayload
	sibling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		_ = json.NewEncoder(w).Encode(protocol.K8sResponsePayload{StatusCode: 200})
	}))
	defer sibling.Close()

	hub := tunnel.NewHub(slog.Default())
	hub.SetLocator(tunnel.NewFakeLocatorForTest("self:9000", map[string]string{
		"cluster-1": strings.TrimPrefix(sibling.URL, "http://"),
	}))
	r := NewTunnelK8sRequester(hub)
	r.SetInternalPSK("test-psk")

	if _, err := r.Do(callerid.WithMachine(t.Context(), callerid.SourceAgentLifecycle), "cluster-1", http.MethodGet, "/api/v1/pods", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !received.IsMachine() {
		t.Fatalf("marked context must be machine-origin, got %+v", received.CallerIdentity)
	}
	if got, want := received.User, callerid.MachineSubject(callerid.SourceAgentLifecycle); got != want {
		t.Fatalf("machine subject = %q, want %q", got, want)
	}

	received = protocol.K8sRequestPayload{}
	if _, err := r.Do(t.Context(), "cluster-1", http.MethodGet, "/api/v1/pods", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if received.IsMachine() || received.IsUser() || received.Attributed() {
		t.Fatalf("an unmarked background call must be unattributed, got %+v", received.CallerIdentity)
	}
}

// TestCallerScopeImpersonationSubjectUsesTheSingleMinter pins design item 4:
// there is ONE spelling of the canonical subject. Previously CallerScope built
// "astronomer:user:"+uuid itself inside ImpersonationHeaders (dead code
// referenced only by its own test).
func TestCallerScopeImpersonationSubjectUsesTheSingleMinter(t *testing.T) {
	caller := uuid.New()
	scope := CallerScope{Caller: caller}
	if got, want := scope.ImpersonationSubject(), callerid.UserSubject(caller); got != want {
		t.Fatalf("subject = %q, want %q", got, want)
	}
	if (CallerScope{Caller: caller, Superuser: true}).ImpersonationSubject() != "" {
		t.Fatal("superuser scopes are never impersonated (§7 invariant 5)")
	}
	if (CallerScope{}).ImpersonationSubject() != "" {
		t.Fatal("an empty caller has no subject")
	}
}
