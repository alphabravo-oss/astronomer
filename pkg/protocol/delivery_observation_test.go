package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestDeliveryObservationValidation(t *testing.T) {
	now := time.Now().UTC()
	old, future, zero := now.Add(-MaxCurrentObservationAge-time.Minute), now.Add(MaxObservationClockSkew+time.Minute), time.Time{}
	for _, tc := range []struct {
		name           string
		state          ObservationState
		at             *time.Time
		healthy, valid bool
	}{
		{"current", ObservationCurrent, &now, true, true},
		{"current missing time", ObservationCurrent, nil, false, false},
		{"current old time", ObservationCurrent, &old, false, false},
		{"future", ObservationCurrent, &future, false, false},
		{"zero", ObservationCurrent, &zero, false, false},
		{"stale", ObservationStale, &old, false, true},
		{"stale healthy", ObservationStale, &old, true, false},
		{"stale missing time", ObservationStale, nil, false, false},
		{"absent", ObservationAbsent, &now, false, true},
		{"absent missing time", ObservationAbsent, nil, false, false},
		{"unsynced", ObservationUnsynced, nil, false, true},
		{"denied", ObservationDenied, nil, false, true},
		{"disconnected", ObservationDisconnected, &old, false, true},
		{"unavailable", ObservationUnavailable, nil, false, true},
		{"unavailable ready", ObservationUnavailable, nil, true, false},
		{"unknown state", "invented", &now, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory := DeliveryControllerInventory{Observation: &DeliveryObservation{State: tc.state, ObservedAt: tc.at}, Ready: tc.healthy}
			if err := inventory.Validate(); (err == nil) != tc.valid {
				t.Fatalf("validation = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func TestDeliveryObservationComponentValidation(t *testing.T) {
	now := time.Now().UTC()
	i := DeliveryControllerInventory{Observation: &DeliveryObservation{State: ObservationCurrent, ObservedAt: &now}, SystemComponents: []SystemComponent{{ID: "app", Name: "app", Health: "healthy"}}}
	if err := i.Validate(); err == nil {
		t.Fatal("accepted unqualified component in negotiated inventory")
	}
	i.SystemComponents[0].Observation = &DeliveryObservation{State: ObservationStale, ObservedAt: &now}
	if err := i.Validate(); err == nil {
		t.Fatal("accepted healthy stale component")
	}
	i.SystemComponents[0].Health = "unknown"
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	i.Observation = nil
	if err := i.Validate(); err == nil {
		t.Fatal("accepted component extension without inventory contract")
	}
}

func TestDeliveryObservationDigestExcludesTimesPreservesStates(t *testing.T) {
	now, later := time.Now().UTC(), time.Now().UTC().Add(time.Second)
	observation := &DeliveryObservation{State: ObservationCurrent, ObservedAt: &now}
	status := DeliveryStatusV2{ControllerInventory: DeliveryControllerInventory{Observation: observation, SystemComponents: []SystemComponent{{Observation: observation, ID: "app", Name: "app"}}}}
	before, _ := json.Marshal(status)
	first := status.SemanticDigest()
	after, _ := json.Marshal(status)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("digest mutated shared observation objects")
	}
	changed := status
	changed.ControllerInventory.Observation = &DeliveryObservation{State: ObservationCurrent, ObservedAt: &later}
	changed.ControllerInventory.SystemComponents = []SystemComponent{{Observation: changed.ControllerInventory.Observation, ID: "app", Name: "app"}}
	if changed.SemanticDigest() != first {
		t.Fatal("source timestamps defeated semantic coalescing")
	}
	changed.ControllerInventory.Observation = &DeliveryObservation{State: ObservationStale, ObservedAt: &now}
	if changed.SemanticDigest() == first {
		t.Fatal("freshness state transition was suppressed")
	}
}

func TestObservationCapabilitiesIntersectionAndLegacyAck(t *testing.T) {
	if got := NegotiatedCapabilities([]string{"unknown"}); len(got) != 0 {
		t.Fatal(got)
	}
	want := []string{FeatureDeliveryObservation}
	if got := NegotiatedCapabilities([]string{"unknown", FeatureDeliveryObservation, FeatureDeliveryObservation}); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	body, err := json.Marshal(ConnectAckPayload{Accepted: true, Capabilities: want})
	if err != nil {
		t.Fatal(err)
	}
	// Old agents decode ACKs with json.Unmarshal, intentionally accepting new fields.
	var legacy struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(body, &legacy); err != nil || !legacy.Accepted {
		t.Fatalf("old agent rejected new ACK: %v", err)
	}
	var modern ConnectAckPayload
	if err := json.Unmarshal([]byte(`{"accepted":true}`), &modern); err != nil || len(modern.Capabilities) != 0 {
		t.Fatalf("old ACK enabled extension: %v", err)
	}
}
