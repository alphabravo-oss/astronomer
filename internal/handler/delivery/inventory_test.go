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
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type inventoryQueryFake struct {
	fleetFn    func(context.Context) ([]sqlc.ListDeliveryEstateClustersRow, error)
	rolloutsFn func(context.Context) (int64, error)
}

func (f *inventoryQueryFake) GetDeliveryControllerInventory(context.Context, sqlc.GetDeliveryControllerInventoryParams) (sqlc.GetDeliveryControllerInventoryRow, error) {
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
func (f *inventoryQueryFake) ListDeliveryEstateClusters(ctx context.Context, _ int32) ([]sqlc.ListDeliveryEstateClustersRow, error) {
	if f.fleetFn == nil {
		panic("unexpected ListDeliveryEstateClusters")
	}
	return f.fleetFn(ctx)
}
func (f *inventoryQueryFake) CountActiveDeliveryRollouts(ctx context.Context) (int64, error) {
	if f.rolloutsFn == nil {
		panic("unexpected CountActiveDeliveryRollouts")
	}
	return f.rolloutsFn(ctx)
}

func TestFleetIncludesLocalAsFirstClassTargetAndSurfacesAttention(t *testing.T) {
	now := time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC)
	localID := uuid.MustParse("5e4fc110-40a2-4bce-bd26-cda32e9809c8")
	readyID := uuid.MustParse("f86508b9-586e-4499-a353-e1d0f630e501")
	brokenID := uuid.MustParse("f051ae19-0455-4ea9-a90f-af5a58a91007")
	handler := NewInventoryHandler(&inventoryQueryFake{
		fleetFn: func(context.Context) ([]sqlc.ListDeliveryEstateClustersRow, error) {
			return []sqlc.ListDeliveryEstateClustersRow{
				{
					ID: localID, Name: "local", DisplayName: "Management", IsLocal: true,
					Connected: true, CompatibilityStatus: "compatible", InventoryReady: true,
					FluxVersion: "v2.9.3", LastHeartbeat: ts(now.Add(-time.Minute)),
					Annotations: json.RawMessage(`{"astronomer.io/agent-privilege-profile":"viewer"}`),
				},
				{
					ID: readyID, Name: "adopt-a", DisplayName: "Adopt A", Environment: "production",
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
		ManagedClusters: 3, FluxReady: 2, Incompatible: 1, Disconnected: 1,
		Assignments: 4, Drifted: 1, Failed: 1, ActiveRollouts: 1,
	}) {
		t.Fatalf("summary=%#v", got.Summary)
	}
	if len(got.Clusters) != 3 || !got.Clusters[0].IsLocal || got.Clusters[1].PrivilegeProfile != "admin" {
		t.Fatalf("clusters=%#v", got.Clusters)
	}
	if got.Clusters[1].Environment != "production" {
		t.Fatalf("environment=%q", got.Clusters[1].Environment)
	}
	if len(got.Attention) != 1 || got.Attention[0].ClusterID != brokenID || got.Attention[0].Reason != "disconnected" {
		t.Fatalf("attention=%#v", got.Attention)
	}
	if !hasFleetCount(got.Distributions.Compatibility, "compatible", 2) ||
		!hasFleetCount(got.Distributions.Privilege, "admin", 2) {
		t.Fatalf("distributions=%#v", got.Distributions)
	}
}

func TestFleetMarksConnectedAdoptedClusterStaleAfterFiveMinutes(t *testing.T) {
	now := time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC)
	id := uuid.New()
	fleet := buildDeliveryFleet([]sqlc.ListDeliveryEstateClustersRow{{
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

// The inventory endpoint forwards persisted protocol JSON unchanged; frontend
// mapClusterDeployment camelizes api_version to apiVersion for the detail view.
func TestClusterInventoryPreservesFluxResourceIdentities(t *testing.T) {
	projectID, clusterID := uuid.New(), uuid.New()
	inventory := protocol.DeliveryInventory{Entries: 3, Resources: []protocol.DeliveryResourceIdentity{
		{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "workload", Name: "app"},
		{APIVersion: "v1", Kind: "Service", Namespace: "workload", Name: "app"},
		{APIVersion: "v1", Kind: "Namespace", Name: "workload"},
	}}
	payload, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewInventoryHandler(&resourceInventoryQueryFake{projectID: projectID, clusterID: clusterID, inventory: payload, t: t})
	request := requestWithPathParams(http.MethodGet, "/api/v1/delivery/clusters/"+clusterID.String()+"/inventory?project_id="+projectID.String(), nil, map[string]string{"clusterId": clusterID.String()})
	recorder := httptest.NewRecorder()
	handler.Cluster(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Deployments []struct {
				Inventory json.RawMessage `json:"inventory"`
			} `json:"deployments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	const want = `{"entries":3,"ready":0,"failed":0,"resources":[{"api_version":"apps/v1","kind":"Deployment","namespace":"workload","name":"app"},{"api_version":"v1","kind":"Service","namespace":"workload","name":"app"},{"api_version":"v1","kind":"Namespace","name":"workload"}]}`
	if len(envelope.Data.Deployments) != 1 || string(envelope.Data.Deployments[0].Inventory) != want {
		t.Fatalf("API inventory contract = %s", recorder.Body.String())
	}
}

type resourceInventoryQueryFake struct {
	inventoryQueryFake
	projectID, clusterID uuid.UUID
	inventory            json.RawMessage
	t                    *testing.T
}

func (f *resourceInventoryQueryFake) GetDeliveryControllerInventory(_ context.Context, params sqlc.GetDeliveryControllerInventoryParams) (sqlc.GetDeliveryControllerInventoryRow, error) {
	if params.ProjectID != f.projectID || params.ClusterID != f.clusterID {
		f.t.Fatal("inventory query lost project/cluster scope")
	}
	return sqlc.GetDeliveryControllerInventoryRow{}, nil
}
func (f *resourceInventoryQueryFake) ListClusterDeployments(_ context.Context, params sqlc.ListClusterDeploymentsParams) ([]sqlc.ClusterDeployment, error) {
	if params.ProjectID != f.projectID || params.ClusterID.Bytes != f.clusterID || !params.ClusterID.Valid {
		f.t.Fatal("deployment query lost project/cluster scope")
	}
	return []sqlc.ClusterDeployment{{Inventory: f.inventory}}, nil
}
func (f *resourceInventoryQueryFake) CountClusterDeployments(_ context.Context, params sqlc.CountClusterDeploymentsParams) (int64, error) {
	if params.ProjectID != f.projectID || params.ClusterID.Bytes != f.clusterID || !params.ClusterID.Valid {
		f.t.Fatal("count query lost project/cluster scope")
	}
	return 1, nil
}
