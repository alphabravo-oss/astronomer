package status

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func modernDeploymentReport() protocol.DeliveryStatusV2 {
	s := validStatus()
	at := time.Now().UTC().Add(-time.Minute)
	s.ControllerInventory.Observation = &protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &at}
	s.Deployments[0].Observation = &protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &at}
	s.StatusDigest = s.SemanticDigest()
	return s
}
func TestDeploymentSourcePersistenceAndCoalescedRefresh(t *testing.T) {
	s := modernDeploymentReport()
	d := s.Deployments[0]
	tx := &fakeTransaction{current: sqlc.ClusterDeployment{ID: deploymentID, DesiredGeneration: d.Generation, DesiredSpecDigest: d.SpecDigest, Phase: "applying"}}
	ingester := NewIngester(&fakeRunner{tx: tx})
	start := time.Now().UTC()
	if err := ingester.Ingest(context.Background(), clusterID, connectionID, "session", s); err != nil {
		t.Fatal(err)
	}
	if tx.updated == nil || !tx.updated.LastObservedAt.Valid || !tx.updated.LastObservedAt.Time.Equal(*d.Observation.ObservedAt) {
		t.Fatal("source time lost")
	}
	if tx.event == nil || tx.event.ObservedAt.Before(start) {
		t.Fatal("modern decision event used stale source time")
	}
	var inventory deploymentInventory
	if err := json.Unmarshal(tx.updated.Inventory, &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.Observation == nil || !inventory.Observation.ObservedAt.Equal(*d.Observation.ObservedAt) {
		t.Fatal("public inventory observation lost")
	}
	// Model the committed row and assert that source-only refresh preserves every
	// semantic field and triggers no decision effects, even if a rollout exists.
	tx.current = sqlc.ClusterDeployment{ID: deploymentID, DesiredGeneration: d.Generation, DesiredSpecDigest: d.SpecDigest, ObservedGeneration: d.Generation, ObservedSpecDigest: d.SpecDigest, Phase: "ready", Inventory: tx.updated.Inventory, Conditions: tx.updated.Conditions, LastObservedAt: tx.updated.LastObservedAt, ObservedRevision: d.ObservedRevision, SourceKind: d.SourceKind, SourceName: d.SourceName, ReconcilerKind: d.ReconcilerKind, ReconcilerName: d.ReconcilerName}
	tx.unchanged = true
	tx.updated = nil
	tx.event = nil
	tx.advance = nil
	tx.rolloutEvent = nil
	tx.outbox = nil
	tx.ack = nil
	ready := &fakeReadyReconciler{}
	ingester.SetReadyReconciler(ready)
	later := d.Observation.ObservedAt.Add(10 * time.Second)
	s.Deployments[0].Observation = &protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &later}
	s.SessionSequence++
	s.StatusDigest = s.SemanticDigest()
	if err := ingester.Ingest(context.Background(), clusterID, connectionID, "session", s); err != nil {
		t.Fatal(err)
	}
	if tx.updated == nil || !tx.updated.LastObservedAt.Time.Equal(later) || tx.updated.Phase != "ready" || tx.updated.ObservedRevision != tx.current.ObservedRevision {
		t.Fatal("coalesced source refresh missing or changed state")
	}
	if tx.event != nil || tx.advance != nil || tx.rolloutEvent != nil || tx.outbox != nil || tx.ack != nil || tx.finalized != [16]byte{} {
		t.Fatal("coalesced refresh replayed a decision effect")
	}
	if ready.clusterID != clusterID {
		t.Fatal("existing readiness retry removed")
	}
	tx.updated = nil
	tx.current.DesiredGeneration++
	s.SessionSequence++
	if err := ingester.Ingest(context.Background(), clusterID, connectionID, "session", s); err != nil {
		t.Fatal(err)
	}
	if tx.updated != nil {
		t.Fatal("coalesced refresh bypassed desired-generation fence")
	}
}

func TestDeploymentNeverObservedAndMutationSourceTimeNULL(t *testing.T) {
	for _, mutation := range []bool{false, true} {
		s := modernDeploymentReport()
		d := &s.Deployments[0]
		d.Conditions = nil
		d.Inventory = protocol.DeliveryInventory{}
		d.ObservedRevision = ""
		d.ObservedDigest = ""
		if mutation {
			d.Phase = "failed"
			d.ErrorCode = "local_apply_failed"
			d.Observation = nil
		} else {
			d.Phase = "unknown"
			d.ErrorCode = "observation_unsynced"
			d.Observation = &protocol.DeliveryObservation{State: protocol.ObservationUnsynced}
		}
		s.StatusDigest = s.SemanticDigest()
		tx := &fakeTransaction{current: sqlc.ClusterDeployment{DesiredGeneration: d.Generation, DesiredSpecDigest: d.SpecDigest}}
		if err := NewIngester(&fakeRunner{tx: tx}).Ingest(context.Background(), clusterID, connectionID, "session", s); err != nil {
			t.Fatal(err)
		}
		if tx.updated == nil || tx.updated.LastObservedAt.Valid {
			t.Fatal("assessment became source freshness")
		}
	}
}
