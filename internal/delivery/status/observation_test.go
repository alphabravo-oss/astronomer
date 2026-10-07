package status

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type observationTransaction struct {
	*fakeTransaction
	accepted []sqlc.AcceptDeliveryStatusInventoryParams
}

func (f *observationTransaction) AcceptDeliveryStatusInventory(ctx context.Context, arg sqlc.AcceptDeliveryStatusInventoryParams) (sqlc.AcceptDeliveryStatusInventoryRow, error) {
	f.accepted = append(f.accepted, arg)
	return f.fakeTransaction.AcceptDeliveryStatusInventory(ctx, arg)
}

func TestIngestPreservesSourceObservationAcrossCoalescing(t *testing.T) {
	sourceTime := time.Now().UTC().Add(-2 * time.Minute)
	payload := validStatus()
	payload.Deployments = nil
	payload.ControllerInventory.Observation = &protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &sourceTime}
	payload.ControllerInventory.SystemComponents = []protocol.SystemComponent{{ID: "app", Name: "app", Health: "healthy", Observation: payload.ControllerInventory.Observation}}
	payload.StatusDigest = payload.SemanticDigest()
	tx := &observationTransaction{fakeTransaction: &fakeTransaction{}}
	ingester := NewIngester(&fakeRunner{tx: tx})
	for n := 0; n < 2; n++ {
		tx.unchanged = n > 0
		payload.SessionSequence++
		if err := ingester.Ingest(context.Background(), clusterID, connectionID, "session", payload); err != nil {
			t.Fatal(err)
		}
	}
	if len(tx.accepted) != 2 {
		t.Fatal("coalesced status skipped inventory acceptance")
	}
	for _, arg := range tx.accepted {
		if !arg.ObservedAt.Valid || !arg.ObservedAt.Time.Equal(sourceTime) {
			t.Fatalf("receipt time replaced source observation: %v", arg.ObservedAt)
		}
		var components []protocol.SystemComponent
		if err := json.Unmarshal(arg.SystemComponents, &components); err != nil {
			t.Fatal(err)
		}
		if len(components) != 1 || components[0].Observation == nil || !components[0].Observation.ObservedAt.Equal(sourceTime) {
			t.Fatal("component freshness lost during JSON persistence")
		}
	}
}

func TestIngestLegacyAndNeverObservedInventoryTimes(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		payload := validStatus()
		payload.Deployments = nil
		if !legacy {
			payload.ControllerInventory.Ready = false
			payload.ControllerInventory.Observation = &protocol.DeliveryObservation{State: protocol.ObservationUnsynced}
		}
		payload.StatusDigest = payload.SemanticDigest()
		tx := &observationTransaction{fakeTransaction: &fakeTransaction{}}
		start := time.Now().UTC()
		if err := NewIngester(&fakeRunner{tx: tx}).Ingest(context.Background(), clusterID, connectionID, "session", payload); err != nil {
			t.Fatal(err)
		}
		at := tx.accepted[0].ObservedAt
		if legacy {
			if !at.Valid || at.Time.Before(start) || at.Time.After(time.Now().UTC()) {
				t.Fatal("legacy receipt-time semantics changed")
			}
		} else if at.Valid || tx.code != "observation_unsynced" {
			t.Fatalf("never-observed inventory became fresh: %v %s", at, tx.code)
		}
	}
}

func TestIngestRejectsInvalidFreshnessBeforeTransaction(t *testing.T) {
	payload := validStatus()
	future := time.Now().Add(time.Hour)
	payload.ControllerInventory.Observation = &protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &future}
	payload.StatusDigest = payload.SemanticDigest()
	runner := &fakeRunner{tx: &fakeTransaction{}}
	if err := NewIngester(runner).Ingest(context.Background(), clusterID, connectionID, "session", payload); err == nil || runner.called {
		t.Fatal("invalid freshness reached persistence")
	}
}

func TestSystemAssignmentPreservesSourceTimeAndUnknownObservation(t *testing.T) {
	sourceTime := time.Now().UTC().Add(-time.Minute)
	for _, observed := range []bool{true, false} {
		payload := validStatus()
		payload.Deployments = nil
		if observed {
			payload.ControllerInventory.Observation = &protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &sourceTime}
		} else {
			payload.ControllerInventory.Observation = &protocol.DeliveryObservation{State: protocol.ObservationUnsynced}
			payload.ControllerInventory.Ready = false
		}
		payload.StatusDigest = payload.SemanticDigest()
		tx := &systemFakeTransaction{fakeTransaction: &fakeTransaction{}, observed: sqlc.ObserveDeliverySystemAssignmentRow{PreviousPhase: "applying", Phase: "ready"}}
		before := time.Now().UTC()
		if err := NewIngester(&fakeRunner{tx: tx}).Ingest(context.Background(), clusterID, connectionID, "session", payload); err != nil {
			t.Fatal(err)
		}
		if tx.observeArg == nil {
			t.Fatal("system assignment was not observed")
		}
		at := tx.observeArg.ObservedAt
		if observed {
			if !at.Valid || !at.Time.Equal(sourceTime) {
				t.Fatal("system assignment restamped source observation")
			}
		} else if at.Valid {
			t.Fatal("never-observed system assignment acquired timestamp")
		}
		if tx.systemEvent == nil || tx.systemEvent.OccurredAt.Before(before) || tx.systemEvent.OccurredAt.After(time.Now().UTC()) {
			t.Fatal("transition event lost receipt/decision time")
		}
	}
}
