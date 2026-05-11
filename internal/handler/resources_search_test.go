package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type stubResourcesSearchQuerier struct {
	clusters []sqlc.Cluster
}

func (s stubResourcesSearchQuerier) ListClustersByStatus(context.Context, sqlc.ListClustersByStatusParams) ([]sqlc.Cluster, error) {
	return s.clusters, nil
}

type stubResourcesSearchRequester struct{}

func (stubResourcesSearchRequester) Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
	body := []byte(`{"items":[]}`)
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString(body)}, nil
}

// stubPerClusterRequester returns a synthetic single-pod payload per cluster
// so the test can verify per-cluster RBAC filtering by checking that rows
// from clusters the caller cannot list never appear in the merged response.
type stubPerClusterRequester struct {
	called map[string]int
}

func (s *stubPerClusterRequester) Do(_ context.Context, clusterID, _, _ string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	if s.called == nil {
		s.called = map[string]int{}
	}
	s.called[clusterID]++
	body := []byte(`{"items":[{"metadata":{"name":"pod-` + clusterID + `","namespace":"default"},"status":{"phase":"Running"}}]}`)
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString(body)}, nil
}

type stubSearchRBACQuerier struct {
	bindings []rbac.RoleBinding
	err      error
}

func (s stubSearchRBACQuerier) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return s.bindings, s.err
}

func TestResourcesSearchAuthorizedSearchClustersFiltersByResourceType(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	clusters := []sqlc.Cluster{{ID: clusterA, Name: "a"}, {ID: clusterB, Name: "b"}}

	h := NewResourcesSearchHandler(nil, nil)
	h.SetAuthorization(rbac.NewEngine(), stubSearchRBACQuerier{
		bindings: []rbac.RoleBinding{
			{
				ClusterID: clusterA.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourcePods), Verbs: []string{string(rbac.VerbList)}}},
			},
			{
				ClusterID: clusterB.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceWorkloads), Verbs: []string{string(rbac.VerbList)}}},
			},
		},
	})

	ctx := middleware.SetAuthenticatedUserForTest(context.Background(), &middleware.AuthenticatedUser{ID: uuid.NewString()})

	podClusters, err := h.authorizedSearchClusters(ctx, clusters, rbac.ResourcePods)
	if err != nil {
		t.Fatalf("authorizedSearchClusters pods: %v", err)
	}
	if len(podClusters) != 1 || podClusters[0].ID != clusterA {
		t.Fatalf("expected only clusterA for pods search, got %+v", podClusters)
	}

	workloadClusters, err := h.authorizedSearchClusters(ctx, clusters, rbac.ResourceWorkloads)
	if err != nil {
		t.Fatalf("authorizedSearchClusters workloads: %v", err)
	}
	if len(workloadClusters) != 1 || workloadClusters[0].ID != clusterB {
		t.Fatalf("expected only clusterB for workloads search, got %+v", workloadClusters)
	}
}

