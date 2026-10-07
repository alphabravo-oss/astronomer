package agent

import (
	"encoding/json"
	"slices"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func (tc *TunnelClient) setObservationCapabilities(ack protocol.ConnectAckPayload) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.observationFreshness = ack.Accepted && slices.Contains(ack.Capabilities, protocol.FeatureDeliveryObservation)
}

func (tc *TunnelClient) ObservationFreshnessEnabled() bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.connected && tc.observationFreshness
}

func (tc *TunnelClient) SetObservationRetry(retry func(*protocol.Message)) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.onObservationRetry = retry
}

// Only observation snapshots/requests can be superseded by a peer contract
// change. Do not rewrite cached observations into apparently direct legacy
// observations, and never discard mutation or audit frames here.
func (tc *TunnelClient) dropUnnegotiatedObservation(msg *protocol.Message) bool {
	if tc.ObservationFreshnessEnabled() || (msg.Type != protocol.MsgDeliveryStateRequest && msg.Type != protocol.MsgDeliveryStatus) {
		return false
	}
	var envelope struct {
		Deployments []struct {
			Observation json.RawMessage `json:"observation"`
		} `json:"deployments"`
		Inventory struct {
			Observation json.RawMessage `json:"observation"`
			Components  []struct {
				Observation json.RawMessage `json:"observation"`
			} `json:"system_components"`
		} `json:"controller_inventory"`
	}
	if json.Unmarshal(msg.Payload, &envelope) != nil {
		return false
	}
	extended := len(envelope.Inventory.Observation) != 0
	for _, deployment := range envelope.Deployments {
		extended = extended || len(deployment.Observation) != 0
	}
	for _, component := range envelope.Inventory.Components {
		extended = extended || len(component.Observation) != 0
	}
	if !extended {
		return false
	}
	agentTunnelSendDroppedTotal.WithLabelValues(observability.MetricValues(string(frameControl), "observation_contract_changed")...).Inc()
	tc.mu.RLock()
	retry := tc.onObservationRetry
	tc.mu.RUnlock()
	if retry != nil {
		retry(msg)
	}
	return true
}
func (tc *TunnelClient) setConnected(v bool) {
	tc.mu.Lock()
	tc.connected = v
	if !v {
		tc.observationFreshness = false
	}
	listener := tc.onConnChange
	tc.mu.Unlock()
	// M4: notify the readiness reporter on EVERY transition (connect AND
	// disconnect) so /readyz reflects the live tunnel state instead of a flag
	// that was latched true on first connect and never reset on drop.
	if listener != nil {
		listener(v)
	}
}
