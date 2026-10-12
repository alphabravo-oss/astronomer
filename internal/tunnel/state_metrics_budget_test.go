package tunnel

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestTunnelStateMetricsBoundsKindWithoutChangingEvents(t *testing.T) {
	previous := observability.InstanceID()
	observability.SetInstanceID(t.Name())
	t.Cleanup(func() { observability.SetInstanceID(previous) })
	hub := NewHub(slog.Default())
	pub := &recordingPublisher{}
	hub.SetPublisher(pub)
	kinds := []string{"SyntheticCustomOne", "SyntheticCustomTwo", "SyntheticCustomThree"}
	for _, kind := range kinds {
		body, err := json.Marshal(protocol.StateUpdatePayload{Op: protocol.StateUpdateOpAdded, Kind: kind, Namespace: "synthetic", Name: "synthetic"})
		if err != nil {
			t.Fatal(err)
		}
		hub.handleStateUpdate(&AgentConnection{ClusterID: "synthetic-current-cluster"}, &protocol.Message{Type: protocol.MsgStateUpdate, Payload: body})
	}
	if len(pub.events) != len(kinds) {
		t.Fatalf("published events=%d want%d", len(pub.events), len(kinds))
	}
	for i, event := range pub.events {
		if event.Data.(map[string]any)["kind"] != kinds[i] {
			t.Fatal("metrics changed published resource kind")
		}
	}
	if got := counterValue(t, tunnelStateUpdatesReceivedTotal.WithLabelValues(observability.MetricValues("other")...)); got != 3 {
		t.Fatalf("finite other-kind metric=%v want3", got)
	}
}
