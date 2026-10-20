package tunnel

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func agentCountBoundaryHub(t *testing.T) (*Hub, *AgentConnection, *recordingValidator, *recordingPublisher) {
	t.Helper()
	validator := &recordingValidator{}
	hub := NewHubWithValidator(slog.New(slog.NewTextHandler(io.Discard, nil)), validator)
	publisher := &recordingPublisher{}
	hub.SetPublisher(publisher)
	conn := &AgentConnection{ClusterID: uuid.NewString(), DBID: uuid.New(), SessionID: uuid.NewString(), sendCh: make(chan *protocol.Message, 1)}
	return hub, conn, validator, publisher
}

func TestHeartbeatInventoryCountsRejectBeforePersistenceOrFanout(t *testing.T) {
	for _, item := range []struct {
		name        string
		nodes, pods int64
	}{
		{"node-overflow", int64(math.MaxInt32) + 1, 1}, {"pod-overflow", 1, int64(math.MaxInt32) + 1},
		{"node-negative", -1, 1}, {"pod-negative", 1, -1},
	} {
		t.Run(item.name, func(t *testing.T) {
			hub, conn, validator, publisher := agentCountBoundaryHub(t)
			raw, err := json.Marshal(map[string]any{"node_count": item.nodes, "pod_count": item.pods, "agent_version": "v1.2.3"})
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) > 256 {
				t.Fatal("fixture must be a small wire payload")
			}
			hub.handleMessage(conn, &protocol.Message{Type: protocol.MsgHeartbeat, Payload: raw})
			if len(validator.SnapshotHeartbeatArgs()) != 0 || len(validator.SnapshotUpserts()) != 0 || len(validator.SnapshotTouchedMetricsIDs()) != 0 {
				t.Error("invalid counts reached a durable operation")
			}
			if len(publisher.Snapshot()) != 0 || len(conn.sendCh) != 0 {
				t.Error("invalid counts reached event or command fanout")
			}
		})
	}
}

func TestMetricsInventoryCountsRejectBeforePersistenceOrFanout(t *testing.T) {
	for _, item := range []struct {
		name        string
		nodes, pods int64
	}{
		{"node-overflow", int64(math.MaxInt32) + 1, 1}, {"pod-overflow", 1, int64(math.MaxInt32) + 1},
		{"node-negative", -1, 1}, {"pod-negative", 1, -1},
	} {
		t.Run(item.name, func(t *testing.T) {
			hub, conn, validator, publisher := agentCountBoundaryHub(t)
			raw, err := json.Marshal(map[string]any{"cluster_node_count": item.nodes, "cluster_pod_count": item.pods, "metrics_available": true})
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) > 256 {
				t.Fatal("fixture must be a small wire payload")
			}
			hub.handleMessage(conn, &protocol.Message{Type: protocol.MsgMetrics, Payload: raw})
			if len(validator.SnapshotUpserts()) != 0 || len(validator.SnapshotTouchedMetricsIDs()) != 0 || len(validator.SnapshotHeartbeatArgs()) != 0 {
				t.Error("invalid counts reached a durable operation")
			}
			if len(publisher.Snapshot()) != 0 || len(conn.sendCh) != 0 {
				t.Error("invalid counts reached event or command fanout")
			}
		})
	}
}

func TestAgentInventoryCountsPreserveZeroNormalAndMaximumWireValues(t *testing.T) {
	for _, item := range []struct {
		name        string
		nodes, pods int32
	}{
		{"zero", 0, 0}, {"normal", 3, 27}, {"maximum", math.MaxInt32, math.MaxInt32},
	} {
		t.Run(item.name, func(t *testing.T) {
			hub, conn, validator, publisher := agentCountBoundaryHub(t)
			raw, err := json.Marshal(protocol.HeartbeatPayload{NodeCount: int(item.nodes), PodCount: int(item.pods), DegradedReasons: []string{"fixture inventory unavailable"}})
			if err != nil {
				t.Fatal(err)
			}
			hub.handleMessage(conn, &protocol.Message{Type: protocol.MsgHeartbeat, Payload: raw})
			heartbeat := validator.SnapshotHeartbeatArgs()
			if len(heartbeat) != 1 || heartbeat[0].NodeCount != item.nodes || heartbeat[0].PodCount != item.pods {
				t.Fatal("valid heartbeat count projection changed")
			}
			if len(publisher.Snapshot()) != 1 {
				t.Fatal("valid heartbeat fanout was lost")
			}
			raw, err = json.Marshal(protocol.MetricsPayload{ClusterNodeCount: int(item.nodes), ClusterPodCount: int(item.pods), MetricsAvailable: true})
			if err != nil {
				t.Fatal(err)
			}
			hub.handleMessage(conn, &protocol.Message{Type: protocol.MsgMetrics, Payload: raw})
			metrics := validator.SnapshotUpserts()
			if len(metrics) != 1 || metrics[0].NodeCount != item.nodes || metrics[0].PodCount != item.pods {
				t.Fatal("valid metrics count projection changed")
			}
			if len(validator.SnapshotTouchedMetricsIDs()) != 1 || len(publisher.Snapshot()) != 2 {
				t.Fatal("valid metrics sample or fanout was lost")
			}
		})
	}
}
