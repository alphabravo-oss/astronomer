package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
)

// quotaClusterQuerier is the smallest QuotaQuerier the global cluster
// check actually needs: GetQuotaPlan + CountTotalClusters.
type quotaClusterQuerier struct {
	plan  sqlc.QuotaPlan
	total int64
}

func (c *quotaClusterQuerier) GetQuotaPlan(_ context.Context, _ string) (sqlc.QuotaPlan, error) {
	return c.plan, nil
}
func (c *quotaClusterQuerier) GetEffectiveQuotaForUser(_ context.Context, _ uuid.UUID) (sqlc.GetEffectiveQuotaForUserRow, error) {
	return sqlc.GetEffectiveQuotaForUserRow{}, errors.New("unused")
}
func (c *quotaClusterQuerier) GetEffectiveQuotaForProject(_ context.Context, _ uuid.UUID) (sqlc.GetEffectiveQuotaForProjectRow, error) {
	return sqlc.GetEffectiveQuotaForProjectRow{}, errors.New("unused")
}
func (c *quotaClusterQuerier) CountClustersInProject(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (c *quotaClusterQuerier) CountMembersInProject(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (c *quotaClusterQuerier) CountProjectsForUser(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (c *quotaClusterQuerier) CountActiveTokensForUser(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (c *quotaClusterQuerier) CountTotalClusters(_ context.Context) (int64, error) {
	return c.total, nil
}
func (c *quotaClusterQuerier) CountTotalActiveUsers(_ context.Context) (int64, error) {
	return 0, nil
}

// TestClusterHandler_GlobalCapReject covers the 429 path on the
// fleet-wide cluster cap. Because the check runs BEFORE CreateCluster,
// we don't need a working cluster querier; the handler short-circuits.
func TestClusterHandler_GlobalCapReject(t *testing.T) {
	h := NewClusterHandler(nil)
	enf := quota.New(&quotaClusterQuerier{
		plan: sqlc.QuotaPlan{
			Name: "global", Enforcement: "hard", MaxTotalClusters: 10,
		},
		total: 10,
	}, nil)
	h.SetQuotaEnforcer(enf)

	body, _ := json.Marshal(CreateClusterRequest{Name: "test-cluster", DisplayName: "Test"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 global cap reject, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errMap, _ := resp["error"].(map[string]any)
	if errMap["limit"] != "max_total_clusters" {
		t.Errorf("expected limit=max_total_clusters, got %v", errMap["limit"])
	}
}
