package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestObservationNegotiationResetsAcrossSessions(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())
	if !slices.Contains(connectCapabilities("viewer"), protocol.FeatureDeliveryObservation) {
		t.Fatal("CONNECT did not advertise optional observation support")
	}
	tc.setObservationCapabilities(protocol.ConnectAckPayload{Accepted: true, Capabilities: []string{protocol.FeatureDeliveryObservation}})
	tc.setConnected(true)
	if !tc.ObservationFreshnessEnabled() {
		t.Fatal("supported ACK did not enable observation contract")
	}
	tc.setConnected(false)
	if tc.ObservationFreshnessEnabled() {
		t.Fatal("disconnect retained feature support")
	}
	var oldAck protocol.ConnectAckPayload
	if err := json.Unmarshal([]byte(`{"accepted":true}`), &oldAck); err != nil {
		t.Fatal(err)
	}
	tc.setObservationCapabilities(oldAck)
	tc.setConnected(true)
	if tc.ObservationFreshnessEnabled() {
		t.Fatal("old server inherited previous support")
	}
	tc.setObservationCapabilities(protocol.ConnectAckPayload{Accepted: false, Capabilities: []string{protocol.FeatureDeliveryObservation}})
	if tc.ObservationFreshnessEnabled() {
		t.Fatal("rejected handshake enabled support")
	}
	// dial resets negotiation before any TLS validation/network attempt.
	tc.setObservationCapabilities(protocol.ConnectAckPayload{Accepted: true, Capabilities: []string{protocol.FeatureDeliveryObservation}})
	tc.config.CACert = "invalid CA fixture"
	if err := tc.dial(context.Background()); err == nil {
		t.Fatal("invalid TLS setup unexpectedly succeeded")
	}
	if tc.ObservationFreshnessEnabled() {
		t.Fatal("failed handshake retained previous support")
	}
}

func TestQueuedExtendedObservationsAreRetriedAfterLegacyReconnect(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())
	tc.setObservationCapabilities(protocol.ConnectAckPayload{Accepted: true, Capabilities: []string{protocol.FeatureDeliveryObservation}})
	tc.setConnected(true)
	var retries []*protocol.Message
	tc.SetObservationRetry(func(msg *protocol.Message) { retries = append(retries, msg) })
	for _, kind := range []protocol.MessageType{protocol.MsgDeliveryStateRequest, protocol.MsgDeliveryStatus} {
		message := &protocol.Message{Type: kind, RequestID: string(kind), Payload: []byte(`{"controller_inventory":{"observation":{"state":"unsynced"}}}`)}
		if err := tc.Send(message); err != nil {
			t.Fatal(err)
		}
	}
	audit := &protocol.Message{Type: protocol.MsgApiserverAudit, Payload: []byte(`{"fixture":true}`)}
	if err := tc.Send(audit); err != nil {
		t.Fatal(err)
	}
	tc.setConnected(false)
	tc.setObservationCapabilities(protocol.ConnectAckPayload{Accepted: true})
	tc.setConnected(true)
	before := sendDrops(t, string(frameControl), "observation_contract_changed")
	for n := 0; n < 2; n++ {
		// A nil WebSocket proves the final queued-write gate returns before trying
		// to send obsolete fields to the strict legacy peer.
		if !tc.writeQueued(context.Background(), <-tc.controlCh) {
			t.Fatal("obsolete observation closed new connection")
		}
	}
	if len(retries) != 2 || sendDrops(t, string(frameControl), "observation_contract_changed") != before+2 {
		t.Fatal("dropped observations were not accounted/retried")
	}
	if got := <-tc.controlCh; got != audit || tc.dropUnnegotiatedObservation(got) {
		t.Fatal("audit frame was discarded or rewritten")
	}
	for _, kind := range []protocol.MessageType{protocol.MsgHelmResult, protocol.MsgK8sResponse, protocol.MsgDecommissionAck} {
		if tc.dropUnnegotiatedObservation(&protocol.Message{Type: kind, Payload: retries[0].Payload}) {
			t.Fatalf("mutation response %s discarded", kind)
		}
	}
	legacy := &protocol.Message{Type: protocol.MsgDeliveryStatus, Payload: []byte(`{"controller_inventory":{"ready":false}}`)}
	if tc.dropUnnegotiatedObservation(legacy) {
		t.Fatal("direct legacy observation discarded")
	}
}
