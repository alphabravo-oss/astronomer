package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestOperatorWorkloadNamespacePage(t *testing.T) {
	cluster := uuid.New()
	for _, tc := range []struct {
		query         string
		status, total int
		want          string
		calls         int
	}{
		{"namespaces=team-b&namespaces=team-a&kind=Deployment&limit=1&offset=1", 200, 2, "team-b", 2},
		{"namespaces=team-a&namespaces=forbidden&kind=Deployment", 200, 1, "team-a", 1},
		{"namespaces=&kind=Deployment", 200, 0, "", 0},
		{"namespace=team-a&namespaces=team-b", 400, 0, "", 0},
		{"namespace=team-a&namespace=team-b", 400, 0, "", 0},
		{"namespaces=../bad", 400, 0, "", 0},
		{"namespaces[]=team-a", 400, 0, "", 0},
		{"namespaces[0]=team-a", 400, 0, "", 0},
		{"namespace[]=team-a", 400, 0, "", 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			stub := &stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
				ns := "team-a"
				if strings.Contains(req.Path, "/namespaces/team-b/") {
					ns = "team-b"
				}
				if strings.Contains(req.Path, "forbidden") || !strings.Contains(req.Path, "/namespaces/") {
					t.Fatalf("unauthorized upstream query: %s", req.Path)
				}
				if !strings.Contains(req.Path, "limit=") {
					t.Fatalf("unbounded query: %s", req.Path)
				}
				body, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"namespace": ns, "name": "same"}}}})
				return &protocol.K8sResponsePayload{StatusCode: 200, Body: base64.StdEncoding.EncodeToString(body)}, nil
			}}
			h := NewWorkloadHandlerWithRequester(stub)
			h.SetNamespaceScopedRBAC(true)
			h.SetAuthorization(rbac.NewEngine(), stubWorkloadRBACQuerier{bindings: []rbac.RoleBinding{
				{ClusterID: cluster.String(), Namespace: "team-a", RoleRules: []rbac.Rule{{Resource: "workloads", Verbs: []string{"list"}}}},
				{ClusterID: cluster.String(), Namespace: "team-b", RoleRules: []rbac.Rule{{Resource: "workloads", Verbs: []string{"list"}}}},
			}})
			rec := httptest.NewRecorder()
			h.List(rec, authedCatalogReq("GET", "/?"+tc.query, map[string]string{"cluster_id": cluster.String()}))
			if rec.Code != tc.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if len(stub.snapshot()) != tc.calls {
				t.Fatalf("upstream calls %d want %d", len(stub.snapshot()), tc.calls)
			}
			if tc.status != 200 {
				return
			}
			var out listEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Pagination.Total == nil || *out.Pagination.Total != int64(tc.total) {
				t.Fatalf("total %+v", out.Pagination)
			}
			if tc.want != "" && (len(out.Data) != 1 || out.Data[0]["namespace"] != tc.want) {
				t.Fatalf("wrong page %+v", out.Data)
			}
		})
	}
}

func TestOperatorReleaseReadbackAuthorizationAndRedaction(t *testing.T) {
	cluster, foreign, id := uuid.New(), uuid.New(), uuid.New()
	q := newIDORCatalogQuerier()
	q.installs[id] = sqlc.InstalledChart{ID: id, ClusterID: cluster, ReleaseName: "dex", ToolSlug: pgtype.Text{String: "dex", Valid: true}, ValuesOverride: "private-value"}
	h := NewCatalogHandler(q)
	for _, tc := range []struct {
		scope  uuid.UUID
		id     uuid.UUID
		status int
	}{{foreign, id, 403}, {cluster, id, 200}, {cluster, uuid.New(), 404}} {
		h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: catalogReadBindings(tc.scope)})
		rec := httptest.NewRecorder()
		h.GetInstalledChart(rec, authedCatalogReq("GET", "/", map[string]string{"id": tc.id.String()}))
		if rec.Code != tc.status {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "private-value") || strings.Contains(rec.Body.String(), "values_override") {
			t.Fatal("release metadata leaked values")
		}
		if tc.status == 200 && !strings.Contains(rec.Body.String(), `"source_kind":"tool"`) {
			t.Fatal("release lost ownership")
		}
	}
	row := enrichedInstalledRowJSON(sqlc.InstalledChartWithMetadata{InstalledChart: q.installs[id]})
	if _, ok := row["values_override"]; ok {
		t.Fatal("cluster-reader list leaked values")
	}
}

