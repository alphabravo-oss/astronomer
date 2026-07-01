package server

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// fakeNativeRuleQuerier returns a fixed rule set regardless of user, so the
// authorizer's DB path + row→NativeRule conversion + cache are exercised.
type fakeNativeRuleQuerier struct{ rows []sqlc.NativeRbacRule }

func (f fakeNativeRuleQuerier) ListNativeRBACRulesByUser(context.Context, uuid.UUID) ([]sqlc.NativeRbacRule, error) {
	return f.rows, nil
}

func nativeRuleRow(group, resource string, verbs []string) sqlc.NativeRbacRule {
	return sqlc.NativeRbacRule{ID: uuid.New(), UserID: uuid.New(), ApiGroup: group, Resource: resource, Verbs: verbs}
}

// newProxyRouterWithNative mirrors newProxyPermissionRouter but injects a
// native authorizer and grants NO coarse bindings, so every request is a
// coarse deny — isolating the native additive-allow behavior.
func newProxyRouterWithNative(t *testing.T, rows []sqlc.NativeRbacRule) (http.Handler, string) {
	t.Helper()
	jwtMgr := auth.NewJWTManager("native-rbac-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: nil}, // coarse denies all
		NativeAuthz: newNativeRBACAuthorizer(fakeNativeRuleQuerier{rows: rows}),
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})
	return router, token
}

// Allowed-through means the proxy reached the tunnel (not connected → 503);
// 403 means the authz gate denied. We assert on that distinction.
const proxyAllowedThrough = http.StatusServiceUnavailable

func TestNativeRBAC_AdditiveAllowOnCoarseDeny(t *testing.T) {
	cluster := uuid.New().String()
	certPath := "/api/v1/clusters/" + cluster + "/k8s/apis/cert-manager.io/v1/namespaces/team-a/certificates"

	// With a native rule granting cert read, a coarse-denied GET passes.
	withRule, tok := newProxyRouterWithNative(t, []sqlc.NativeRbacRule{
		nativeRuleRow("cert-manager.io", "certificates", []string{"read", "list"}),
	})
	if rec := proxyRequest(withRule, http.MethodGet, certPath+"/my-cert", tok); rec.Code != proxyAllowedThrough {
		t.Fatalf("native cert read should pass authz (expect %d proxy-through), got %d", proxyAllowedThrough, rec.Code)
	}

	// A DIFFERENT CRD in the same group is NOT covered by the rule → 403.
	issuerPath := "/api/v1/clusters/" + cluster + "/k8s/apis/cert-manager.io/v1/namespaces/team-a/issuers"
	if rec := proxyRequest(withRule, http.MethodGet, issuerPath, tok); rec.Code != http.StatusForbidden {
		t.Fatalf("issuers not granted → expect 403, got %d", rec.Code)
	}

	// A write verb is not in the rule → 403.
	if rec := proxyRequest(withRule, http.MethodDelete, certPath+"/my-cert", tok); rec.Code != http.StatusForbidden {
		t.Fatalf("cert delete not granted → expect 403, got %d", rec.Code)
	}

	// With NO native rules, the same read is a plain coarse deny → 403.
	noRule, tok2 := newProxyRouterWithNative(t, nil)
	if rec := proxyRequest(noRule, http.MethodGet, certPath+"/my-cert", tok2); rec.Code != http.StatusForbidden {
		t.Fatalf("no native rule → expect 403, got %d", rec.Code)
	}
}

func TestNativeRBAC_NeverGrantsEscalationOrExec(t *testing.T) {
	cluster := uuid.New().String()

	// Even with a stored rule on an escalation group, the request is denied.
	escRouter, tok := newProxyRouterWithNative(t, []sqlc.NativeRbacRule{
		nativeRuleRow("rbac.authorization.k8s.io", "clusterroles", []string{"*"}),
	})
	escPath := "/api/v1/clusters/" + cluster + "/k8s/apis/rbac.authorization.k8s.io/v1/clusterroles"
	if rec := proxyRequest(escRouter, http.MethodGet, escPath, tok); rec.Code != http.StatusForbidden {
		t.Fatalf("native rule must NEVER grant escalation groups → expect 403, got %d", rec.Code)
	}

	// A stored exec grant on pods must not open a shell via the proxy.
	execRouter, tok2 := newProxyRouterWithNative(t, []sqlc.NativeRbacRule{
		nativeRuleRow("", "pods", []string{"exec", "*"}),
	})
	execPath := "/api/v1/clusters/" + cluster + "/k8s/api/v1/namespaces/team-a/pods/p/exec"
	if rec := proxyRequest(execRouter, http.MethodGet, execPath, tok2); rec.Code != http.StatusForbidden {
		t.Fatalf("native rule must NEVER grant pod exec → expect 403, got %d", rec.Code)
	}
}
