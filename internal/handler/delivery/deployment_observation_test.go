package delivery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
)

func TestClusterInventoryDeploymentObservationReadback(t *testing.T) {
	for _, state := range []protocol.ObservationState{protocol.ObservationCurrent, protocol.ObservationUnsynced} {
		projectID, clusterID := uuid.New(), uuid.New()
		at := time.Now().UTC().Add(-time.Minute)
		observation := &protocol.DeliveryObservation{State: state}
		if state == protocol.ObservationCurrent {
			observation.ObservedAt = &at
		}
		payload, err := json.Marshal(struct {
			protocol.DeliveryInventory
			Observation *protocol.DeliveryObservation `json:"observation"`
		}{Observation: observation})
		if err != nil {
			t.Fatal(err)
		}
		handler := NewInventoryHandler(&resourceInventoryQueryFake{projectID: projectID, clusterID: clusterID, inventory: payload, t: t})
		request := requestWithPathParams(http.MethodGet, "/api/v1/delivery/clusters/"+clusterID.String()+"/inventory?project_id="+projectID.String(), nil, map[string]string{"clusterId": clusterID.String()})
		recorder := httptest.NewRecorder()
		handler.Cluster(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d: %s", recorder.Code, recorder.Body.String())
		}
		var envelope struct {
			Data struct {
				Deployments []struct {
					Inventory struct {
						Observation *protocol.DeliveryObservation `json:"observation"`
					} `json:"inventory"`
				}
			} `json:"data"`
		}
		if err = json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Data.Deployments) != 1 {
			t.Fatal("deployment missing")
		}
		got := envelope.Data.Deployments[0].Inventory.Observation
		if got == nil || got.State != state {
			t.Fatal("source state lost")
		}
		if state == protocol.ObservationCurrent && (got.ObservedAt == nil || !got.ObservedAt.Equal(at)) {
			t.Fatal("source time changed")
		}
		if state == protocol.ObservationUnsynced && got.ObservedAt != nil {
			t.Fatal("never-observed source became fresh")
		}
	}
}
