package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Freeze the previous inventory shape: using today's protocol struct would
// silently accept the very extension this mixed-version test must reject.
type legacyObservationInventory struct {
	AgentVersion     string            `json:"agent_version,omitempty"`
	FluxVersion      string            `json:"flux_version,omitempty"`
	Components       map[string]string `json:"components,omitempty"`
	SystemComponents []struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		Category         string `json:"category"`
		Owner            string `json:"owner"`
		ManagementMethod string `json:"management_method"`
		Kind             string `json:"kind"`
		Health           string `json:"health"`
		HighAvailability bool   `json:"high_availability"`
	} `json:"system_components,omitempty"`
	APIVersions          []string `json:"api_versions,omitempty"`
	KubernetesVersion    string   `json:"kubernetes_version,omitempty"`
	DistributionDigest   string   `json:"distribution_digest,omitempty"`
	Ready                bool     `json:"ready"`
	CompatibilityMessage string   `json:"compatibility_message,omitempty"`
}

func TestRuntimeLegacyDeliveryStrictDecodeAndNegotiatedFields(t *testing.T) {
	runtime, _ := newRuntimeFixture(t)
	probe := runtime.probe.(staticCapabilityProbe)
	probe.inventory.SystemComponents = []protocol.SystemComponent{{ID: "app", Name: "app", Kind: "Deployment", Health: "healthy"}}
	runtime.probe = probe
	enabled := false
	runtime.config.ObservationFreshness = func() bool { return enabled }
	for _, negotiated := range []bool{false, true, false} {
		enabled = negotiated
		inventory, _, err := runtime.inspectInventory(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []protocol.MessageType{protocol.MsgDeliveryStateRequest, protocol.MsgDeliveryStatus} {
			var payload any
			var legacy any
			if kind == protocol.MsgDeliveryStateRequest {
				payload = protocol.DeliveryStateRequestV2{ProtocolVersion: protocol.DeliveryProtocolVersion, ControllerInventory: inventory}
				legacy = &struct {
					ProtocolVersion         string                     `json:"protocol_version"`
					ClusterID               string                     `json:"cluster_id"`
					AckedSnapshotGeneration int64                      `json:"acked_snapshot_generation"`
					AckedETag               string                     `json:"acked_etag,omitempty"`
					ControllerInventory     legacyObservationInventory `json:"controller_inventory"`
				}{}
			} else {
				payload = protocol.DeliveryStatusV2{ProtocolVersion: protocol.DeliveryProtocolVersion, ControllerInventory: inventory}
				legacy = &struct {
					ProtocolVersion     string                                `json:"protocol_version"`
					ClusterID           string                                `json:"cluster_id"`
					SessionSequence     int64                                 `json:"session_sequence"`
					SnapshotGeneration  int64                                 `json:"snapshot_generation"`
					SnapshotETag        string                                `json:"snapshot_etag,omitempty"`
					StatusDigest        string                                `json:"status_digest"`
					ControllerInventory legacyObservationInventory            `json:"controller_inventory"`
					Deployments         []protocol.DeliveryDeploymentStatusV2 `json:"deployments,omitempty"`
				}{}
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			err = decoder.Decode(legacy)
			if negotiated == (err == nil) {
				t.Fatalf("%s negotiated=%v legacy decode=%v", kind, negotiated, err)
			}
			if !negotiated && bytes.Contains(body, []byte(`"observation"`)) {
				t.Fatal("legacy message emitted optional observation fields")
			}
		}
	}
	if probe.inventory.Observation != nil || probe.inventory.SystemComponents[0].Observation != nil {
		t.Fatal("emitter mutated probe snapshot")
	}
}

func TestRuntimePreservesExistingObservationAndRefusesLegacyCache(t *testing.T) {
	runtime, _ := newRuntimeFixture(t)
	sourceTime := time.Now().UTC().Add(-time.Minute)
	probe := runtime.probe.(staticCapabilityProbe)
	probe.inventory.Observation = &protocol.DeliveryObservation{State: protocol.ObservationStale, ObservedAt: &sourceTime}
	probe.inventory.Ready = false
	runtime.probe = probe
	runtime.config.ObservationFreshness = func() bool { return true }
	inventory, _, err := runtime.inspectInventory(context.Background())
	if err != nil || !inventory.Observation.ObservedAt.Equal(sourceTime) {
		t.Fatal("cached source observation was restamped")
	}
	runtime.config.ObservationFreshness = func() bool { return false }
	if _, _, err := runtime.inspectInventory(context.Background()); err == nil {
		t.Fatal("cached snapshot silently downgraded to direct legacy observation")
	}
}

func TestRuntimeFailedSourceReadCannotBecomeCurrent(t *testing.T) {
	runtime, _ := newRuntimeFixture(t)
	probe := runtime.probe.(staticCapabilityProbe)
	probe.inventory.CompatibilityMessage = "controller_inventory_unavailable"
	runtime.probe = probe
	runtime.config.ObservationFreshness = func() bool { return true }
	inventory, _, err := runtime.inspectInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Ready || inventory.Observation.State != protocol.ObservationUnavailable || inventory.Observation.ObservedAt != nil {
		t.Fatalf("failed read fabricated freshness: %#v", inventory)
	}
}

func TestRuntimeRetriesSupersededObservationInDirectLegacyMode(t *testing.T) {
	runtime, _ := newRuntimeFixture(t)
	runtime.lastStatusDigest = "unchanged"
	runtime.lastStatusSentAt = time.Now()
	runtime.RetryObservation(&protocol.Message{Type: protocol.MsgDeliveryStateRequest, RequestID: "old-session"})
	select {
	case reply := <-runtime.replies:
		if reply.requestID != "old-session" || reply.errorCode != "observation_contract_changed" {
			t.Fatal("waiting request was not released")
		}
	default:
		t.Fatal("request remains blocked for response timeout")
	}
	select {
	case <-runtime.wake:
	default:
		t.Fatal("runtime was not woken for direct observation")
	}
	runtime.resetObservationSuppression()
	if runtime.lastStatusDigest != "" || !runtime.lastStatusSentAt.IsZero() {
		t.Fatal("unchanged status would suppress recovery")
	}
	inventory, _, err := runtime.inspectInventory(context.Background())
	if err != nil || inventory.Observation != nil {
		t.Fatal("retry did not use direct legacy observation")
	}
}
