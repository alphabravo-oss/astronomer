package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// captureProxyPayload drives one request through `router` — the REAL chain,
// middleware and all — and returns the K8sRequestPayload the server put on the
// tunnel for it. The request context is cancelled as soon as the payload
// arrives so the handler unwinds instead of waiting out its 30s response
// timeout.
func captureProxyPayload(t *testing.T, router http.Handler, agentCh <-chan *protocol.Message, req *http.Request) protocol.K8sRequestPayload {
	t.Helper()
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
	}()

	var msg *protocol.Message
	select {
	case msg = <-agentCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no message reached the agent; the request never got past the middleware chain")
	}
	cancel()
	<-done

	var payload protocol.K8sRequestPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("decode k8s request payload: %v", err)
	}
	return payload
}

// TestK8sProxyPayloadIdentityIgnoresSpoofedIdentityHeaders is THE security test
// for the caller-identity plumbing.
//
// A client sends Impersonate-User, Impersonate-Group, X-Remote-User and
// X-Remote-Group alongside a legitimate session, claiming to be
// system:admin / system:masters. It is driven through NewRouter — the real
// auth + scope + RBAC + audit chain — because a test that called
// buildK8sRequestPayload directly would prove nothing about the wire.
//
// Two assertions, both required:
//   - the spoofed headers do not survive into payload.Headers (pkg/proxyhdr's
//     allowlist, invariant §7.1), and
//   - the typed identity is the SESSION's subject, not the header's claim
//     (invariant §7.3). Identity is a typed field precisely so a header can
//     never reach it.
//
// PRE-FIX BEHAVIOUR: there was no typed identity at all — the payload carried
// no caller attribution whatsoever, so the second assertion could not even be
// expressed. The header-stripping half held (and still holds).
func TestK8sProxyPayloadIdentityIgnoresSpoofedIdentityHeaders(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("caller-identity-test-secret", 60)
	userID := uuid.New()
	clusterID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	hub := tunnel.NewHub(slog.Default())
	hub.RegisterAgentForTest(clusterID.String())
	agentCh := hub.OutboundForTest(clusterID.String())
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: clusterWideListBindings(clusterID, rbac.ResourcePods)},
		Proxy:       tunnel.NewProxyHandler(hub, slog.Default()),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/pods", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Impersonate-User", "system:admin")
	req.Header.Set("Impersonate-Group", "system:masters")
	req.Header.Set("Impersonate-Extra-Scopes", "cluster-admin")
	req.Header.Set("X-Remote-User", "system:admin")
	req.Header.Set("X-Remote-Group", "system:masters")

	payload := captureProxyPayload(t, router, agentCh, req)

	for name := range payload.Headers {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "impersonate-") || strings.HasPrefix(lower, "x-remote-") {
			t.Fatalf("spoofable identity header %q reached the agent payload", name)
		}
	}
	if got, want := payload.User, callerid.UserSubject(userID); got != want {
		t.Fatalf("payload identity = %q, want the session subject %q", got, want)
	}
	if payload.Origin != protocol.OriginUser {
		t.Fatalf("payload origin = %q, want %q", payload.Origin, protocol.OriginUser)
	}
	if strings.Contains(payload.User, "system:") {
		t.Fatalf("payload identity %q was influenced by the spoofed headers", payload.User)
	}
	if !payload.IsUser() || payload.IsMachine() {
		t.Fatalf("user request must be user-origin, got %+v", payload.CallerIdentity)
	}
}

// TestK8sProxyPayloadCarriesNoImpersonationHeaders pins the Phase 0 safety
// property from the server side: with the flag at its `off` default, the
// payload the agent receives contains no Impersonate-* header for it to
// forward. The agent-side half of this assertion (that executeUpstream sets
// none of its own) lives in internal/agent.
func TestK8sProxyPayloadCarriesNoImpersonationHeaders(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("caller-identity-test-secret", 60)
	userID := uuid.New()
	clusterID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	hub := tunnel.NewHub(slog.Default())
	hub.RegisterAgentForTest(clusterID.String())
	agentCh := hub.OutboundForTest(clusterID.String())
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: clusterWideListBindings(clusterID, rbac.ResourcePods)},
		Proxy:       tunnel.NewProxyHandler(hub, slog.Default()),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/pods", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	payload := captureProxyPayload(t, router, agentCh, req)
	for name := range payload.Headers {
		if strings.HasPrefix(strings.ToLower(name), "impersonate-") {
			t.Fatalf("flag is off, yet the payload carries %q", name)
		}
	}
	if !payload.IsUser() {
		t.Fatalf("identity should still be populated with the flag off, got %+v", payload.CallerIdentity)
	}
}

// TestArgoCDInternalProxyPayloadIsMachineOrigin covers exemption §10.1 on BOTH
// listeners: the dedicated internal ArgoCD router and the compatibility route
// on the public listener. Both must stamp an explicit machine origin.
//
// This is the exemption whose loss is silent: ArgoCD's cluster cache LISTs every
// registered resource type, so if this traffic were ever impersonated the whole
// cache sync would fail and every Application would pin at sync=Unknown.
//
// PRE-FIX BEHAVIOUR: no origin was carried at all, so "machine" was
// indistinguishable from "a user route that forgot to populate identity" —
// exactly the fail-open §7 invariant 4 forbids.
func TestArgoCDInternalProxyPayloadIsMachineOrigin(t *testing.T) {
	clusterID := uuid.New()
	proxyToken := auth.ArgoCDClusterProxyTokenPrefix + "caller-identity-test"
	tokens := &routeSecurityArgoTokenQuerier{
		tokenHash: auth.HashArgoCDClusterProxyToken(proxyToken),
		clusterID: clusterID,
	}
	path := "/api/v1/internal/argocd/clusters/" + clusterID.String() + "/k8s/api/v1/pods"

	for _, tc := range []struct {
		name  string
		build func(deps RouterDependencies) http.Handler
	}{
		{
			name:  "dedicated internal listener",
			build: func(deps RouterDependencies) http.Handler { return NewInternalArgoCDProxyRouter(deps) },
		},
		{
			name:  "compatibility route on the public listener",
			build: func(deps RouterDependencies) http.Handler { return NewRouter(&config.Config{}, deps) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := tunnel.NewHub(slog.Default())
			hub.RegisterAgentForTest(clusterID.String())
			agentCh := hub.OutboundForTest(clusterID.String())
			router := tc.build(RouterDependencies{
				Proxy:             tunnel.NewProxyHandler(hub, slog.Default()),
				ArgoCDProxyTokens: tokens,
			})
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+proxyToken)

			payload := captureProxyPayload(t, router, agentCh, req)
			if !payload.IsMachine() {
				t.Fatalf("ArgoCD proxy payload must be machine-origin, got %+v", payload.CallerIdentity)
			}
			if payload.IsUser() {
				t.Fatalf("ArgoCD proxy payload must not be user-origin, got %+v", payload.CallerIdentity)
			}
			if got, want := payload.User, callerid.MachineSubject(callerid.SourceArgoCDProxy); got != want {
				t.Fatalf("machine subject = %q, want %q", got, want)
			}
		})
	}
}
