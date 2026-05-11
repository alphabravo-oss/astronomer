package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type clusterRegistryTestQuerier struct {
	deletedRegistryConfigFor uuid.UUID
	deleteRegistryConfigErr  error
}

func (q *clusterRegistryTestQuerier) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}

func (q *clusterRegistryTestQuerier) GetClusterByName(context.Context, string) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}

func (q *clusterRegistryTestQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return nil, nil
}

func (q *clusterRegistryTestQuerier) CreateCluster(context.Context, sqlc.CreateClusterParams) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}

func (q *clusterRegistryTestQuerier) UpdateCluster(context.Context, sqlc.UpdateClusterParams) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}

func (q *clusterRegistryTestQuerier) DeleteCluster(context.Context, uuid.UUID) error { return nil }

func (q *clusterRegistryTestQuerier) CountClusters(context.Context) (int64, error) { return 0, nil }

func (q *clusterRegistryTestQuerier) GetClusterHealthStatus(context.Context, uuid.UUID) (sqlc.ClusterHealthStatus, error) {
	return sqlc.ClusterHealthStatus{}, nil
}

func (q *clusterRegistryTestQuerier) CreateClusterRegistrationToken(context.Context, sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error) {
	return sqlc.ClusterRegistrationToken{}, nil
}

func (q *clusterRegistryTestQuerier) GetRegistrationTokenByToken(context.Context, string) (sqlc.ClusterRegistrationToken, error) {
	return sqlc.ClusterRegistrationToken{}, nil
}

func (q *clusterRegistryTestQuerier) MarkRegistrationTokenUsed(context.Context, uuid.UUID) error {
	return nil
}

func (q *clusterRegistryTestQuerier) GetClusterRegistryConfig(context.Context, uuid.UUID) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, nil
}

func (q *clusterRegistryTestQuerier) UpsertClusterRegistryConfig(context.Context, sqlc.UpsertClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, nil
}

func (q *clusterRegistryTestQuerier) DeleteClusterRegistryConfig(_ context.Context, clusterID uuid.UUID) error {
	q.deletedRegistryConfigFor = clusterID
	return q.deleteRegistryConfigErr
}

// ListClusterConditions satisfies the ClusterQuerier interface introduced by
// the cluster-conditions reconciler. This stub is only used by the registry
// tests, which never exercise the conditions endpoint, so a nil return is
// sufficient.
func (q *clusterRegistryTestQuerier) ListClusterConditions(context.Context, uuid.UUID) ([]sqlc.ClusterCondition, error) {
	return nil, nil
}

// Cluster-decommission stubs — added by the cluster.deletion reconciler.
// These tests don't exercise the decommission flow, so nil/zero values are
// fine and keep the interface satisfied.
func (q *clusterRegistryTestQuerier) CreateClusterDecommission(context.Context, sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error) {
	return sqlc.ClusterDecommission{}, nil
}

func (q *clusterRegistryTestQuerier) GetLatestClusterDecommissionByCluster(context.Context, uuid.UUID) (sqlc.ClusterDecommission, error) {
	return sqlc.ClusterDecommission{}, nil
}

func TestDeleteRegistryConfig(t *testing.T) {
	clusterID := uuid.New()
	q := &clusterRegistryTestQuerier{}
	h := NewClusterHandler(q)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/"+clusterID.String()+"/registry/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()

	h.DeleteRegistryConfig(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if q.deletedRegistryConfigFor != clusterID {
		t.Fatalf("deleted registry config for %s, want %s", q.deletedRegistryConfigFor, clusterID)
	}
}

func TestDeleteRegistryConfigRejectsBadID(t *testing.T) {
	q := &clusterRegistryTestQuerier{}
	h := NewClusterHandler(q)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/not-a-uuid/registry/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "not-a-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()

	h.DeleteRegistryConfig(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if q.deletedRegistryConfigFor != uuid.Nil {
		t.Fatalf("unexpected delete call for %s", q.deletedRegistryConfigFor)
	}
}
