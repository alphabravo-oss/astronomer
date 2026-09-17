package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestNewHub(t *testing.T) {
	h := NewHub(slog.Default())
	if h == nil {
		t.Fatal("NewHub returned nil")
		return
	}
	if n := h.agents.Len(); n != 0 {
		t.Fatalf("expected 0 agents, got %d", n)
	}
}

func TestNewHubNilLogger(t *testing.T) {
	h := NewHub(nil)
	if h == nil {
		t.Fatal("NewHub with nil logger returned nil")
		return
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

func TestSendToAgentContextCarriesW3CParent(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})

	h := NewHub(slog.Default())
	agent := &AgentConnection{ClusterID: "trace-cluster", sendCh: make(chan *protocol.Message, 1)}
	h.agents.Set(agent.ClusterID, agent)
	ctx, span := provider.Tracer("test/server").Start(context.Background(), "request")
	defer span.End()
	if err := h.SendToAgentContext(ctx, agent.ClusterID, &protocol.Message{Type: protocol.MsgHealthCheck}); err != nil {
		t.Fatal(err)
	}
	received := <-agent.sendCh
	extracted := observability.ContextWithTraceContext(context.Background(), received.Traceparent, received.Tracestate)
	if got := trace.SpanContextFromContext(extracted).TraceID(); got != span.SpanContext().TraceID() {
		t.Fatalf("tunnel trace ID = %s, want %s", got, span.SpanContext().TraceID())
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

// acceptingConnectValidator is explicit test wiring for websocket fixtures.
// The token is the cluster UUID, so one validator safely supports multiple
// connections without reintroducing the production nil-validator bypass.
type acceptingConnectValidator struct{ *recordingValidator }

func newAcceptingConnectValidator() *acceptingConnectValidator {
	return &acceptingConnectValidator{recordingValidator: &recordingValidator{}}
}

func (v *acceptingConnectValidator) GetRegistrationTokenByToken(_ context.Context, token string) (sqlc.ClusterRegistrationToken, error) {
	clusterID, err := uuid.Parse(token)
	if err != nil {
		return sqlc.ClusterRegistrationToken{}, err
	}
	return sqlc.ClusterRegistrationToken{ID: uuid.New(), ClusterID: clusterID}, nil
}

func newConnectTestHub(log *slog.Logger) *Hub {
	return NewHubWithValidator(log, newAcceptingConnectValidator())
}

// connectAgent sends a CONNECT message and reads the CONNECT_ACK.
func connectAgent(t *testing.T, conn *websocket.Conn, ctx context.Context, clusterID, agentID string) protocol.ConnectAckPayload {
	t.Helper()
	connectPayload, _ := json.Marshal(protocol.ConnectPayload{
		ClusterID: clusterID, AgentID: agentID, AgentVersion: "1.0.0",
		TunnelProtocolVersion: protocol.TunnelProtocolVersion, HeartbeatSchemaVersion: protocol.HeartbeatSchemaVersion,
		DeliveryProtocolVersion: protocol.DeliveryProtocolVersion, Capabilities: protocol.RequiredConnectCapabilities(), Token: clusterID,
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
	h := newConnectTestHub(slog.New(slog.NewJSONHandler(buf, nil)))
	_, conn, ctx := testServerAndClient(t, h)

	const clusterID = "11111111-1111-4111-8111-111111111111"
	connectAgent(t, conn, ctx, clusterID, "agent-1")

	// Give the hub time to register the agent.
	time.Sleep(50 * time.Millisecond)

	// Verify agent is registered.
	agent := h.GetAgent(clusterID)
	if agent == nil {
		t.Fatal("expected agent to be registered")
		return
	}
	if agent.ClusterID != clusterID {
		t.Fatalf("expected %s, got %s", clusterID, agent.ClusterID)
	}
	if agent.AgentID != "agent-1" {
		t.Fatalf("expected agent-1, got %s", agent.AgentID)
	}

	clusters := h.ConnectedClusters()
	if len(clusters) != 1 || clusters[0] != clusterID {
		t.Fatalf("expected [%s], got %v", clusterID, clusters)
	}

	// Close the connection and verify deregistration.
	_ = conn.Close(websocket.StatusNormalClosure, "done")
	time.Sleep(100 * time.Millisecond)

	if a := h.GetAgent(clusterID); a != nil {
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
	h := newConnectTestHub(slog.Default())
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
	h := newConnectTestHub(slog.Default())
	_, conn, ctx := testServerAndClient(t, h)

	const clusterID = "22222222-2222-4222-8222-222222222222"
	connectAgent(t, conn, ctx, clusterID, "agent-2")
	time.Sleep(50 * time.Millisecond)

	// Send a message to the agent via the hub.
	healthCheckMsg := &protocol.Message{Type: protocol.MsgHealthCheck, RequestID: "hc-1"}
	if err := h.SendToAgent(clusterID, healthCheckMsg); err != nil {
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

	_ = conn.Close(websocket.StatusNormalClosure, "done")
}

func TestSendToAgentRequiresAdvertisedOperationCapability(t *testing.T) {
	h := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: "capability-cluster",
		Capabilities: map[string]struct{}{
			"watch": {},
		},
		sendCh: make(chan *protocol.Message, 2),
	}
	h.agents.Set(agent.ClusterID, agent)

	readPayload, _ := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodGet, Path: "/api/v1/pods"})
	if err := h.SendToAgent(agent.ClusterID, &protocol.Message{Type: protocol.MsgK8sRequest, Payload: readPayload}); err != nil {
		t.Fatalf("advertised read capability rejected: %v", err)
	}
	mutatePayload, _ := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodDelete, Path: "/api/v1/pods/p"})
	if err := h.SendToAgent(agent.ClusterID, &protocol.Message{Type: protocol.MsgK8sRequest, Payload: mutatePayload}); err == nil || !strings.Contains(err.Error(), "mutate") {
		t.Fatalf("unadvertised mutation error = %v, want mutate capability rejection", err)
	}
	if err := h.SendToAgent(agent.ClusterID, &protocol.Message{Type: protocol.MsgHelmInstall}); err == nil || !strings.Contains(err.Error(), "helm") {
		t.Fatalf("unadvertised Helm error = %v, want helm capability rejection", err)
	}
}

func TestBroadcastToAll(t *testing.T) {
	h := newConnectTestHub(slog.Default())

	// Connect two agents.
	conns := make([]*websocket.Conn, 2)
	clusterIDs := []string{
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
	}

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
		_ = conn.Close(websocket.StatusNormalClosure, "done")
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

	if got := len(validator.SnapshotHeartbeatArgs()); got != 1 {
		t.Fatalf("expected one atomic heartbeat write, got %d", got)
	}

	_ = conn.Close(websocket.StatusNormalClosure, "done")
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

func TestConnectBlocksIncompatibleAgentVersion(t *testing.T) {
	clusterID := "945db76b-d7f3-4e6c-8c70-6ca50ca514f4"
	h := newConnectTestHub(slog.Default())
	pub := &recordingPublisher{}
	h.SetPublisher(pub)
	_, conn, ctx := testServerAndClient(t, h)

	connectPayload, _ := json.Marshal(protocol.ConnectPayload{
		ClusterID: clusterID, AgentID: "agent-old",
		AgentVersion:          "v0.9.9", // below the compatible floor → blocked
		TunnelProtocolVersion: protocol.TunnelProtocolVersion, HeartbeatSchemaVersion: protocol.HeartbeatSchemaVersion,
		DeliveryProtocolVersion: protocol.DeliveryProtocolVersion, Capabilities: protocol.RequiredConnectCapabilities(), Token: "test-token",
	})
	if err := wsjson.Write(ctx, conn, &protocol.Message{Type: protocol.MsgConnect, Payload: connectPayload}); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	var msg protocol.Message
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatalf("read structured rejection: %v", err)
	}
	var rejection protocol.ConnectAckPayload
	if err := json.Unmarshal(msg.Payload, &rejection); err != nil {
		t.Fatal(err)
	}
	if rejection.Accepted || rejection.ReasonCode != "agent_version_too_old" || rejection.UpgradeRecommendation == "" {
		t.Fatalf("unexpected structured rejection: %+v", rejection)
	}
	err := wsjson.Read(ctx, conn, &msg)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("read error status = %v err=%v, want policy violation", websocket.CloseStatus(err), err)
	}
	if connected := h.ConnectedClusters(); len(connected) != 0 {
		t.Fatalf("connected clusters = %v, want none", connected)
	}
	events := pub.Snapshot()
	if len(events) != 1 || events[0].Type != "agent.failed" {
		t.Fatalf("events = %+v, want one agent.failed", events)
	}
}