func TestResourcesSearchSearchReturnsForbiddenWhenNoClusterPermission(t *testing.T) {
	clusterA := uuid.New()
	h := NewResourcesSearchHandler(
		stubResourcesSearchQuerier{clusters: []sqlc.Cluster{{ID: clusterA, Name: "a"}}},
		stubResourcesSearchRequester{},
	)
	h.SetAuthorization(rbac.NewEngine(), stubSearchRBACQuerier{
		bindings: []rbac.RoleBinding{
			{
				ClusterID: clusterA.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceWorkloads), Verbs: []string{string(rbac.VerbList)}}},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/resources/search/?type=pods", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))
	rec := httptest.NewRecorder()

	h.Search(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestResourcesSearchSearchAllowsAuthorizedType(t *testing.T) {
	clusterA := uuid.New()
	h := NewResourcesSearchHandler(
		stubResourcesSearchQuerier{clusters: []sqlc.Cluster{{ID: clusterA, Name: "a"}}},
		stubResourcesSearchRequester{},
	)
	h.SetAuthorization(rbac.NewEngine(), stubSearchRBACQuerier{
		bindings: []rbac.RoleBinding{
			{
				ClusterID: clusterA.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourcePods), Verbs: []string{string(rbac.VerbList)}}},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/resources/search/?type=pods", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))
	rec := httptest.NewRecorder()

	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	body, _ := envelope["data"].(map[string]any)
	if body == nil {
		t.Fatalf("response missing data envelope: %s", rec.Body.String())
	}
	results, _ := body["results"].([]any)
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %+v", results)
	}
}

// TestResourcesSearchSearchFiltersResultsByClusterRBAC exercises the
// end-to-end search across clusters {A, B, C} with bindings that only permit
// pods:list on cluster A, and asserts that:
//   - only cluster A's row appears in `results`
//   - the requester is never invoked for cluster B or C (i.e. we don't even
//     fan out to clusters the caller can't see, matching the
//     "skip this cluster — don't even fan out to it" requirement)
//   - clusters_queried reflects the post-filter cluster count (1), not 3
func TestResourcesSearchSearchFiltersResultsByClusterRBAC(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	clusterC := uuid.New()

	requester := &stubPerClusterRequester{}
	h := NewResourcesSearchHandler(
		stubResourcesSearchQuerier{clusters: []sqlc.Cluster{
			{ID: clusterA, Name: "a"},
			{ID: clusterB, Name: "b"},
			{ID: clusterC, Name: "c"},
		}},
		requester,
	)
	h.SetAuthorization(rbac.NewEngine(), stubSearchRBACQuerier{
		bindings: []rbac.RoleBinding{
			{
				ClusterID: clusterA.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourcePods), Verbs: []string{string(rbac.VerbList)}}},
			},
			// Intentionally no pods:list bindings for B or C. They have
			// unrelated workloads:read bindings to make sure the filter
			// matches on resource type, not just cluster scope.
			{
				ClusterID: clusterB.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceWorkloads), Verbs: []string{string(rbac.VerbRead)}}},
			},
			{
				ClusterID: clusterC.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceWorkloads), Verbs: []string{string(rbac.VerbList)}}},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/resources/search/?type=pods", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))
	rec := httptest.NewRecorder()

	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	body, _ := envelope["data"].(map[string]any)
	if body == nil {
		t.Fatalf("response missing data envelope: %s", rec.Body.String())
	}

	results, _ := body["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 row from cluster A, got %d: %+v", len(results), results)
	}
	row, _ := results[0].(map[string]any)
	if gotID, _ := row["cluster_id"].(string); gotID != clusterA.String() {
		t.Fatalf("expected row from cluster A (%s), got cluster_id=%q", clusterA, gotID)
	}

	if got, ok := body["clusters_queried"].(float64); !ok || int(got) != 1 {
		t.Fatalf("expected clusters_queried=1 (post-filter), got %v", body["clusters_queried"])
	}

	// Confirm we never even fanned out to the forbidden clusters.
	if requester.called[clusterB.String()] != 0 {
		t.Fatalf("expected zero calls to cluster B, got %d", requester.called[clusterB.String()])
	}
	if requester.called[clusterC.String()] != 0 {
		t.Fatalf("expected zero calls to cluster C, got %d", requester.called[clusterC.String()])
	}
	if requester.called[clusterA.String()] == 0 {
		t.Fatalf("expected at least one call to cluster A, got 0")
	}
}

// TestRBACResourceForTypeMapping documents and pins the resource-type →
// rbac.Resource categorization choices on the search handler so that
// adding a new entry to searchResourceDefs without updating its rbacResource
// can never silently downgrade a sensitive type to the default bucket.
func TestRBACResourceForTypeMapping(t *testing.T) {
	cases := map[string]rbac.Resource{
		"pods":                   rbac.ResourcePods,
		"events":                 rbac.ResourcePods,
		"endpoints":              rbac.ResourcePods,
		"deployments":            rbac.ResourceWorkloads,
		"statefulsets":           rbac.ResourceWorkloads,
		"daemonsets":             rbac.ResourceWorkloads,
		"replicasets":            rbac.ResourceWorkloads,
		"jobs":                   rbac.ResourceWorkloads,
		"cronjobs":               rbac.ResourceWorkloads,
		"services":               rbac.ResourceWorkloads,
		"ingresses":              rbac.ResourceWorkloads,
		"networkpolicies":        rbac.ResourceWorkloads,
		"gateways":               rbac.ResourceWorkloads,
		"httproutes":             rbac.ResourceWorkloads,
		"secrets":                rbac.ResourceWorkloads,
		"configmaps":             rbac.ResourceWorkloads,
		"persistentvolumes":      rbac.ResourceWorkloads,
		"persistentvolumeclaims": rbac.ResourceWorkloads,
		"storageclasses":         rbac.ResourceWorkloads,
		"namespaces":             rbac.ResourceClusters,
		"nodes":                  rbac.ResourceClusters,
		"completely-unknown":     rbac.ResourceWorkloads, // default fallback
		"":                       rbac.ResourceWorkloads, // default fallback
	}
	for resourceType, want := range cases {
		if got := rbacResourceForType(resourceType); got != want {
			t.Errorf("rbacResourceForType(%q) = %q, want %q", resourceType, got, want)
		}
	}
}
