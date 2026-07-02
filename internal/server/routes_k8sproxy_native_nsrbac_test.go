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

// fakeNativeNSAuthorizer implements both nativeAuthorizer (Allow) and the
// optional nativeNamespaceLister capability. Allow always denies (mirroring a
// cluster-wide LIST, namespace=="", which no single-namespace native rule
// matches); AuthorizedNamespaces returns the canned list-filter allow-set.
type fakeNativeNSAuthorizer struct {
	all   bool
	names map[string]struct{}
}

func (f fakeNativeNSAuthorizer) Allow(_ context.Context, _, _, _, _, _, _ string) bool {
	return false
}

func (f fakeNativeNSAuthorizer) AuthorizedNamespaces(_ context.Context, _, _, _, _, _ string) (bool, map[string]struct{}) {
	return f.all, f.names
}

// TestK8sProxyNativeNamespaceListFold covers the fix: a user whose ONLY grant
// for a CRD is a native namespaced rule must get a filtered cluster-wide LIST
// (admitted -> 503 with no agent) instead of a hard 403. The user carries no
// coarse bindings, so engine.AuthorizedNamespaces contributes nothing; only the
// folded native namespaces admit the request.
func TestK8sProxyNativeNamespaceListFold(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	// Custom resource list path -> api_group=example.com, resource=widgets,
	// namespace="" (cluster-wide LIST), verb=list.
	path := "/api/v1/clusters/" + clusterID.String() + "/k8s/apis/example.com/v1/widgets"

	const (
		admitted = http.StatusServiceUnavailable
		denied   = http.StatusForbidden
	)

	tests := []struct {
		name   string
		flagOn bool
		native nativeAuthorizer
		want   int
	}{
		{
			name:   "native per-namespace grant folds into list allow-set (flag on)",
			flagOn: true,
			native: fakeNativeNSAuthorizer{names: map[string]struct{}{"team-a": {}}},
			want:   admitted,
		},
		{
			name:   "native cluster-wide (all) grant admits unfiltered (flag on)",
			flagOn: true,
			native: fakeNativeNSAuthorizer{all: true},
			want:   admitted,
		},
		{
			name:   "no native namespaces -> still denied (flag on)",
			flagOn: true,
			native: fakeNativeNSAuthorizer{names: map[string]struct{}{}},
			want:   denied,
		},
		{
			name:   "native grant ignored when flag off",
			flagOn: false,
			native: fakeNativeNSAuthorizer{names: map[string]struct{}{"team-a": {}}},
			want:   denied,
		},
		{
			name:   "nil native authorizer -> denied (flag on)",
			flagOn: true,
			native: nil,
			want:   denied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := RouterDependencies{
				JWT:                 jwtMgr,
				RBACEngine:          rbac.NewEngine(),
				RBACQueries:         routeSecurityRBACQuerier{bindings: nil},
				Proxy:               tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
				NamespaceScopedRBAC: tt.flagOn,
			}
			if tt.native != nil {
				deps.NativeAuthz = tt.native
			}
			router := NewRouter(&config.Config{}, deps)
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
