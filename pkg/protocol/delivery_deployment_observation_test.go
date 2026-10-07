package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func observedDeploymentStatus() DeliveryStatusV2 {
	now := time.Now().UTC()
	return DeliveryStatusV2{ProtocolVersion: DeliveryProtocolVersion, ClusterID: "11111111-1111-4111-8111-111111111111", SessionSequence: 1,
		ControllerInventory: DeliveryControllerInventory{Observation: &DeliveryObservation{State: ObservationCurrent, ObservedAt: &now}},
		Deployments:         []DeliveryDeploymentStatusV2{{DeploymentID: "22222222-2222-4222-8222-222222222222", Generation: 1, SpecDigest: "sha256:" + strings.Repeat("a", 64), Phase: "ready", ObservedAt: now, Observation: &DeliveryObservation{State: ObservationCurrent, ObservedAt: &now}}}}
}
func TestDeploymentObservationValidationAndMutationException(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*DeliveryStatusV2)
		valid  bool
	}{
		{"current", func(*DeliveryStatusV2) {}, true},
		{"legacy", func(s *DeliveryStatusV2) { s.ControllerInventory.Observation = nil; s.Deployments[0].Observation = nil }, true},
		{"missing-marker", func(s *DeliveryStatusV2) { s.ControllerInventory.Observation = nil }, false},
		{"modern-missing-source", func(s *DeliveryStatusV2) { s.Deployments[0].Observation = nil }, false},
		{"never-observed", func(s *DeliveryStatusV2) {
			d := &s.Deployments[0]
			d.Observation = &DeliveryObservation{State: ObservationUnsynced}
			d.Phase = "unknown"
			d.ErrorCode = "observation_unsynced"
		}, true},
		{"false-ready", func(s *DeliveryStatusV2) { s.Deployments[0].Observation.State = ObservationStale }, false},
		{"stale-current", func(s *DeliveryStatusV2) {
			at := time.Now().Add(-6 * time.Minute)
			s.Deployments[0].Observation = &DeliveryObservation{State: ObservationCurrent, ObservedAt: &at}
		}, false},
		{"future", func(s *DeliveryStatusV2) {
			at := time.Now().Add(time.Hour)
			s.Deployments[0].Observation = &DeliveryObservation{State: ObservationCurrent, ObservedAt: &at}
		}, false},
		{"absent-without-time", func(s *DeliveryStatusV2) {
			d := &s.Deployments[0]
			d.Phase = "unknown"
			d.ErrorCode = "observation_absent"
			d.Observation = &DeliveryObservation{State: ObservationAbsent}
		}, false},
		{"removed-decision", func(s *DeliveryStatusV2) { s.Deployments[0].Observation = nil; s.Deployments[0].Phase = "removed" }, true},
		{"apply-failure-decision", func(s *DeliveryStatusV2) {
			d := &s.Deployments[0]
			d.Observation = nil
			d.Phase = "failed"
			d.ErrorCode = "local_apply_failed"
		}, true},
		{"decision-cached-health", func(s *DeliveryStatusV2) {
			d := &s.Deployments[0]
			d.Observation = nil
			d.Phase = "removed"
			d.Inventory.Ready = 1
			d.Inventory.Entries = 1
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := observedDeploymentStatus()
			tc.change(&s)
			s.StatusDigest = s.SemanticDigest()
			if err := s.Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
func TestDeploymentObservationDigestAndStrictLegacy(t *testing.T) {
	s := observedDeploymentStatus()
	before, _ := json.Marshal(s)
	digest := s.SemanticDigest()
	after, _ := json.Marshal(s)
	if !bytes.Equal(before, after) {
		t.Fatal("digest mutated source")
	}
	changed := s
	changed.Deployments = append([]DeliveryDeploymentStatusV2(nil), s.Deployments...)
	later := s.Deployments[0].Observation.ObservedAt.Add(time.Second)
	changed.Deployments[0].Observation = &DeliveryObservation{State: ObservationCurrent, ObservedAt: &later}
	if changed.SemanticDigest() != digest {
		t.Fatal("source time changed semantic digest")
	}
	changed.Deployments[0].Observation = &DeliveryObservation{State: ObservationStale, ObservedAt: &later}
	if changed.SemanticDigest() == digest {
		t.Fatal("state transition omitted from digest")
	}
	// Frozen legacy decoder of the mandatory deployment fields in this fixture.
	var legacy struct {
		DeploymentID string            `json:"deployment_id"`
		Generation   int64             `json:"generation"`
		SpecDigest   string            `json:"spec_digest"`
		Phase        string            `json:"phase"`
		Inventory    DeliveryInventory `json:"inventory"`
		ObservedAt   time.Time         `json:"observed_at"`
	}
	raw, _ := json.Marshal(s.Deployments[0])
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err == nil || !strings.Contains(err.Error(), "observation") {
		t.Fatalf("strict legacy accepted extension: %v", err)
	}
	s.Deployments[0].Observation = nil
	raw, _ = json.Marshal(s.Deployments[0])
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err != nil {
		t.Fatalf("legacy omission failed: %v", err)
	}
}
