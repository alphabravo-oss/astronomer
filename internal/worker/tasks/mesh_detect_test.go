// Worker tests for the mesh:detect periodic task (migration 071).

package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// fakeMeshQuerier is the in-memory implementation of MeshDetectQuerier.
type fakeMeshQuerier struct {
	mu       sync.Mutex
	clusters []sqlc.Cluster
	rows     map[uuid.UUID]sqlc.ClusterServiceMesh
	upserts  []sqlc.UpsertClusterServiceMeshParams
}

func newFakeMeshQuerier() *fakeMeshQuerier {
	return &fakeMeshQuerier{rows: map[uuid.UUID]sqlc.ClusterServiceMesh{}}
}

func (f *fakeMeshQuerier) ListClusters(_ context.Context, _ sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sqlc.Cluster{}, f.clusters...), nil
}

func (f *fakeMeshQuerier) GetClusterServiceMesh(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterServiceMesh, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[clusterID]
	if !ok {
		return sqlc.ClusterServiceMesh{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeMeshQuerier) UpsertClusterServiceMesh(_ context.Context, arg sqlc.UpsertClusterServiceMeshParams) (sqlc.ClusterServiceMesh, error) {
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

// scriptedRequester is the worker-test version of mesh.fakeRequester —
// duplicated here so the worker test stays independent of the mesh
// package's _test.go.
type scriptedRequester struct {
	responses map[string]scriptedResponse
}

type scriptedResponse struct {
	status int
	body   any
}

func newScriptedRequester() *scriptedRequester {
	return &scriptedRequester{responses: map[string]scriptedResponse{}}
}

func (s *scriptedRequester) set(method, path string, status int, body any) {
	s.responses[method+" "+path] = scriptedResponse{status: status, body: body}
}

func (s *scriptedRequester) Do(_ context.Context, _ string, method, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	key := method + " " + path
	resp, ok := s.responses[key]
	if !ok {
		// Prefix-match for query-string variants.
		for k, v := range s.responses {
			if len(key) >= len(k) && key[:len(k)] == k {
				resp = v
				ok = true
				break
			}
		}
	}
	if !ok {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
	}
	out := &protocol.K8sResponsePayload{StatusCode: resp.status}
	switch v := resp.body.(type) {
	case nil:
		// empty
	case []byte:
		out.Body = base64.StdEncoding.EncodeToString(v)
	case string:
		out.Body = base64.StdEncoding.EncodeToString([]byte(v))
	default:
		raw, _ := json.Marshal(v)
		out.Body = base64.StdEncoding.EncodeToString(raw)
	}
	return out, nil
}

// itemsList replicates the helper used in internal/mesh/detect_test.go
// so the worker tests can build the same fixtures without an export.
func meshItemsList(count int) map[string]any {
	items := make([]map[string]any, count)
	for i := range items {
		items[i] = map[string]any{}
	}
	return map[string]any{"items": items}
}

func meshNSList(names ...string) map[string]any {
	items := make([]map[string]any, 0, len(names))
	for _, n := range names {
		items = append(items, map[string]any{"metadata": map[string]any{"name": n}})
	}
	return map[string]any{"items": items}
}

// setupMeshDeps installs a fakeMeshQuerier + scriptedRequester and
// returns both. Caller is responsible for ResetMeshDetect at the end
// of the test.
func setupMeshDeps(t *testing.T) (*fakeMeshQuerier, *scriptedRequester) {
	t.Helper()
	q := newFakeMeshQuerier()
	r := newScriptedRequester()
	ConfigureMeshDetect(MeshDetectDeps{Queries: q, Requester: r})
	t.Cleanup(ResetMeshDetect)
	return q, r
}

func TestWorker_UpsertsRow(t *testing.T) {
	q, r := setupMeshDeps(t)
	clusterID := uuid.New()
	q.clusters = []sqlc.Cluster{{ID: clusterID, Name: "c1", Status: "healthy"}}
	// Bare-minimum mesh detection: one Istio gateway is enough to
	// flip the detected_mesh to "istio".
	r.set("GET", "/api/v1/namespaces", 200, meshNSList("default"))
	r.set("GET", "/apis/networking.istio.io/v1beta1/gateways", 200, meshItemsList(2))

	if err := HandleMeshDetect(context.Background(), nil); err != nil {
		t.Fatalf("HandleMeshDetect: %v", err)
	}
	if len(q.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(q.upserts))
	}
	got := q.upserts[0]
	if got.ClusterID != clusterID {
		t.Errorf("cluster_id = %s, want %s", got.ClusterID, clusterID)
	}
	if got.DetectedMesh != "istio" {
		t.Errorf("detected_mesh = %q, want istio", got.DetectedMesh)
	}
	if got.GatewayCount != 2 {
		t.Errorf("gateway_count = %d, want 2", got.GatewayCount)
	}
}

func TestWorker_NoOpForUnhealthyCluster(t *testing.T) {
	q, _ := setupMeshDeps(t)
	q.clusters = []sqlc.Cluster{
		{ID: uuid.New(), Name: "offline", Status: "unhealthy"},
		{ID: uuid.New(), Name: "pending", Status: "pending"},
	}
	if err := HandleMeshDetect(context.Background(), nil); err != nil {
		t.Fatalf("HandleMeshDetect: %v", err)
	}
	if len(q.upserts) != 0 {
		t.Errorf("unhealthy clusters should not upsert; got %d upserts", len(q.upserts))
	}
}

func TestWorker_DetectAndUpsert_RecordsMeshFlip(t *testing.T) {
	q, r := setupMeshDeps(t)
	clusterID := uuid.New()
	// Seed a prior detection of "linkerd" so this run's "istio"
	// result counts as a flip — verifies the prior-vs-new branch
	// of DetectAndUpsert is wired (we can't easily assert gauges
	// here, but we can at least assert the upsert path runs and
	// the prior read happens via the fake's GetClusterServiceMesh).
	q.rows[clusterID] = sqlc.ClusterServiceMesh{ClusterID: clusterID, DetectedMesh: "linkerd"}
	r.set("GET", "/api/v1/namespaces", 200, meshNSList("default"))
	r.set("GET", "/apis/networking.istio.io/v1beta1/gateways", 200, meshItemsList(1))
	if err := DetectAndUpsert(context.Background(), clusterID); err != nil {
		t.Fatalf("DetectAndUpsert: %v", err)
	}
	if len(q.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(q.upserts))
	}
	if got := q.upserts[0].DetectedMesh; got != "istio" {
		t.Errorf("detected_mesh = %q, want istio", got)
	}
}

func TestWorker_NotConfiguredIsNoOp(t *testing.T) {
	ResetMeshDetect()
	if err := HandleMeshDetect(context.Background(), nil); err != nil {
		t.Fatalf("HandleMeshDetect: %v", err)
	}
	if err := DetectAndUpsert(context.Background(), uuid.New()); err == nil {
		t.Errorf("DetectAndUpsert with unconfigured deps must return an error")
	}
}
