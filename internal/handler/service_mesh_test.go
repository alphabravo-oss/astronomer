// Handler unit tests for the per-cluster service-mesh tile (migration 071).
//
// The tests stand up an in-memory fakeServiceMeshQuerier so the handler
// can be exercised without a Postgres dependency. The route layer
// (RBAC scope + permission gates) is verified by the dedicated
// RequiresClusterRead test which wires a real authorization engine and
// asserts the 403 path.

package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ----------------------------------------------------------------------
// Fakes
// ----------------------------------------------------------------------

// fakeServiceMeshQuerier is the in-memory ServiceMeshQuerier used by
// the unit tests.
type fakeServiceMeshQuerier struct {
	mu       sync.Mutex
	clusters map[uuid.UUID]sqlc.Cluster
	rows     map[uuid.UUID]sqlc.ClusterServiceMesh
	upserts  []sqlc.UpsertClusterServiceMeshParams
}

func newFakeServiceMeshQuerier(clusterID uuid.UUID, name string) *fakeServiceMeshQuerier {
	return &fakeServiceMeshQuerier{
		clusters: map[uuid.UUID]sqlc.Cluster{clusterID: {ID: clusterID, Name: name}},
		rows:     map[uuid.UUID]sqlc.ClusterServiceMesh{},
	}
}

func (f *fakeServiceMeshQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeServiceMeshQuerier) GetClusterServiceMesh(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterServiceMesh, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[clusterID]
	if !ok {
		return sqlc.ClusterServiceMesh{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeServiceMeshQuerier) UpsertClusterServiceMesh(_ context.Context, arg sqlc.UpsertClusterServiceMeshParams) (sqlc.ClusterServiceMesh, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, arg)
	row := sqlc.ClusterServiceMesh{
		ClusterID:               arg.ClusterID,
		DetectedMesh:            arg.DetectedMesh,
		DetectedVersion:         arg.DetectedVersion,
		ControlPlaneNamespace:   arg.ControlPlaneNamespace,
		GatewayCount:            arg.GatewayCount,
		VirtualServiceCount:     arg.VirtualServiceCount,
		DestinationRuleCount:    arg.DestinationRuleCount,
		PeerAuthenticationCount: arg.PeerAuthenticationCount,
		ServiceProfileCount:     arg.ServiceProfileCount,
		ServerAuthCount:         arg.ServerAuthCount,
		MtlsCoveragePct:         arg.MtlsCoveragePct,
		LastError:               arg.LastError,
	}
	f.rows[arg.ClusterID] = row
	return row, nil
}

// stubServiceMeshDetector records the detect calls and writes a
// pre-canned row into the querier.
type stubServiceMeshDetector struct {
	calls    []uuid.UUID
	queries  *fakeServiceMeshQuerier
	override sqlc.UpsertClusterServiceMeshParams
	err      error
}

func (s *stubServiceMeshDetector) DetectAndUpsert(ctx context.Context, clusterID uuid.UUID) error {
	s.calls = append(s.calls, clusterID)
	if s.err != nil {
		return s.err
	}
	if s.queries != nil {
		args := s.override
		args.ClusterID = clusterID
		if args.DetectedMesh == "" {
			args.DetectedMesh = "istio"
		}
		_, _ = s.queries.UpsertClusterServiceMesh(ctx, args)
	}
	return nil
}

// stubSMRequester is a static K8sRequester that returns a single STRICT
// PeerAuthentication so the MTLS handler's tunnel-fallback path is
// covered by at least one test.
type stubSMRequester struct{}

func (stubSMRequester) Do(_ context.Context, _, _, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	if path == "/apis/security.istio.io/v1beta1/peerauthentications" {
		body := map[string]any{
			"items": []map[string]any{
				{
					"metadata": map[string]any{"namespace": "app1"},
					"spec":     map[string]any{"mtls": map[string]any{"mode": "STRICT"}},
				},
				{
					"metadata": map[string]any{"namespace": "app1"},
					"spec":     map[string]any{"mtls": map[string]any{"mode": "PERMISSIVE"}},
				},
			},
		}
		raw, _ := json.Marshal(body)
		return &protocol.K8sResponsePayload{
			StatusCode: http.StatusOK,
			Body:       base64.StdEncoding.EncodeToString(raw),
		}, nil
	}
	return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
}

// stubServiceMeshRBACQuerier returns the configured bindings. Mirrors
// the pattern used in resources_search_test.go.
type stubServiceMeshRBACQuerier struct {
	bindings []rbac.RoleBinding
}

func (s stubServiceMeshRBACQuerier) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return s.bindings, nil
}

