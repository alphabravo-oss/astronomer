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
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourcePods), Verbs: []string{string(rbac.VerbRead)}}},
			},
			{
				ClusterID: clusterB.String(),
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceWorkloads), Verbs: []string{string(rbac.VerbRead)}}},
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
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceWorkloads), Verbs: []string{string(rbac.VerbRead)}}},
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
				RoleRules: []rbac.Rule{{Resource: string(rbac.ResourcePods), Verbs: []string{string(rbac.VerbRead)}}},
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
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	results, _ := body["results"].([]any)
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %+v", results)
	}
}
