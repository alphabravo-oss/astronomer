package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// stubBindings returns a fixed set of bindings for any user id. The whole point
// of §DataProxy is that the proxy re-checks the CALLER's own bindings, so the
// test drives the bindings the user actually holds and asserts the proxy never
// exceeds them.
type stubBindings struct {
	bindings []rbac.RoleBinding
	err      error
}

func (s stubBindings) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return s.bindings, s.err
}

// monitoringReaderOn builds a cluster-scoped monitoring:read binding for a
// single cluster — exactly what a tenant user with read access to ONE cluster
// would hold.
func monitoringReaderOn(clusterID uuid.UUID) []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		Scope:     "cluster",
		RoleRules: []rbac.Rule{{Resource: "monitoring", Verbs: []string{"read"}}},
	}}
}

// proxyHandler wires an ExtensionHandler with a seeded tier-1 extension, the
// given bindings, and a recording upstream so the test can assert whether the
// upstream was ever reached (it must NOT be on a denied call).
func proxyHandler(t *testing.T, bindings []rbac.RoleBinding) (*ExtensionHandler, *bool) {
	t.Helper()
	q := newFakeExtensionQuerier()
	q.seedExtension(t, tier1Manifest(), true, "compatible", false)
	h := NewExtensionHandler(q)
	h.SetRBAC(rbac.NewEngine(), stubBindings{bindings: bindings})
	upstreamHit := false
	h.SetUpstream(func(context.Context, ExtensionUpstreamRequest) (any, error) {
		upstreamHit = true
		return []any{
			map[string]any{"namespace": "team-a", "usd": 12.5, "secret": "hidden"},
		}, nil
	})
	return h, &upstreamHit
}

func proxyRequest(t *testing.T, userID uuid.UUID, dataSourceID string, body map[string]any) *http.Request {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/extensions/cost-insights/data/"+dataSourceID+"/", strings.NewReader(string(raw)))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "cost-insights")
	rctx.URLParams.Add("dataSourceId", dataSourceID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = middleware.SetAuthenticatedUserForTest(ctx, &middleware.AuthenticatedUser{ID: userID.String(), AuthMethod: "jwt"})
	return req.WithContext(ctx)
}

// TestProxyData_CannotExceedUserRBAC is the load-bearing test: a user who holds
// monitoring:read on cluster A cannot use the extension proxy to read cluster B,
// even though the extension's manifest declares the monitoring:read source. The
// manifest only narrows; the user's own RBAC is the actual grant.
func TestProxyData_CannotExceedUserRBAC(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	userID := uuid.New()

	h, upstreamHit := proxyHandler(t, monitoringReaderOn(clusterA))

	// The user asks for cluster B — which they have NO binding for.
	req := proxyRequest(t, userID, "podCost", map[string]any{
		"context":    map[string]any{"clusterId": clusterB.String()},
		"pathParams": map[string]any{"clusterId": clusterB.String()},
	})
	rr := httptest.NewRecorder()
	h.ProxyData(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a cluster the user cannot read, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "extension_rbac_denied") {
		t.Fatalf("expected extension_rbac_denied code, got %s", rr.Body.String())
	}
	if *upstreamHit {
		t.Fatalf("upstream must NOT be reached when the user's RBAC denies the call")
	}
}

// TestProxyData_AllowedWithinUserRBAC proves the happy path: the same user CAN
// read the cluster they hold a binding for, and the response is projected to the
// manifest's declared fields (the undeclared "secret" field is stripped).
func TestProxyData_AllowedWithinUserRBAC(t *testing.T) {
	clusterA := uuid.New()
	userID := uuid.New()

	h, upstreamHit := proxyHandler(t, monitoringReaderOn(clusterA))

	req := proxyRequest(t, userID, "podCost", map[string]any{
		"context":    map[string]any{"clusterId": clusterA.String()},
		"pathParams": map[string]any{"clusterId": clusterA.String()},
	})
	rr := httptest.NewRecorder()
	h.ProxyData(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 within the user's RBAC, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !*upstreamHit {
		t.Fatalf("upstream should be reached on an allowed call")
	}
	resp := decodeExtensionResp[extProxyResponse](t, rr)
	rowsWrap, _ := resp.Data.(map[string]any)
	rows, _ := rowsWrap["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 projected row, got %+v", resp.Data)
	}
	row, _ := rows[0].(map[string]any)
	if _, leaked := row["secret"]; leaked {
		t.Fatalf("undeclared field 'secret' must be projected out, got %+v", row)
	}
	if row["namespace"] != "team-a" {
		t.Fatalf("declared field 'namespace' missing, got %+v", row)
	}
}

