package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// TestScopeChokepointRejectsReadTokenOnUnguardedMutation pins the
// default-deny scope backstop wired on the authenticated subrouter
// (routes.go). POST /clusters/{id}/generate-kubeconfig/ is guarded only
// by an RBAC *read* permission and carries NO per-route write-scope
// middleware, so before the chokepoint a read-scoped API token whose
// owner held cluster-read RBAC could drive this mutation. The
// group-level RequireWriteScopeForMutations("") backstop must now reject
// that read-only token (403 scope_denied) before the handler runs, while
// a GET on the same subtree still passes for the read token.
func TestScopeChokepointRejectsReadTokenOnUnguardedMutation(t *testing.T) {
	userID := uuid.New()
	clusterID := uuid.New().String()
	// Owner holds full cluster read access — RBAC would let this through;
	// only the scope backstop stops it.
	readRBAC := routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead)

	newRouter := func(rawToken string, scopes json.RawMessage) http.Handler {
		return NewRouter(&config.Config{}, RouterDependencies{
			JWT:         auth.NewJWTManager("scope-chokepoint-test-secret", 60),
			AuthQueries: routeSecurityAPITokenQuerier(rawToken, userID, scopes),
			RBACEngine:  rbac.NewEngine(),
			RBACQueries: routeSecurityRBACQuerier{bindings: readRBAC},
			Clusters:    handler.NewClusterHandler(routeSecurityClusterQuerier{}),
		})
	}

	mutatePath := "/api/v1/clusters/" + clusterID + "/generate-kubeconfig/"

	// Read-scoped token => mutating POST is denied by the chokepoint.
	readRouter := newRouter("astro_chokepoint_read", json.RawMessage(`["read"]`))
	rec := doRequest(readRouter, http.MethodPost, mutatePath, "astro_chokepoint_read", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-scoped token on unguarded mutation: status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "scope_denied") {
		t.Fatalf("read-scoped token body = %s, want scope_denied", rec.Body.String())
	}

	// Same read token on a GET (read route) is NOT blocked by the
	// chokepoint — reads are the allowlist.
	getRec := doRequest(readRouter, http.MethodGet, "/api/v1/clusters/"+clusterID+"/", "astro_chokepoint_read", "")
	if getRec.Code == http.StatusForbidden && strings.Contains(getRec.Body.String(), "scope_denied") {
		t.Fatalf("read-scoped token GET must not be scope_denied; body=%s", getRec.Body.String())
	}
}
