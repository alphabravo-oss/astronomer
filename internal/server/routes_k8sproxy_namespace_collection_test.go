package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

func TestK8sProxyExplicitNamespaceCollectionAuthorization(t *testing.T) {
	jwt := auth.MustNewJWTManager("namespace-collection-test-secret", 60)
	clusterID := uuid.New()
	token := nsRBACProxyToken(t, jwt, uuid.New())
	bindings := append(namespaceScopedListBindings(clusterID, "a", rbac.ResourceCustomResources), namespaceScopedListBindings(clusterID, "b", rbac.ResourceCustomResources)...)
	router := NewRouter(&config.Config{}, RouterDependencies{CoreAuth: CoreAuthDependencies{JWT: jwt, RBACEngine: rbac.NewEngine(), RBACQueries: routeSecurityRBACQuerier{bindings: bindings}}, StreamingInternal: StreamingInternalDependencies{Proxy: tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), nil)}})
	base := "/api/v1/clusters/" + clusterID.String() + "/k8s/apis/example.com/v1/widgets"
	for _, tt := range []struct {
		name, path string
		want       int
	}{
		{"explicit subset does not require cluster-wide grant", base + "?astronomerNamespace=b&astronomerNamespace=a", 503},
		{"unselected cluster-wide list stays forbidden", base, 403},
		{"unauthorized selected namespace denied", base + "?astronomerNamespace=a&astronomerNamespace=other", 403},
		{"forged continuation cannot authorize namespace", base + "?astronomerNamespace=other&continue=forged", 403},
		{"empty does not become all", base + "?astronomerNamespace=", 400},
		{"object usage rejected", base + "/name?astronomerNamespace=a", 400},
		{"watch usage rejected", base + "?astronomerNamespace=a&watch=true", 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("status%d want%d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

type changingNamespaceBindings struct {
	current []rbac.RoleBinding
}

func (q *changingNamespaceBindings) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return q.current, nil
}

func TestK8sProxyNamespaceContinuationRechecksBindings(t *testing.T) {
	jwt := auth.MustNewJWTManager("namespace-revocation-test-secret", 60)
	cluster := uuid.New()
	token := nsRBACProxyToken(t, jwt, uuid.New())
	queries := &changingNamespaceBindings{current: append(namespaceScopedListBindings(cluster, "a", rbac.ResourceCustomResources), namespaceScopedListBindings(cluster, "b", rbac.ResourceCustomResources)...)}
	router := NewRouter(&config.Config{}, RouterDependencies{CoreAuth: CoreAuthDependencies{JWT: jwt, RBACEngine: rbac.NewEngine(), RBACQueries: queries}, StreamingInternal: StreamingInternalDependencies{Proxy: tunnel.NewProxyHandler(tunnel.NewHub(nil), nil)}})
	base := "/api/v1/clusters/" + cluster.String() + "/k8s/apis/example.com/v1/widgets?astronomerNamespace=a&astronomerNamespace=b"
	request := func(path string) int {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	if got := request(base); got != 503 {
		t.Fatalf("initial bindings should admit, status%d", got)
	}
	queries.current = namespaceScopedListBindings(cluster, "a", rbac.ResourceCustomResources)
	// Authorization must deny BEFORE cursor processing or contacting an agent.
	if got := request(base + "&continue=previous-page"); got != 403 {
		t.Fatalf("revoked namespace continuation status%d", got)
	}
}
