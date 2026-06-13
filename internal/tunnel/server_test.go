package tunnel

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestNewHub(t *testing.T) {
	h := NewHub(slog.Default())
	if h == nil {
		t.Fatal("NewHub returned nil")
	}
	if n := h.agents.Len(); n != 0 {
		t.Fatalf("expected 0 agents, got %d", n)
	}
}

func TestNewHubNilLogger(t *testing.T) {
	h := NewHub(nil)
	if h == nil {
		t.Fatal("NewHub with nil logger returned nil")
	}
	if h.log == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestGetAgentNotConnected(t *testing.T) {
	h := NewHub(slog.Default())
	if agent := h.GetAgent("nonexistent"); agent != nil {
		t.Fatalf("expected nil, got %v", agent)
	}
}

func TestConnectedClustersEmpty(t *testing.T) {
	h := NewHub(slog.Default())
	clusters := h.ConnectedClusters()
	if len(clusters) != 0 {
		t.Fatalf("expected 0 clusters, got %d", len(clusters))
	}
}

func TestSendToAgentNotConnected(t *testing.T) {
	h := NewHub(slog.Default())
	err := h.SendToAgent("nonexistent", &protocol.Message{Type: protocol.MsgPong})
	if err == nil {
		t.Fatal("expected error for non-connected agent")
	}
}

// testServerAndClient sets up an httptest server with the hub's WebSocket handler
// and returns a client WebSocket connection.
func testServerAndClient(t *testing.T, h *Hub) (*httptest.Server, *websocket.Conn, context.Context) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(h.HandleWebSocket))
	t.Cleanup(func() { srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	wsURL := "ws" + srv.URL[4:] // http -> ws
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	return srv, conn, ctx
}

// connectAgent sends a CONNECT message and reads the CONNECT_ACK.
func connectAgent(t *testing.T, conn *websocket.Conn, ctx context.Context, clusterID, agentID string) protocol.ConnectAckPayload {
	t.Helper()
	connectPayload, _ := json.Marshal(protocol.ConnectPayload{
		ClusterID:    clusterID,
		AgentID:      agentID,
		AgentVersion: "1.0.0",
		Token:        "test-token",
	})
	connectMsg := protocol.Message{
		Type:    protocol.MsgConnect,
		Payload: connectPayload,
	}
	if err := wsjson.Write(ctx, conn, &connectMsg); err != nil {
		t.Fatalf("write connect: %v", err)
	}

	var ack protocol.Message
	if err := wsjson.Read(ctx, conn, &ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack.Type != protocol.MsgConnectAck {
		t.Fatalf("expected CONNECT_ACK, got %s", ack.Type)
	}

	var ackPayload protocol.ConnectAckPayload
	if err := json.Unmarshal(ack.Payload, &ackPayload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}
	if !ackPayload.Accepted {
		t.Fatalf("expected accepted=true, got false: %s", ackPayload.Reason)
	}
	return ackPayload
}

func TestAgentConnectAndDisconnect(t *testing.T) {
	// The hub writes log lines from its goroutine while the test reads
	// the buffer at the end — guard the buffer to avoid a -race fail.
	buf := newSyncBuffer()
	h := NewHub(slog.New(slog.NewJSONHandler(buf, nil)))
	_, conn, ctx := testServerAndClient(t, h)

	connectAgent(t, conn, ctx, "cluster-1", "agent-1")

	// Give the hub time to register the agent.
	time.Sleep(50 * time.Millisecond)

	// Verify agent is registered.
	agent := h.GetAgent("cluster-1")
	if agent == nil {
		t.Fatal("expected agent to be registered")
	}
	if agent.ClusterID != "cluster-1" {
		t.Fatalf("expected cluster-1, got %s", agent.ClusterID)
	}
	if agent.AgentID != "agent-1" {
		t.Fatalf("expected agent-1, got %s", agent.AgentID)
	}

	clusters := h.ConnectedClusters()
	if len(clusters) != 1 || clusters[0] != "cluster-1" {
		t.Fatalf("expected [cluster-1], got %v", clusters)
	}

	// Close the connection and verify deregistration.
	conn.Close(websocket.StatusNormalClosure, "done")
	time.Sleep(100 * time.Millisecond)

	if a := h.GetAgent("cluster-1"); a != nil {
		t.Fatal("expected agent to be deregistered after disconnect")
	}
	if len(h.ConnectedClusters()) != 0 {
		t.Fatal("expected 0 connected clusters after disconnect")
	}
	body := buf.String()
	if !strings.Contains(body, `"event":"agent_connected"`) {
		t.Fatalf("expected agent_connected event log, got %s", body)
	}
	if !strings.Contains(body, `"event":"agent_disconnected"`) {
		t.Fatalf("expected agent_disconnected event log, got %s", body)
	}
}

func TestAgentConnectInvalidFirstMessage(t *testing.T) {
	h := NewHub(slog.Default())
	_, conn, ctx := testServerAndClient(t, h)

	// Send a non-CONNECT message (use PONG since MsgPing was removed).
	msg := protocol.Message{Type: protocol.MsgPong}
	if err := wsjson.Write(ctx, conn, &msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The server should close the connection.
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error after invalid first message")
	}
}

func TestSendToConnectedAgent(t *testing.T) {
	h := NewHub(slog.Default())
	_, conn, ctx := testServerAndClient(t, h)

	connectAgent(t, conn, ctx, "cluster-2", "agent-2")
	time.Sleep(50 * time.Millisecond)

	// Send a message to the agent via the hub.
	healthCheckMsg := &protocol.Message{Type: protocol.MsgHealthCheck, RequestID: "hc-1"}
	if err := h.SendToAgent("cluster-2", healthCheckMsg); err != nil {
		t.Fatalf("SendToAgent: %v", err)
	}

	// Read the message on the client side.
	var received protocol.Message
	if err := wsjson.Read(ctx, conn, &received); err != nil {
		t.Fatalf("read message: %v", err)
	}
	if received.Type != protocol.MsgHealthCheck {
		t.Fatalf("expected HEALTH_CHECK, got %s", received.Type)
	}
	if received.RequestID != "hc-1" {
		t.Fatalf("expected request_id hc-1, got %s", received.RequestID)
	}

	conn.Close(websocket.StatusNormalClosure, "done")
}

func TestBroadcastToAll(t *testing.T) {
	h := NewHub(slog.Default())

	// Connect two agents.
	conns := make([]*websocket.Conn, 2)
	clusterIDs := []string{"cluster-a", "cluster-b"}

	for i, cid := range clusterIDs {
		_, conn, ctx := testServerAndClient(t, h)
		conns[i] = conn
		connectAgent(t, conn, ctx, cid, "agent-"+cid)
	}

	time.Sleep(50 * time.Millisecond)

	if len(h.ConnectedClusters()) != 2 {
		t.Fatalf("expected 2 connected clusters, got %d", len(h.ConnectedClusters()))
	}

	// Broadcast.
	h.BroadcastToAll(&protocol.Message{Type: protocol.MsgHealthCheck, RequestID: "hc-1"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i, conn := range conns {
		var msg protocol.Message
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			t.Fatalf("read broadcast on conn %d: %v", i, err)
		}
		if msg.Type != protocol.MsgHealthCheck {
			t.Fatalf("expected HEALTH_CHECK, got %s", msg.Type)
		}
	}

	for _, conn := range conns {
		conn.Close(websocket.StatusNormalClosure, "done")
	}
}

func TestAgentConnectionPersistenceLifecycle(t *testing.T) {
	clusterID := "945db76b-d7f3-4e6c-8c70-6ca50ca514f4"
	validator := &recordingValidator{tokenClusterID: clusterID}
	h := NewHubWithValidator(slog.Default(), validator)
	_, conn, ctx := testServerAndClient(t, h)

	connectAgent(t, conn, ctx, clusterID, "agent-persist")
	time.Sleep(50 * time.Millisecond)

	creates := validator.SnapshotCreates()
	if len(creates) != 1 {
		t.Fatalf("expected 1 persisted connection row, got %d", len(creates))
	}
	disconnectedClusters := validator.SnapshotDisconnectedClusters()
	if len(disconnectedClusters) != 1 || disconnectedClusters[0].String() != clusterID {
		t.Fatalf("expected stale-session cleanup for cluster %s, got %v", clusterID, disconnectedClusters)
	}
	if creates[0].ClusterID.String() != clusterID {
		t.Fatalf("expected cluster id %s, got %s", clusterID, creates[0].ClusterID)
	}
	if creates[0].AgentID != "agent-persist" {
		t.Fatalf("expected agent id agent-persist, got %s", creates[0].AgentID)
	}
	if creates[0].SessionID == "" {
		t.Fatal("expected non-empty session id to be persisted")
	}
	if creates[0].Status != "connected" {
		t.Fatalf("expected status connected, got %s", creates[0].Status)
	}

	heartbeatPayload, _ := json.Marshal(protocol.HeartbeatPayload{AgentVersion: "1.0.0"})
	if err := wsjson.Write(ctx, conn, &protocol.Message{Type: protocol.MsgHeartbeat, Payload: heartbeatPayload}); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if got := len(validator.SnapshotPings()); got == 0 {
		t.Fatal("expected persisted ping update after heartbeat")
	}

	conn.Close(websocket.StatusNormalClosure, "done")
	time.Sleep(100 * time.Millisecond)

	disconnects := validator.SnapshotDisconnects()
	if len(disconnects) != 1 {
		t.Fatalf("expected 1 persisted disconnect, got %d", len(disconnects))
	}
	if disconnects[0].Status != "disconnected" {
		t.Fatalf("expected status disconnected, got %s", disconnects[0].Status)
	}
	if !disconnects[0].DisconnectedAt.Valid {
		t.Fatal("expected disconnected_at to be set")
	}
}

func TestConnectAckRotatesToDurableAgentToken(t *testing.T) {
	clusterID := "945db76b-d7f3-4e6c-8c70-6ca50ca514f4"
	validator := &recordingValidator{tokenClusterID: clusterID}
	h := NewHubWithValidator(slog.Default(), validator)
	_, conn, ctx := testServerAndClient(t, h)

	ack := connectAgent(t, conn, ctx, clusterID, "agent-rotate")
	if ack.AgentToken == "" {
		t.Fatal("expected durable agent token in CONNECT_ACK")
	}

	upserts := validator.SnapshotAgentTokenUpserts()
	if len(upserts) != 1 {
		t.Fatalf("expected 1 durable token upsert, got %d", len(upserts))
	}
	if upserts[0].ClusterID.String() != clusterID {
		t.Fatalf("expected cluster id %s, got %s", clusterID, upserts[0].ClusterID)
	}
	if upserts[0].Token != "" {
		t.Fatalf("expected durable token plaintext not to be persisted")
	}
	if upserts[0].TokenHash != auth.HashOpaqueToken(ack.AgentToken) {
		t.Fatalf("expected persisted token hash to match ack token")
	}

	conn.Close(websocket.StatusNormalClosure, "done")
}

// --- StreamManager tests ---

func TestStreamManagerCreateAndGet(t *testing.T) {
	sm := NewStreamManager(10)

	s, err := sm.CreateStream("stream-1")
	if err != nil {
		t.Fatalf("CreateStream: %v", err)
	}
	if s.ID != "stream-1" {
		t.Fatalf("expected stream-1, got %s", s.ID)
	}

	got, ok := sm.GetStream("stream-1")
	if !ok {
		t.Fatal("expected stream to be found")
	}
	if got != s {
		t.Fatal("expected same stream instance")
	}

	if sm.Count() != 1 {
		t.Fatalf("expected count 1, got %d", sm.Count())
	}
}

func TestStreamManagerDuplicateCreate(t *testing.T) {
	sm := NewStreamManager(10)

	if _, err := sm.CreateStream("dup"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := sm.CreateStream("dup"); err == nil {
		t.Fatal("expected error for duplicate stream")
	}
}

func TestStreamManagerMaxLimit(t *testing.T) {
	sm := NewStreamManager(2)

	if _, err := sm.CreateStream("s1"); err != nil {
		t.Fatalf("create s1: %v", err)
	}
	if _, err := sm.CreateStream("s2"); err != nil {
		t.Fatalf("create s2: %v", err)
	}
	if _, err := sm.CreateStream("s3"); err == nil {
		t.Fatal("expected error when max streams reached")
	}
}

func TestStreamManagerClose(t *testing.T) {
	sm := NewStreamManager(10)

	s, _ := sm.CreateStream("to-close")
	sm.CloseStream("to-close")

	if _, ok := sm.GetStream("to-close"); ok {
		t.Fatal("expected stream to be removed after close")
	}
	if sm.Count() != 0 {
		t.Fatalf("expected count 0, got %d", sm.Count())
	}
	if !s.IsClosed() {
		t.Fatal("expected stream to be marked closed")
	}

	// Closing a non-existent stream should not panic.
	sm.CloseStream("nonexistent")
}

func TestStreamManagerCloseAll(t *testing.T) {
	sm := NewStreamManager(10)

	s1, _ := sm.CreateStream("s1")
	s2, _ := sm.CreateStream("s2")

	sm.CloseAll()

	if sm.Count() != 0 {
		t.Fatalf("expected count 0 after CloseAll, got %d", sm.Count())
	}
	if !s1.IsClosed() {
		t.Fatal("expected s1 to be closed")
	}
	if !s2.IsClosed() {
		t.Fatal("expected s2 to be closed")
	}
}

func TestStreamManagerDefaultMaxStreams(t *testing.T) {
	sm := NewStreamManager(0)
	if sm.maxStreams != 256 {
		t.Fatalf("expected default max 256, got %d", sm.maxStreams)
	}
}

func TestStreamDoneCh(t *testing.T) {
	sm := NewStreamManager(10)
	s, _ := sm.CreateStream("done-test")

	// DoneCh should not be closed yet.
	select {
	case <-s.DoneCh:
		t.Fatal("DoneCh should not be closed yet")
	default:
	}

	sm.CloseStream("done-test")

	// DoneCh should now be closed.
	select {
	case <-s.DoneCh:
		// ok
	case <-time.After(time.Second):
		t.Fatal("DoneCh should be closed after CloseStream")
	}
}

// --- Message dispatch tests ---

func TestHandleMessagePong(t *testing.T) {
	h := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: "test-cluster",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	// Should not panic.
	h.handleMessage(agent, &protocol.Message{Type: protocol.MsgPong})
}

func TestHandleMessageHeartbeat(t *testing.T) {
	h := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: "test-cluster",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	payload, _ := json.Marshal(protocol.HeartbeatPayload{
		KubernetesVersion: "1.28.0",
		NodeCount:         3,
	})
	msg := &protocol.Message{
		Type:    protocol.MsgHeartbeat,
		Payload: payload,
	}

	// Should not panic.
	h.handleMessage(agent, msg)
}

func TestHandleMessageK8sResponseRoutesToStream(t *testing.T) {
	h := NewHub(slog.Default())
	sm := NewStreamManager(10)
	agent := &AgentConnection{
		ClusterID: "test-cluster",
		Streams:   sm,
		sendCh:    make(chan *protocol.Message, 10),
	}

	stream, _ := sm.CreateStream("req-123")

	responsePayload, _ := json.Marshal(protocol.K8sResponsePayload{StatusCode: 200})
	msg := &protocol.Message{
		Type:     protocol.MsgK8sResponse,
		StreamID: "req-123",
		Payload:  responsePayload,
	}

	h.handleMessage(agent, msg)

	select {
	case data := <-stream.DataCh:
		var resp protocol.K8sResponsePayload
		if err := json.Unmarshal(data, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	case <-time.After(time.Second):
		t.Fatal("expected data on stream")
	}
}

func TestHandleMessageErrorRoutesToStream(t *testing.T) {
	h := NewHub(slog.Default())
	sm := NewStreamManager(10)
	agent := &AgentConnection{
		ClusterID: "test-cluster",
		Streams:   sm,
		sendCh:    make(chan *protocol.Message, 10),
	}

	stream, _ := sm.CreateStream("err-stream")

	errPayload, _ := json.Marshal(protocol.ErrorPayload{
		Code:    "TEST_ERROR",
		Message: "something went wrong",
	})
	msg := &protocol.Message{
		Type:     protocol.MsgError,
		StreamID: "err-stream",
		Payload:  errPayload,
	}

	h.handleMessage(agent, msg)

	select {
	case <-stream.DataCh:
		// Got routed - good.
	case <-time.After(time.Second):
		t.Fatal("expected error to be routed to stream")
	}
}

func TestHandleMessageK8sResponseCountsDroppedEventWhenStreamFull(t *testing.T) {
	oldInstanceID := observability.InstanceID()
	observability.SetInstanceID("test-tunnel-route")
	t.Cleanup(func() {
		observability.SetInstanceID(oldInstanceID)
	})

	h := NewHub(slog.Default())
	sm := NewStreamManager(10)
	agent := &AgentConnection{
		ClusterID: "test-cluster",
		Streams:   sm,
		sendCh:    make(chan *protocol.Message, 10),
	}

	stream, _ := sm.CreateStream("req-123")
	for i := 0; i < cap(stream.DataCh); i++ {
		stream.DataCh <- []byte("full")
	}

	responsePayload, _ := json.Marshal(protocol.K8sResponsePayload{StatusCode: 200})
	msg := &protocol.Message{
		Type:     protocol.MsgK8sResponse,
		StreamID: "req-123",
		Payload:  responsePayload,
	}

	before := tunnelDroppedCounterValue(t, map[string]string{
		"astronomer_instance_id": "test-tunnel-route",
		"component":              "tunnel_stream_route",
		"reason":                 "channel_full",
	})

	h.handleMessage(agent, msg)

	after := tunnelDroppedCounterValue(t, map[string]string{
		"astronomer_instance_id": "test-tunnel-route",
		"component":              "tunnel_stream_route",
		"reason":                 "channel_full",
	})
	if after != before+1 {
		t.Fatalf("dropped events counter = %v, want %v", after, before+1)
	}
}

func TestHandleMessageUnknownType(t *testing.T) {
	h := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: "test-cluster",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	// Should not panic, just log a warning.
	h.handleMessage(agent, &protocol.Message{Type: "TOTALLY_UNKNOWN"})
}

func tunnelDroppedCounterValue(t *testing.T, wantLabels map[string]string) float64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "astronomer_dropped_events_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if tunnelDroppedLabelsMatch(metric.GetLabel(), wantLabels) && metric.Counter != nil {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func tunnelDroppedLabelsMatch(labels []*dto.LabelPair, want map[string]string) bool {
	if len(labels) != len(want) {
		return false
	}
	for _, label := range labels {
		if want[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}