func TestExpectedAgentDisconnect(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "normal closure", err: websocket.CloseError{Code: websocket.StatusNormalClosure}, want: true},
		{name: "going away", err: websocket.CloseError{Code: websocket.StatusGoingAway}, want: true},
		{name: "peer eof", err: fmt.Errorf("read frame: %w", io.EOF), want: true},
		{name: "policy violation", err: websocket.CloseError{Code: websocket.StatusPolicyViolation}},
		{name: "transport error", err: errors.New("connection reset")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := expectedAgentDisconnect(test.err); got != test.want {
				t.Fatalf("expectedAgentDisconnect(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

func TestConnectRejectsPreV2AgentWithStableReenrollmentReason(t *testing.T) {
	h := newConnectTestHub(slog.Default())
	_, conn, ctx := testServerAndClient(t, h)
	payload, _ := json.Marshal(protocol.ConnectPayload{
		ClusterID: "945db76b-d7f3-4e6c-8c70-6ca50ca514f4", AgentID: "old-agent",
		AgentVersion: "1.0.0", TunnelProtocolVersion: protocol.TunnelProtocolVersion,
		HeartbeatSchemaVersion: protocol.HeartbeatSchemaVersion, Capabilities: protocol.RequiredConnectCapabilities(), Token: "test-token",
	})
	if err := wsjson.Write(ctx, conn, &protocol.Message{Type: protocol.MsgConnect, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var message protocol.Message
	if err := wsjson.Read(ctx, conn, &message); err != nil {
		t.Fatalf("read rejection ack: %v", err)
	}
	if message.Type != protocol.MsgConnectAck {
		t.Fatalf("got %s, want CONNECT_ACK", message.Type)
	}
	var ack protocol.ConnectAckPayload
	if err := json.Unmarshal(message.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Accepted || ack.Reason != "agent_reenrollment_required" || ack.ReasonCode != "delivery_protocol_unsupported" {
		t.Fatalf("unexpected rejection: %#v", ack)
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
	// A successful CONNECT also records an agent.connected audit (A4), but that
	// is emitted AFTER the ACK (post-registration) so it races this read — the
	// connect audit is asserted by the dedicated A4 tests. Here we filter to the
	// token-rotation row, which is recorded synchronously before the ACK.
	auditRows := validator.SnapshotAuditRows()
	var rotated *sqlc.CreateAuditLogV1Params
	for i := range auditRows {
		if auditRows[i].Action == "agent.token.rotated" {
			rotated = &auditRows[i]
		}
	}
	if rotated == nil {
		t.Fatalf("expected an agent.token.rotated audit row, got %d rows", len(auditRows))
		return
	}
	if rotated.ResourceType != "cluster" || rotated.ResourceID != clusterID {
		t.Fatalf("audit resource = %s/%s, want cluster/%s", rotated.ResourceType, rotated.ResourceID, clusterID)
	}
	if strings.Contains(string(rotated.Detail), ack.AgentToken) {
		t.Fatal("audit detail leaked durable agent token plaintext")
	}

	_ = conn.Close(websocket.StatusNormalClosure, "done")
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
