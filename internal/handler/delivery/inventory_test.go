package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type inventoryQueryFake struct {
	fleetFn    func(context.Context) ([]sqlc.ListDeliveryFleetClustersRow, error)
	rolloutsFn func(context.Context) (int64, error)
}

func (f *inventoryQueryFake) GetDeliveryControllerInventory(context.Context, sqlc.GetDeliveryControllerInventoryParams) (sqlc.DeliveryControllerInventory, error) {
	panic("unexpected GetDeliveryControllerInventory")
}
func (f *inventoryQueryFake) ListClusterDeployments(context.Context, sqlc.ListClusterDeploymentsParams) ([]sqlc.ClusterDeployment, error) {
	panic("unexpected ListClusterDeployments")
}
func (f *inventoryQueryFake) CountClusterDeployments(context.Context, sqlc.CountClusterDeploymentsParams) (int64, error) {
	panic("unexpected CountClusterDeployments")
}
func (f *inventoryQueryFake) CountDeliveryControllerCompatibility(context.Context) ([]sqlc.CountDeliveryControllerCompatibilityRow, error) {
	panic("unexpected CountDeliveryControllerCompatibility")
}
func (f *inventoryQueryFake) GetCurrentDeliverySystemRollout(context.Context) (sqlc.DeliverySystemRollout, error) {
	panic("unexpected GetCurrentDeliverySystemRollout")
}
func (f *inventoryQueryFake) ListDeliverySystemReleases(context.Context, sqlc.ListDeliverySystemReleasesParams) ([]sqlc.ListDeliverySystemReleasesRow, error) {
	panic("unexpected ListDeliverySystemReleases")
}
func (f *inventoryQueryFake) ListDeliveryFleetClusters(ctx context.Context) ([]sqlc.ListDeliveryFleetClustersRow, error) {
	if f.fleetFn == nil {
		panic("unexpected ListDeliveryFleetClusters")
	}
	return f.fleetFn(ctx)
}
func (f *inventoryQueryFake) CountActiveDeliveryRollouts(ctx context.Context) (int64, error) {
	if f.rolloutsFn == nil {
		panic("unexpected CountActiveDeliveryRollouts")
	}
	return f.rolloutsFn(ctx)
}

func TestFleetExcludesLocalFromTilesAndSurfacesAdoptedAttention(t *testing.T) {
	now := time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC)
	localID := uuid.MustParse("5e4fc110-40a2-4bce-bd26-cda32e9809c8")
	readyID := uuid.MustParse("f86508b9-586e-4499-a353-e1d0f630e501")
	brokenID := uuid.MustParse("f051ae19-0455-4ea9-a90f-af5a58a91007")
	handler := NewInventoryHandler(&inventoryQueryFake{
		fleetFn: func(context.Context) ([]sqlc.ListDeliveryFleetClustersRow, error) {
			return []sqlc.ListDeliveryFleetClustersRow{
				{
					ID: localID, Name: "local", DisplayName: "Management", IsLocal: true,
					Connected: true, CompatibilityStatus: "unknown",
					LastHeartbeat: ts(now.Add(-time.Minute)),
					Annotations:   json.RawMessage(`{"astronomer.io/agent-privilege-profile":"viewer"}`),
				},
				{
					ID: readyID, Name: "adopt-a", DisplayName: "Adopt A",
					Connected: true, CompatibilityStatus: "compatible", InventoryReady: true,
					FluxVersion: "v2.9.3", AgentVersion: "v1.0.0", KubernetesVersion: "v1.35.7+k3s1",
					AssignmentCount: 2, ReadyCount: 2,
					LastHeartbeat:       ts(now.Add(-30 * time.Second)),
					InventoryObservedAt: ts(now.Add(-time.Minute)),
					Annotations:         json.RawMessage(`{"astronomer.io/agent-privilege-profile":"admin"}`),
				},
				{
					ID: brokenID, Name: "adopt-b", DisplayName: "Adopt B",
					Connected: false, CompatibilityStatus: "incompatible", InventoryReady: false,
					InventoryErrorCode: "controller_inventory_stale",
					AssignmentCount:    2, FailedCount: 1, DriftedCount: 1,
					Annotations: json.RawMessage(`{"astronomer.io/agent-privilege-profile":"admin"}`),
				},
			}, nil
		},
		rolloutsFn: func(context.Context) (int64, error) { return 1, nil },
	})
	handler.now = func() time.Time { return now }

	recorder := httptest.NewRecorder()
	handler.Fleet(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/delivery/fleet/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var envelope struct {
		Data DeliveryFleet `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data
	if got.Summary != (DeliveryFleetSummary{
		AdoptedClusters: 2, FluxReady: 1, Incompatible: 1, Disconnected: 1,
		Assignments: 4, Drifted: 1, Failed: 1, ActiveRollouts: 1,
	}) {
		t.Fatalf("summary=%#v", got.Summary)
	}
	if len(got.Clusters) != 3 || !got.Clusters[0].IsLocal || got.Clusters[1].PrivilegeProfile != "admin" {
		t.Fatalf("clusters=%#v", got.Clusters)
	}
	if len(got.Attention) != 1 || got.Attention[0].ClusterID != brokenID || got.Attention[0].Reason != "disconnected" {
		t.Fatalf("attention=%#v", got.Attention)
	}
	if !hasFleetCount(got.Distributions.Compatibility, "compatible", 1) ||
		!hasFleetCount(got.Distributions.Privilege, "admin", 2) {
		t.Fatalf("distributions=%#v", got.Distributions)
	}
}

func TestFleetMarksConnectedAdoptedClusterStaleAfterFiveMinutes(t *testing.T) {
	now := time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC)
	id := uuid.New()
	fleet := buildDeliveryFleet([]sqlc.ListDeliveryFleetClustersRow{{
		ID: id, Name: "adopt-a", DisplayName: "Adopt A",
		Connected: true, CompatibilityStatus: "compatible", InventoryReady: true,
		LastHeartbeat: ts(now.Add(-6 * time.Minute)),
		Annotations:   json.RawMessage(`{}`),
	}}, 0, now)
	if fleet.Summary.Stale != 1 || !fleet.Clusters[0].Stale {
		t.Fatalf("expected stale cluster: %#v", fleet)
	}
	if len(fleet.Attention) != 1 || fleet.Attention[0].Reason != "stale" {
		t.Fatalf("attention=%#v", fleet.Attention)
	}
}

func TestFleetUnavailableWithoutQueries(t *testing.T) {
	recorder := httptest.NewRecorder()
	NewInventoryHandler(nil).Fleet(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/delivery/fleet/", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func ts(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func hasFleetCount(items []DeliveryFleetCount, key string, count int64) bool {
	for _, item := range items {
		if item.Key == key && item.Count == count {
			return true
		}
	}
	return false
}
