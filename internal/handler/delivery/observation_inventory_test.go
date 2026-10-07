package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type observationInventoryQuery struct {
	resourceInventoryQueryFake
	observedAt time.Time
	components json.RawMessage
}

func (q *observationInventoryQuery) GetDeliveryControllerInventory(ctx context.Context, params sqlc.GetDeliveryControllerInventoryParams) (sqlc.GetDeliveryControllerInventoryRow, error) {
	row, err := q.resourceInventoryQueryFake.GetDeliveryControllerInventory(ctx, params)
	row.ObservedAt = pgtype.Timestamptz{Time: q.observedAt, Valid: true}
	row.SystemComponents = q.components
	return row, err
}

func TestInventoryAPIExposesSourceTimeAndComponentFreshness(t *testing.T) {
	sourceTime := time.Now().UTC().Add(-time.Minute)
	components, err := json.Marshal([]protocol.SystemComponent{{ID: "app", Name: "app", Health: "unknown", Observation: &protocol.DeliveryObservation{State: protocol.ObservationStale, ObservedAt: &sourceTime}}})
	if err != nil {
		t.Fatal(err)
	}
	projectID, clusterID := uuid.New(), uuid.New()
	queries := &observationInventoryQuery{resourceInventoryQueryFake: resourceInventoryQueryFake{projectID: projectID, clusterID: clusterID, inventory: json.RawMessage(`{}`), t: t}, observedAt: sourceTime, components: components}
	request := requestWithPathParams(http.MethodGet, "/inventory?project_id="+projectID.String(), nil, map[string]string{"clusterId": clusterID.String()})
	recorder := httptest.NewRecorder()
	NewInventoryHandler(queries).Cluster(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Inventory struct {
				ObservedAt time.Time                  `json:"observed_at"`
				Components []protocol.SystemComponent `json:"system_components"`
			} `json:"controller_inventory"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	inventory := response.Data.Inventory
	if !inventory.ObservedAt.Equal(sourceTime) || len(inventory.Components) != 1 {
		t.Fatal("API replaced or omitted source observation")
	}
	observation := inventory.Components[0].Observation
	if observation == nil || observation.State != protocol.ObservationStale || observation.ObservedAt == nil || !observation.ObservedAt.Equal(sourceTime) {
		t.Fatal("API lost nested freshness contract")
	}
}
