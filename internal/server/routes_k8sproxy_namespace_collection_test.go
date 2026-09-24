package server

import (
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/google/uuid"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
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