// smReq builds a chi-aware request with the cluster_id URL param.
func smReq(t *testing.T, method, target string, clusterID uuid.UUID) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req
}

// unwrapDataResp pops the {"data": ...} wrapper our response helper adds.
func unwrapDataResp(t *testing.T, rr *httptest.ResponseRecorder, out any) {
	t.Helper()
	var wrapped struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode wrapper: %v body=%s", err, rr.Body.String())
	}
	if err := json.Unmarshal(wrapped.Data, out); err != nil {
		t.Fatalf("decode data: %v body=%s", err, rr.Body.String())
	}
}

// ----------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------

func TestServiceMeshHandler_GetReturnsDetection(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeServiceMeshQuerier(clusterID, "c1")
	q.rows[clusterID] = sqlc.ClusterServiceMesh{
		ClusterID:       clusterID,
		DetectedMesh:    "istio",
		DetectedVersion: "1.22.4",
		GatewayCount:    5,
	}
	h := NewServiceMeshHandler(q)
	rr := httptest.NewRecorder()
	h.Get(rr, smReq(t, http.MethodGet, "/api/v1/clusters/X/service-mesh/", clusterID))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp ServiceMeshDetectionResponse
	unwrapDataResp(t, rr, &resp)
	if resp.DetectedMesh != "istio" {
		t.Errorf("detected_mesh = %q", resp.DetectedMesh)
	}
	if resp.DetectedVersion != "1.22.4" || resp.GatewayCount != 5 {
		t.Errorf("body = %+v", resp)
	}
}

func TestServiceMeshHandler_GetReturnsEmptyWhenNoRow(t *testing.T) {
	// A cluster that has never been detected. The Get must return
	// the "unknown" placeholder instead of 404.
	clusterID := uuid.New()
	q := newFakeServiceMeshQuerier(clusterID, "c1")
	h := NewServiceMeshHandler(q)
	rr := httptest.NewRecorder()
	h.Get(rr, smReq(t, http.MethodGet, "/", clusterID))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp ServiceMeshDetectionResponse
	unwrapDataResp(t, rr, &resp)
	if resp.DetectedMesh != "unknown" {
		t.Errorf("detected_mesh = %q, want unknown", resp.DetectedMesh)
	}
}

func TestServiceMeshHandler_DetectFiresWorkerInline(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeServiceMeshQuerier(clusterID, "c1")
	d := &stubServiceMeshDetector{queries: q, override: sqlc.UpsertClusterServiceMeshParams{
		DetectedMesh:    "istio",
		DetectedVersion: "1.22.4",
	}}
	h := NewServiceMeshHandler(q)
	h.SetDetector(d)
	rr := httptest.NewRecorder()
	h.Detect(rr, smReq(t, http.MethodPost, "/", clusterID))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(d.calls) != 1 {
		t.Fatalf("detector calls = %d, want 1", len(d.calls))
	}
	if d.calls[0] != clusterID {
		t.Errorf("detector called for %s, want %s", d.calls[0], clusterID)
	}
	var resp ServiceMeshDetectionResponse
	unwrapDataResp(t, rr, &resp)
	if resp.DetectedMesh != "istio" {
		t.Errorf("post-detect mesh = %q", resp.DetectedMesh)
	}
}

