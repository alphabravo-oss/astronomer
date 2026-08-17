package tunnel

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type fakeDeliveryProvider struct {
	cluster uuid.UUID
	request protocol.DeliveryStateRequestV2
}

func (f *fakeDeliveryProvider) Snapshot(_ context.Context, cluster uuid.UUID, request protocol.DeliveryStateRequestV2) (protocol.DeliveryStateResponseV2, error) {
	f.cluster, f.request = cluster, request
	response := protocol.DeliveryStateResponseV2{
		ProtocolVersion: protocol.DeliveryProtocolVersion, SnapshotGeneration: 1,
		FullSnapshot: true, CredentialEpoch: 1,
	}
	response.ETag, _ = response.CanonicalETag()
	return response, nil
}

type fakeDeliveryStatusSink struct {
	cluster, connection uuid.UUID
	session             string
	payload             protocol.DeliveryStatusV2
}

func (f *fakeDeliveryStatusSink) Ingest(_ context.Context, cluster, connection uuid.UUID, session string, payload protocol.DeliveryStatusV2) error {
	f.cluster, f.connection, f.session, f.payload = cluster, connection, session, payload
	return nil
}

func TestDeliveryStateRequestUsesAuthenticatedConnectionIdentity(t *testing.T) {
	clusterID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	hub := NewHub(slog.Default())
	provider := &fakeDeliveryProvider{}
	hub.SetDeliveryStateProvider(provider)
	connection := &AgentConnection{ClusterID: clusterID.String(), sendCh: make(chan *protocol.Message, 1)}
	hub.agents.Set(connection.ClusterID, connection)
	t.Cleanup(func() { hub.agents.Delete(connection.ClusterID) })
	payload, _ := json.Marshal(protocol.DeliveryStateRequestV2{
		ClusterID: clusterID.String(), ProtocolVersion: protocol.DeliveryProtocolVersion,
	})
	hub.handleMessage(connection, &protocol.Message{
		Type: protocol.MsgDeliveryStateRequest, StreamID: "stream", RequestID: "request", Payload: payload,
	})
	select {
	case response := <-connection.sendCh:
		if response.Type != protocol.MsgDeliveryStateResponse || response.Error != "" || response.RequestID != "request" {
			t.Fatalf("unexpected response: %#v", response)
		}
		if provider.cluster != clusterID || provider.request.ClusterID != clusterID.String() {
			t.Fatalf("provider identity = %s/%s", provider.cluster, provider.request.ClusterID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivery response")
	}
}

func TestDeliveryStateRequestRejectsUnknownFields(t *testing.T) {
	clusterID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	hub := NewHub(slog.Default())
	provider := &fakeDeliveryProvider{}
	hub.SetDeliveryStateProvider(provider)
	connection := &AgentConnection{ClusterID: clusterID.String(), sendCh: make(chan *protocol.Message, 1)}
	hub.agents.Set(connection.ClusterID, connection)
	t.Cleanup(func() { hub.agents.Delete(connection.ClusterID) })
	payload := []byte(`{"cluster_id":"` + clusterID.String() + `","protocol_version":"2.0","unexpected":true}`)
	hub.handleMessage(connection, &protocol.Message{Type: protocol.MsgDeliveryStateRequest, Payload: payload})
	response := <-connection.sendCh
	if response.Error != "invalid_delivery_state_request" || provider.cluster != uuid.Nil {
		t.Fatalf("unknown field was not rejected: response=%#v provider=%s", response, provider.cluster)
	}
}

func TestDeliveryStatusPassesDatabaseSessionFence(t *testing.T) {
	clusterID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	connectionID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	hub := NewHub(slog.Default())
	sink := &fakeDeliveryStatusSink{}
	hub.SetDeliveryStatusSink(sink)
	connection := &AgentConnection{ClusterID: clusterID.String(), DBID: connectionID, SessionID: "session-1"}
	payload, _ := json.Marshal(protocol.DeliveryStatusV2{
		ProtocolVersion: protocol.DeliveryProtocolVersion, ClusterID: clusterID.String(), SessionSequence: 1,
	})
	hub.handleMessage(connection, &protocol.Message{Type: protocol.MsgDeliveryStatus, Payload: payload})
	if sink.cluster != clusterID || sink.connection != connectionID || sink.session != "session-1" || sink.payload.SessionSequence != 1 {
		t.Fatalf("status sink identity was not bound: %#v", sink)
	}
}

func TestDeliveryStateProviderErrorIsSanitized(t *testing.T) {
	hub := NewHub(slog.Default())
	clusterID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	connection := &AgentConnection{ClusterID: clusterID.String(), sendCh: make(chan *protocol.Message, 1)}
	hub.agents.Set(connection.ClusterID, connection)
	t.Cleanup(func() { hub.agents.Delete(connection.ClusterID) })
	payload := []byte(`{"cluster_id":"` + clusterID.String() + `","protocol_version":"2.0"}`)
	hub.handleMessage(connection, &protocol.Message{Type: protocol.MsgDeliveryStateRequest, Payload: payload})
	response := <-connection.sendCh
	if response.Error != "delivery_state_unavailable" || strings.Contains(response.Error, "provider") {
		t.Fatalf("error was not sanitized: %#v", response)
	}
}
