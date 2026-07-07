package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// fixedLocalCluster returns a resolver that always yields id — the anchor scope
// the ArgoCD proxy authz check evaluates against.
func fixedLocalCluster(id uuid.UUID) LocalClusterResolver {
	return func(_ context.Context) (uuid.UUID, error) { return id, nil }
}

// serveArgoCDAuthz runs the ArgoCDAuthz middleware for the given request and
// reports whether the wrapped handler (the proxy that injects the upstream
// admin cookie) was reached, plus the recorded response.
func serveArgoCDAuthz(t *testing.T, engine *rbac.Engine, q RBACQuerier, resolver LocalClusterResolver, r *http.Request) (bool, *httptest.ResponseRecorder) {
	t.Helper()
	nextCalled := false
	mw := ArgoCDAuthz(engine, q, resolver)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	r = r.WithContext(SetAuthenticatedUserForTest(r.Context(), &AuthenticatedUser{ID: uuid.NewString()}))
	h.ServeHTTP(rec, r)
	return nextCalled, rec
}

// TestArgoCDAuthz_ViewerNoGrants_Denied covers scenario (a): a viewer with no
// grants gets 403 on ArgoCD reads and the proxy (which injects the argocd.token
// admin cookie) is never reached.
func TestArgoCDAuthz_ViewerNoGrants_Denied(t *testing.T) {
	engine := rbac.NewEngine()
	local := uuid.New()
	q := &mockRBACQuerier{bindings: nil} // no grants at all

	for _, path := range []string{"/argocd/", "/argocd/api/v1/applications"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		nextCalled, rec := serveArgoCDAuthz(t, engine, q, fixedLocalCluster(local), req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", path, rec.Code)
		}
		if nextCalled {
			t.Errorf("%s: proxy handler was reached — admin cookie would be injected for an unauthorized viewer", path)
		}
	}
}

// TestArgoCDAuthz_GrantedUser_Proxied covers scenario (b): a user holding a
// read grant is proxied through on a GET.
func TestArgoCDAuthz_GrantedUser_Proxied(t *testing.T) {
	engine := rbac.NewEngine()
	local := uuid.New()
	q := &mockRBACQuerier{bindings: readOnlyBindings()} // global read+list

	req := httptest.NewRequest(http.MethodGet, "/argocd/api/v1/applications", nil)
	nextCalled, rec := serveArgoCDAuthz(t, engine, q, fixedLocalCluster(local), req)
	if !nextCalled {
		t.Fatal("granted user should be proxied through to ArgoCD")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestArgoCDAuthz_ReadOnlyMutation_Denied covers scenario (c): a read-only user
// issuing a mutating request (POST, or a GET to a sync action path) is 403'd.
func TestArgoCDAuthz_ReadOnlyMutation_Denied(t *testing.T) {
	engine := rbac.NewEngine()
	local := uuid.New()
	q := &mockRBACQuerier{bindings: readOnlyBindings()} // read+list, no update

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/argocd/api/v1/applications"},
		{http.MethodDelete, "/argocd/api/v1/applications/foo"},
		{http.MethodGet, "/argocd/api/v1/applications/foo/sync"}, // action path as GET
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		nextCalled, rec := serveArgoCDAuthz(t, engine, q, fixedLocalCluster(local), req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", tc.method, tc.path, rec.Code)
		}
		if nextCalled {
			t.Errorf("%s %s: mutation reached the proxy for a read-only user", tc.method, tc.path)
		}
	}
}

// TestArgoCDAuthz_ClusterUpdateUser_MutationAllowed confirms a user with
// clusters:update on the local cluster can drive ArgoCD mutations.
func TestArgoCDAuthz_ClusterUpdateUser_MutationAllowed(t *testing.T) {
	engine := rbac.NewEngine()
	local := uuid.New()
	q := &mockRBACQuerier{bindings: clusterBindings(local.String())} // wildcard on the cluster

	req := httptest.NewRequest(http.MethodPost, "/argocd/api/v1/applications/foo/sync", nil)
	nextCalled, rec := serveArgoCDAuthz(t, engine, q, fixedLocalCluster(local), req)
	if !nextCalled || rec.Code != http.StatusOK {
		t.Fatalf("cluster-update user should be allowed to sync: nextCalled=%v code=%d", nextCalled, rec.Code)
	}
}

// TestArgoCDAuthz_FailClosed_NilEngine verifies a misconfiguration (nil RBAC
// engine/querier) denies rather than silently opening the admin proxy.
func TestArgoCDAuthz_FailClosed_NilEngine(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/argocd/", nil)
	nextCalled, rec := serveArgoCDAuthz(t, nil, nil, nil, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 on nil engine/querier", rec.Code)
	}
	if nextCalled {
		t.Error("nil engine/querier must fail closed, not pass through to the admin proxy")
	}
}

// TestArgoCDAuthz_HTMLNav_GetsPermissionPage verifies the HTML-vs-JSON split:
// a browser navigation denial returns an HTML permission page, an XHR returns
// JSON.
func TestArgoCDAuthz_HTMLNav_GetsPermissionPage(t *testing.T) {
	engine := rbac.NewEngine()
	q := &mockRBACQuerier{bindings: nil}

	navReq := httptest.NewRequest(http.MethodGet, "/argocd/applications", nil)
	navReq.Header.Set("Accept", "text/html")
	_, navRec := serveArgoCDAuthz(t, engine, q, fixedLocalCluster(uuid.New()), navReq)
	if ct := navRec.Header().Get("Content-Type"); ct == "" || ct[:9] != "text/html" {
		t.Errorf("browser nav denial Content-Type = %q, want text/html", ct)
	}

	xhrReq := httptest.NewRequest(http.MethodGet, "/argocd/api/v1/applications", nil)
	xhrReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	_, xhrRec := serveArgoCDAuthz(t, engine, q, fixedLocalCluster(uuid.New()), xhrReq)
	if ct := xhrRec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("XHR denial Content-Type = %q, want application/json", ct)
	}
}
