package tunnel

import (
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// recordingPublisher captures every Publish call so tests can assert what
// the hub fanned out onto the SSE bus.
type recordingPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	Type string
	Data any
}

func (r *recordingPublisher) Publish(eventType string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{Type: eventType, Data: data})
}

func (r *recordingPublisher) Snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

// TestHandleStateUpdatePublishesK8sChanged verifies a STATE_UPDATE from the
// agent becomes a `cluster.k8s_changed` SSE event with the cluster_id
// stitched in.
func TestHandleStateUpdatePublishesK8sChanged(t *testing.T) {
	h := NewHub(slog.Default())
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	conn := &AgentConnection{
		ClusterID: "cluster-xyz",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	body, _ := json.Marshal(protocol.StateUpdatePayload{
		Op:              protocol.StateUpdateOpAdded,
		Kind:            "Pod",
		APIVersion:      "v1",
		Namespace:       "default",
		Name:            "echo",
		ResourceVersion: "42",
	})
	msg := &protocol.Message{Type: protocol.MsgStateUpdate, Payload: body}

	h.handleMessage(conn, msg)

	events := pub.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 publish, got %d", len(events))
	}
	if events[0].Type != "cluster.k8s_changed" {
		t.Fatalf("expected cluster.k8s_changed, got %s", events[0].Type)
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any payload, got %T", events[0].Data)
	}
	if data["cluster_id"] != "cluster-xyz" {
		t.Errorf("expected cluster_id=cluster-xyz, got %v", data["cluster_id"])
	}
	if data["kind"] != "Pod" {
		t.Errorf("expected kind=Pod, got %v", data["kind"])
	}
	if data["op"] != "added" {
		t.Errorf("expected op=added, got %v", data["op"])
	}
	if data["namespace"] != "default" {
		t.Errorf("expected namespace=default, got %v", data["namespace"])
	}
	if data["name"] != "echo" {
		t.Errorf("expected name=echo, got %v", data["name"])
	}
}

// TestHandleStateUpdateRateLimited verifies the server-side limiter collapses
// bursts on the same (cluster, kind, namespace) tuple. Without this, a
// thousand pod updates inside a Deployment rollout would each fan out.
func TestHandleStateUpdateRateLimited(t *testing.T) {
	h := NewHub(slog.Default())
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	conn := &AgentConnection{
		ClusterID: "cluster-rate",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	for i := 0; i < 10; i++ {
		body, _ := json.Marshal(protocol.StateUpdatePayload{
			Op:        protocol.StateUpdateOpModified,
			Kind:      "Pod",
			Namespace: "default",
			Name:      "echo",
		})
		h.handleMessage(conn, &protocol.Message{Type: protocol.MsgStateUpdate, Payload: body})
	}

	events := pub.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected the limiter to collapse the burst to 1 event, got %d", len(events))
	}
}

// TestHandleStateUpdateDistinctKeysPassThrough confirms that distinct
// (cluster, kind, namespace) tuples don't share a budget.
func TestHandleStateUpdateDistinctKeysPassThrough(t *testing.T) {
	h := NewHub(slog.Default())
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	conn := &AgentConnection{
		ClusterID: "cluster-keys",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	cases := []protocol.StateUpdatePayload{
		{Op: protocol.StateUpdateOpAdded, Kind: "Pod", Namespace: "ns-a", Name: "a"},
		{Op: protocol.StateUpdateOpAdded, Kind: "Pod", Namespace: "ns-b", Name: "a"},
		{Op: protocol.StateUpdateOpAdded, Kind: "Service", Namespace: "ns-a", Name: "a"},
		{Op: protocol.StateUpdateOpAdded, Kind: "Deployment", Namespace: "ns-a", Name: "a"},
	}
	for _, c := range cases {
		body, _ := json.Marshal(c)
		h.handleMessage(conn, &protocol.Message{Type: protocol.MsgStateUpdate, Payload: body})
	}

	events := pub.Snapshot()
	if len(events) != len(cases) {
		t.Fatalf("expected %d distinct fan-outs, got %d", len(cases), len(events))
	}
}

// TestStateUpdateLimiterAllow exercises the limiter's tick gating directly.
// Independent of the hub so a regression here doesn't depend on the
// publisher plumbing.
func TestStateUpdateLimiterAllow(t *testing.T) {
	r := newStateUpdateLimiter(500 * time.Millisecond)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }

	if !r.allow("k") {
		t.Fatal("first allow must pass")
	}
	if r.allow("k") {
		t.Fatal("second allow within window must be rejected")
	}
	now = now.Add(time.Second)
	if !r.allow("k") {
		t.Fatal("allow after window must pass")
	}
}

// TestHandleStateUpdateInvalidPayload makes sure malformed payloads don't
// panic and don't publish anything.
func TestHandleStateUpdateInvalidPayload(t *testing.T) {
	h := NewHub(slog.Default())
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	conn := &AgentConnection{
		ClusterID: "cluster-bad",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	h.handleMessage(conn, &protocol.Message{
		Type:    protocol.MsgStateUpdate,
		Payload: []byte("{not-json"),
	})

	if got := len(pub.Snapshot()); got != 0 {
		t.Fatalf("expected 0 publishes for invalid payload, got %d", got)
	}
}