func TestOperatorPipelineReadbackPreservesOpaqueConfiguration(t *testing.T) {
	q := newLoggingFakeQuerier()
	cluster := uuid.New()
	pipeline, err := q.CreateLoggingPipeline(context.Background(), sqlc.CreateLoggingPipelineParams{Name: "pipeline", ClusterID: cluster, Namespaces: json.RawMessage(`[]`), Labels: json.RawMessage(`{"owner":"ops"}`), Filters: json.RawMessage(`{"future":{"enabled":true}}`)})
	if err != nil {
		t.Fatal(err)
	}
	h := NewLoggingHandler(q)
	for _, tc := range []struct {
		scope  uuid.UUID
		status int
	}{{uuid.New(), 403}, {cluster, 200}} {
		h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{ClusterID: tc.scope.String(), RoleRules: []rbac.Rule{{Resource: "logging", Verbs: []string{"read"}}}}}})
		rec := httptest.NewRecorder()
		h.GetPipeline(rec, authedCatalogReq("GET", "/", map[string]string{"id": pipeline.ID.String()}))
		if rec.Code != tc.status {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		if tc.status == 200 {
			var out struct {
				Data loggingPipelineResponse `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if string(out.Data.Filters) != string(pipeline.Filters) || string(out.Data.Labels) != string(pipeline.Labels) {
				t.Fatal("opaque configuration changed")
			}
		}
	}
}

func TestOperatorRestoreReadbackRequiresBothClusters(t *testing.T) {
	source, target := uuid.New(), uuid.New()
	q := newFakeSnapshotQuerier(source, "source")
	snapshot, _ := q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{ClusterID: source, Phase: "Completed"})
	restore, _ := q.CreateClusterRestore(context.Background(), sqlc.CreateClusterRestoreParams{SnapshotID: snapshot.ID, TargetClusterID: target, Phase: "PartiallyFailed"})
	h := NewClusterSnapshotsHandler(q)
	for _, tc := range []struct {
		sourceRead bool
		requested  uuid.UUID
		status     int
	}{{false, target, 404}, {true, source, 404}, {true, target, 200}} {
		bindings := []rbac.RoleBinding{{ClusterID: target.String(), RoleRules: []rbac.Rule{{Resource: "clusters", Verbs: []string{"read"}}}}}
		if tc.sourceRead {
			bindings = append(bindings, rbac.RoleBinding{ClusterID: source.String(), RoleRules: []rbac.Rule{{Resource: "clusters", Verbs: []string{"read"}}}})
		}
		h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: bindings})
		rec := httptest.NewRecorder()
		h.GetRestore(rec, authedCatalogReq("GET", "/", map[string]string{"cluster_id": tc.requested.String(), "id": restore.ID.String()}))
		if rec.Code != tc.status {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		if tc.status == 200 {
			var out RestoreResponse
			unwrap(t, rec, &out)
			if out.SourceClusterID != source || out.TargetClusterID != target || out.Phase != "PartiallyFailed" {
				t.Fatalf("wrong restore %+v", out)
			}
		}
	}
}

func TestOperatorRestoreRejectsUnauthorizedTargetBeforePreflight(t *testing.T) {
	source, target := uuid.New(), uuid.New()
	q := newFakeSnapshotQuerier(source, "source")
	snapshot, _ := q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{ClusterID: source, Phase: "Completed"})
	h := NewClusterSnapshotsHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{ClusterID: source.String(), RoleRules: []rbac.Rule{{Resource: "clusters", Verbs: []string{"update"}}}}}})
	req := authedCatalogReq(http.MethodPost, "/", map[string]string{"cluster_id": source.String(), "id": snapshot.ID.String()})
	req.Body = http.NoBody
	body := `{"target_cluster_id":"` + target.String() + `"}`
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Idempotency-Key", "restore-denied")
	rec := httptest.NewRecorder()
	h.CreateRestore(rec, req)
	if rec.Code != 403 || len(q.restores) != 0 || len(q.taskOutbox) != 0 {
		t.Fatalf("unauthorized restore accepted: %d %s", rec.Code, rec.Body.String())
	}
}

func TestOperatorRestoreUsesTargetNamespaceAndVerifiesSourceStore(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			source, target := uuid.New(), uuid.New()
			q := newFakeSnapshotQuerier(source, "source")
			q.AddCluster(sqlc.Cluster{ID: target, Name: "target"})
			snapshot, _ := q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{ClusterID: source, VeleroName: "backup", VeleroNamespace: "velero", Phase: "Completed"})
			requester := newFakeSnapshotRequester()
			store := func(bucket string) fakeSnapshotResponse {
				return fakeSnapshotResponse{Status: 200, Body: map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "default"}, "spec": map[string]any{"provider": "aws", "default": true, "objectStorage": map[string]any{"bucket": bucket}}}}}}
			}
			requester.setResponse("/apis/velero.io/v1/namespaces/recovery/backupstoragelocations", store("snapshots"))
			sourceBucket := "snapshots"
			if !known {
				sourceBucket = ""
			}
			requester.setResponse("/apis/velero.io/v1/namespaces/velero/backupstoragelocations", store(sourceBucket))
			h := NewClusterSnapshotsHandler(q)
			h.SetRequester(requester)
			h.SetRunTx(fakeClusterSnapshotRunTx(q))
			body := mustSnapshotJSON(t, map[string]any{"target_cluster_id": target.String(), "velero_namespace": "recovery"})
			req := snapshotReq(t, "POST", "/", body, map[string]string{"cluster_id": source.String(), "id": snapshot.ID.String()})
			rec := httptest.NewRecorder()
			h.CreateRestore(rec, req)
			if !known {
				if rec.Code != 409 || len(q.restores) != 0 {
					t.Fatalf("unverified store queued: %d %s", rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != 202 {
				t.Fatalf("custom namespace restore: %d %s", rec.Code, rec.Body.String())
			}
			var out RestoreResponse
			unwrap(t, rec, &out)
			if out.VeleroNamespace != "recovery" || out.SourceClusterID != source || rec.Header().Get("Location") != "/api/v1/clusters/"+target.String()+"/snapshot-restores/"+out.ID.String()+"/" {
				t.Fatalf("wrong receipt %+v %v", out, rec.Header())
			}
		})
	}
}