// TestProxyData_UnknownDataSourceRejected proves the manifest allowlist: a
// dataSource id not present in the stored manifest is a 404 — an extension can
// only reach routes it shipped.
func TestProxyData_UnknownDataSourceRejected(t *testing.T) {
	userID := uuid.New()
	h, upstreamHit := proxyHandler(t, monitoringReaderOn(uuid.New()))

	req := proxyRequest(t, userID, "not-declared", map[string]any{
		"context": map[string]any{"clusterId": uuid.New().String()},
	})
	rr := httptest.NewRecorder()
	h.ProxyData(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an undeclared dataSource, got %d body=%s", rr.Code, rr.Body.String())
	}
	if *upstreamHit {
		t.Fatalf("upstream must not be reached for an unknown dataSource")
	}
}

// TestProxyData_SuperuserSatisfiesCheck proves the proxy uses the same engine
// semantics as the host middleware: a superuser binding short-circuits the
// per-call check (the proxy doesn't invent a stricter rule than the host).
func TestProxyData_SuperuserSatisfiesCheck(t *testing.T) {
	userID := uuid.New()
	h, _ := proxyHandler(t, []rbac.RoleBinding{{IsSuperuser: true}})

	req := proxyRequest(t, userID, "podCost", map[string]any{
		"context":    map[string]any{"clusterId": uuid.New().String()},
		"pathParams": map[string]any{"clusterId": uuid.New().String()},
	})
	rr := httptest.NewRecorder()
	h.ProxyData(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("superuser should be allowed, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestProxyData_DisabledExtensionRejected proves step 1: a disabled (or
// incompatible) extension is a 409 even for a user who would otherwise pass the
// RBAC check.
func TestProxyData_DisabledExtensionRejected(t *testing.T) {
	q := newFakeExtensionQuerier()
	q.seedExtension(t, tier1Manifest(), false /*disabled*/, "compatible", false)
	h := NewExtensionHandler(q)
	h.SetRBAC(rbac.NewEngine(), stubBindings{bindings: []rbac.RoleBinding{{IsSuperuser: true}}})
	h.SetUpstream(func(context.Context, ExtensionUpstreamRequest) (any, error) { return []any{}, nil })

	req := proxyRequest(t, uuid.New(), "podCost", map[string]any{
		"context": map[string]any{"clusterId": uuid.New().String()},
	})
	rr := httptest.NewRecorder()
	h.ProxyData(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a disabled extension, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestProxyData_UnauthenticatedRejected proves a request with no session and no
// ticket cannot reach the proxy (the extension has no identity of its own).
func TestProxyData_UnauthenticatedRejected(t *testing.T) {
	h, _ := proxyHandler(t, monitoringReaderOn(uuid.New()))

	raw, _ := json.Marshal(map[string]any{"context": map[string]any{"clusterId": uuid.New().String()}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/extensions/cost-insights/data/podCost/", strings.NewReader(string(raw)))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "cost-insights")
	rctx.URLParams.Add("dataSourceId", "podCost")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()
	h.ProxyData(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a session or ticket, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestProxyData_RuntimeCeilingReassertion proves step 5: even if the stored
// manifest is mutated out-of-band so a dataSource's RBAC no longer appears in
// permissions[], the proxy denies the call at runtime (defence in depth on top
// of install-time validation).
func TestProxyData_RuntimeCeilingReassertion(t *testing.T) {
	clusterA := uuid.New()
	m := tier1Manifest()
	// Strip permissions so the declared dataSource RBAC is no longer covered.
	m.Permissions = []string{"clusters:read"}
	q := newFakeExtensionQuerier()
	q.seedExtension(t, m, true, "compatible", false)
	h := NewExtensionHandler(q)
	// User genuinely holds monitoring:read — but the manifest ceiling no longer
	// declares it, so the proxy must still refuse.
	h.SetRBAC(rbac.NewEngine(), stubBindings{bindings: monitoringReaderOn(clusterA)})
	upstreamHit := false
	h.SetUpstream(func(context.Context, ExtensionUpstreamRequest) (any, error) { upstreamHit = true; return []any{}, nil })

	req := proxyRequest(t, uuid.New(), "podCost", map[string]any{
		"context":    map[string]any{"clusterId": clusterA.String()},
		"pathParams": map[string]any{"clusterId": clusterA.String()},
	})
	rr := httptest.NewRecorder()
	h.ProxyData(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when the runtime ceiling no longer covers the source, got %d body=%s", rr.Code, rr.Body.String())
	}
	if upstreamHit {
		t.Fatalf("upstream must not be reached when the runtime ceiling fails")
	}
}

// TestProxyData_NoUpstreamConfiguredFailsAfterGates proves the upstream is only
// reached AFTER the RBAC + allowlist gates pass: an allowed call with no
// upstream wired returns 502 (not 403), confirming a denied caller never gets
// that far.
func TestProxyData_NoUpstreamConfiguredFailsAfterGates(t *testing.T) {
	clusterA := uuid.New()
	q := newFakeExtensionQuerier()
	q.seedExtension(t, tier1Manifest(), true, "compatible", false)
	h := NewExtensionHandler(q)
	h.SetRBAC(rbac.NewEngine(), stubBindings{bindings: monitoringReaderOn(clusterA)})
	// No SetUpstream.

	req := proxyRequest(t, uuid.New(), "podCost", map[string]any{
		"context":    map[string]any{"clusterId": clusterA.String()},
		"pathParams": map[string]any{"clusterId": clusterA.String()},
	})
	rr := httptest.NewRecorder()
	h.ProxyData(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 (gates passed, upstream unconfigured), got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestBuildUpstreamPath_RejectsMissingPlaceholder proves the server-side URL
// build refuses an unfilled placeholder rather than emitting a literal
// "{clusterId}" upstream — no client field can redirect the upstream.
func TestBuildUpstreamPath_RejectsMissingPlaceholder(t *testing.T) {
	if _, err := buildUpstreamPath("/api/v1/clusters/{clusterId}/cost", uuid.Nil, uuid.Nil, "", nil); err == nil {
		t.Fatalf("expected error for missing clusterId placeholder")
	}
	cid := uuid.New()
	out, err := buildUpstreamPath("/api/v1/clusters/{clusterId}/cost", cid, uuid.Nil, "", nil)
	if err != nil || out != "/api/v1/clusters/"+cid.String()+"/cost" {
		t.Fatalf("unexpected fill: out=%q err=%v", out, err)
	}
}

// TestMergeDeclaredQuery_DropsUndeclaredKeys proves a widget may override only
// declared query keys and can never introduce new ones.
func TestMergeDeclaredQuery_DropsUndeclaredKeys(t *testing.T) {
	declared := map[string]string{"window": "30d"}
	got := mergeDeclaredQuery(declared, map[string]string{"window": "7d", "evil": "1"})
	if got["window"] != "7d" {
		t.Fatalf("declared key override dropped: %+v", got)
	}
	if _, ok := got["evil"]; ok {
		t.Fatalf("undeclared key 'evil' must be dropped: %+v", got)
	}
}