func TestServiceMeshHandler_DetectReturnsServiceUnavailableWhenUnwired(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeServiceMeshQuerier(clusterID, "c1")
	h := NewServiceMeshHandler(q)
	// no SetDetector — handler should 503 with detector_unwired.
	rr := httptest.NewRecorder()
	h.Detect(rr, smReq(t, http.MethodPost, "/", clusterID))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestServiceMeshHandler_MTLSAggregateOnlyWhenRequesterMissing(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeServiceMeshQuerier(clusterID, "c1")
	q.rows[clusterID] = sqlc.ClusterServiceMesh{
		ClusterID:               clusterID,
		DetectedMesh:            "istio",
		PeerAuthenticationCount: 7,
		MtlsCoveragePct:         42,
	}
	h := NewServiceMeshHandler(q)
	// No requester wired → handler degrades to aggregate-only with notice.
	rr := httptest.NewRecorder()
	h.MTLS(rr, smReq(t, http.MethodGet, "/", clusterID))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp MTLSBreakdownResponse
	unwrapDataResp(t, rr, &resp)
	if resp.Notice == "" {
		t.Errorf("expected notice describing scaffolding-only fallback, got empty")
	}
	if resp.TotalCount != 7 || resp.Coverage != 42 {
		t.Errorf("aggregate fields lost; got total=%d coverage=%d", resp.TotalCount, resp.Coverage)
	}
	if len(resp.Rows) != 0 {
		t.Errorf("rows should be empty in aggregate-only fallback; got %d", len(resp.Rows))
	}
}

func TestServiceMeshHandler_MTLSPullsPerNamespaceWhenRequesterAvailable(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeServiceMeshQuerier(clusterID, "c1")
	q.rows[clusterID] = sqlc.ClusterServiceMesh{
		ClusterID:               clusterID,
		DetectedMesh:            "istio",
		PeerAuthenticationCount: 2,
	}
	h := NewServiceMeshHandler(q)
	h.SetRequester(stubSMRequester{})
	rr := httptest.NewRecorder()
	h.MTLS(rr, smReq(t, http.MethodGet, "/", clusterID))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp MTLSBreakdownResponse
	unwrapDataResp(t, rr, &resp)
	if len(resp.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(resp.Rows))
	}
	if resp.Rows[0].Namespace != "app1" {
		t.Errorf("ns = %q", resp.Rows[0].Namespace)
	}
	if resp.Rows[0].Mode != "STRICT" {
		t.Errorf("mode = %q, want STRICT (strict wins over permissive)", resp.Rows[0].Mode)
	}
	if resp.Rows[0].Rules != 2 {
		t.Errorf("rules = %d, want 2", resp.Rows[0].Rules)
	}
}

func TestServiceMeshHandler_RequiresClusterRead(t *testing.T) {
	// Wire a real RBAC engine + a bindings stub that grants no
	// access to the target cluster. The handler must respond 403
	// regardless of which method (Get / Detect / MTLS) is invoked.
	clusterID := uuid.New()
	q := newFakeServiceMeshQuerier(clusterID, "c1")
	h := NewServiceMeshHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubServiceMeshRBACQuerier{
		bindings: []rbac.RoleBinding{
			// Grants only on a different cluster — caller has no
			// rights on clusterID.
			{
				ClusterID: uuid.NewString(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceClusters), Verbs: []string{string(rbac.VerbRead)}}},
			},
		},
	})
	for _, tc := range []struct {
		name string
		call http.HandlerFunc
		verb string
	}{
		{"Get", h.Get, http.MethodGet},
		{"Detect", h.Detect, http.MethodPost},
		{"MTLS", h.MTLS, http.MethodGet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := smReq(t, tc.verb, "/", clusterID)
			req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(),
				&middleware.AuthenticatedUser{ID: uuid.NewString()}))
			rr := httptest.NewRecorder()
			tc.call(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s status=%d body=%s; want 403", tc.name, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestServiceMeshHandler_NotFoundWhenClusterUnknown(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeServiceMeshQuerier(uuid.New(), "other") // different cluster
	h := NewServiceMeshHandler(q)
	rr := httptest.NewRecorder()
	h.Get(rr, smReq(t, http.MethodGet, "/", clusterID))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404 for unknown cluster", rr.Code)
	}
}
