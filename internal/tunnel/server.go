// Package tunnel implements the server side of the multiplexed JSON
// WebSocket tunnel between the management server and remote cluster agents.
//
// Liveness model
// --------------
// The agent is the SOLE originator of HEARTBEAT messages. The agent emits a
// HEARTBEAT periodically (config.HeartbeatInterval, default 30s) carrying
// node/pod count and lightweight cluster metadata, and a separate METRICS
// frame on a slower ticker (config.MetricsInterval, default 60s).
//
// The server does NOT actively ping the agent. If the WebSocket read goroutine
// is silent for longer than the underlying TCP keepalive (handled by
// nhooyr.io/websocket and the OS), the connection is torn down. Database
// liveness is updated whenever a HEARTBEAT lands (see handleHeartbeat).
//
// PONG is reserved for cases where the SERVER explicitly sends a HEARTBEAT
// (e.g. for a future server-initiated probe); the agent's readLoop already
// replies with PONG. Today the server never originates HEARTBEAT, so PONG
// frames should not appear on the wire — the handlePong handler exists only
// to avoid logging spurious "unknown message type" warnings if a future
// version of either side starts probing.
package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	// connectTimeout is the maximum time to wait for the initial CONNECT message.
	connectTimeout = 10 * time.Second

	// writeTimeout is the maximum time to wait for a write to complete.
	writeTimeout = 10 * time.Second

	// sendChannelSize is the buffer size for the per-agent send channel.
	sendChannelSize = 256
)

// AgentConnection represents a connected agent.
type AgentConnection struct {
	ClusterID    string
	AgentID      string
	AgentVersion string
	SessionID    string
	Conn         *websocket.Conn
	Streams      *StreamManager
	cancel       context.CancelFunc
	sendCh       chan *protocol.Message
}

// Hub manages all connected agent tunnels.
type Hub struct {
	mu        sync.RWMutex
	agents    map[string]*AgentConnection // keyed by clusterID
	log       *slog.Logger
	validator AgentTokenValidator
	// publisher receives connect/disconnect/heartbeat lifecycle events for
	// fan-out to SSE subscribers. Optional; nil-safe.
	publisher LifecyclePublisher
	// stateLim collapses redundant STATE_UPDATE fan-outs at the (cluster_id,
	// kind, namespace) granularity. Lazily constructed on first use so the
	// hub remains zero-value-safe.
	stateLim *stateUpdateLimiter
}

// LifecyclePublisher is the interface the events bus implements; the tunnel
// package depends on the interface, not the bus, to keep import direction clean.
type LifecyclePublisher interface {
	Publish(eventType string, data any)
}

// SetPublisher attaches a lifecycle publisher (set once at startup).
func (h *Hub) SetPublisher(p LifecyclePublisher) {
	h.mu.Lock()
	h.publisher = p
	h.mu.Unlock()
}

func (h *Hub) publish(eventType string, clusterID, sessionID, agentVersion string) {
	h.mu.RLock()
	p := h.publisher
	h.mu.RUnlock()
	if p == nil {
		return
	}
	p.Publish(eventType, map[string]any{
		"cluster_id":    clusterID,
		"session_id":    sessionID,
		"agent_version": agentVersion,
	})
}

type AgentTokenValidator interface {
	GetRegistrationTokenByToken(ctx context.Context, token string) (sqlc.ClusterRegistrationToken, error)
	MarkRegistrationTokenUsed(ctx context.Context, id uuid.UUID) error
	UpdateClusterHeartbeat(ctx context.Context, arg sqlc.UpdateClusterHeartbeatParams) error
	UpsertClusterHealthStatus(ctx context.Context, arg sqlc.UpsertClusterHealthStatusParams) (sqlc.ClusterHealthStatus, error)
}

// NewHub creates a new Hub.
func NewHub(log *slog.Logger) *Hub {
	return NewHubWithValidator(log, nil)
}

// NewHubWithValidator creates a new Hub with optional DB-backed token validation.
func NewHubWithValidator(log *slog.Logger, validator AgentTokenValidator) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		agents:    make(map[string]*AgentConnection),
		log:       log,
		validator: validator,
	}
}

// HandleWebSocket is the HTTP handler for WebSocket upgrade.
// Route: /api/v1/ws/agent/tunnel/{cluster_id}/
func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Allow any origin for agent connections; agents authenticate via token.
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.log.Error("websocket accept failed", slog.String("error", err.Error()))
		return
	}

	// Default nhooyr.io/websocket read limit is 32 KiB which is too small for
	// proxied k8s API list responses. Bump to 16 MiB on the tunnel.
	conn.SetReadLimit(16 << 20)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 1. Read first message — must be CONNECT with valid token.
	connectCtx, connectCancel := context.WithTimeout(ctx, connectTimeout)
	defer connectCancel()

	var firstMsg protocol.Message
	if err := wsjson.Read(connectCtx, conn, &firstMsg); err != nil {
		h.log.Error("failed to read connect message", slog.String("error", err.Error()))
		conn.Close(websocket.StatusProtocolError, "expected CONNECT message")
		return
	}

	if firstMsg.Type != protocol.MsgConnect {
		h.log.Warn("first message was not CONNECT", slog.String("type", string(firstMsg.Type)))
		conn.Close(websocket.StatusProtocolError, "first message must be CONNECT")
		return
	}

	var payload protocol.ConnectPayload
	if err := json.Unmarshal(firstMsg.Payload, &payload); err != nil {
		h.log.Error("invalid connect payload", slog.String("error", err.Error()))
		conn.Close(websocket.StatusProtocolError, "invalid CONNECT payload")
		return
	}

	// 2. Validate registration token.
	if payload.ClusterID == "" || payload.Token == "" {
		h.log.Warn("connect payload missing cluster_id or token")
		conn.Close(websocket.StatusProtocolError, "cluster_id and token are required")
		return
	}

	if h.validator != nil {
		tokenRecord, err := h.validator.GetRegistrationTokenByToken(ctx, payload.Token)
		if err != nil {
			h.log.Warn("invalid registration token", slog.String("cluster_id", payload.ClusterID))
			h.publish("agent.failed", payload.ClusterID, "", payload.AgentVersion)
			conn.Close(websocket.StatusPolicyViolation, "invalid registration token")
			return
		}
		if tokenRecord.ClusterID.String() != payload.ClusterID {
			h.log.Warn("registration token cluster mismatch",
				slog.String("expected_cluster_id", tokenRecord.ClusterID.String()),
				slog.String("provided_cluster_id", payload.ClusterID),
			)
			h.publish("agent.failed", payload.ClusterID, "", payload.AgentVersion)
			conn.Close(websocket.StatusPolicyViolation, "registration token does not match cluster")
			return
		}
		if err := h.validator.MarkRegistrationTokenUsed(ctx, tokenRecord.ID); err != nil {
			h.log.Error("failed to mark registration token used", slog.String("error", err.Error()))
			conn.Close(websocket.StatusInternalError, "failed to mark token used")
			return
		}
	}

	// 3. Generate session ID and send CONNECT_ACK.
	sessionID := fmt.Sprintf("session-%s-%d", payload.ClusterID, time.Now().UnixNano())

	ackPayload, _ := json.Marshal(protocol.ConnectAckPayload{
		Accepted: true,
	})

	ackMsg := &protocol.Message{
		Type:    protocol.MsgConnectAck,
		Payload: ackPayload,
	}

	writeCtx, writeCancel := context.WithTimeout(ctx, writeTimeout)
	if err := wsjson.Write(writeCtx, conn, ackMsg); err != nil {
		writeCancel()
		h.log.Error("failed to send connect ack", slog.String("error", err.Error()))
		conn.Close(websocket.StatusInternalError, "failed to send CONNECT_ACK")
		return
	}
	writeCancel()

	// 4. Register agent in hub.
	agent := &AgentConnection{
		ClusterID:    payload.ClusterID,
		AgentID:      payload.AgentID,
		AgentVersion: payload.AgentVersion,
		SessionID:    sessionID,
		Conn:         conn,
		Streams:      NewStreamManager(256),
		cancel:       cancel,
		sendCh:       make(chan *protocol.Message, sendChannelSize),
	}

	// Disconnect any existing connection for this cluster. If we replaced one,
	// surface that as an agent.reconnecting hint so subscribers can show the
	// transition explicitly (the cluster.connected fan-out below covers the
	// happy path; this distinguishes "first connect" from "reconnect").
	h.mu.Lock()
	wasReconnect := false
	if existing, ok := h.agents[payload.ClusterID]; ok {
		h.log.Info("replacing existing agent connection",
			slog.String("cluster_id", payload.ClusterID),
			slog.String("old_session", existing.SessionID),
		)
		existing.cancel()
		existing.Streams.CloseAll()
		wasReconnect = true
	}
	h.agents[payload.ClusterID] = agent
	h.mu.Unlock()
	if wasReconnect {
		h.publish("agent.reconnecting", payload.ClusterID, sessionID, payload.AgentVersion)
	}

	h.log.Info("agent connected",
		slog.String("cluster_id", payload.ClusterID),
		slog.String("agent_id", payload.AgentID),
		slog.String("agent_version", payload.AgentVersion),
		slog.String("session_id", sessionID),
	)
	h.publish("cluster.connected", payload.ClusterID, sessionID, payload.AgentVersion)

	// 5. Start read/write goroutines.
	var wg sync.WaitGroup
	wg.Add(2)

	// Write goroutine: sends messages from sendCh to the WebSocket.
	go func() {
		defer wg.Done()
		h.writePump(ctx, agent)
	}()

	// Read goroutine: reads messages from the WebSocket and dispatches them.
	go func() {
		defer wg.Done()
		h.readPump(ctx, agent)
	}()

	wg.Wait()

	// 6. On disconnect: remove from map, close connection.
	h.removeAgent(agent)
	agent.Streams.CloseAll()
	conn.Close(websocket.StatusNormalClosure, "disconnected")

	h.log.Info("agent disconnected",
		slog.String("cluster_id", agent.ClusterID),
		slog.String("session_id", agent.SessionID),
	)
	h.publish("cluster.disconnected", agent.ClusterID, agent.SessionID, agent.AgentVersion)
}

// readPump reads messages from the WebSocket and dispatches them.
func (h *Hub) readPump(ctx context.Context, agent *AgentConnection) {
	for {
		var msg protocol.Message
		if err := wsjson.Read(ctx, agent.Conn, &msg); err != nil {
			if ctx.Err() != nil {
				return
			}
			h.log.Error("read error",
				slog.String("cluster_id", agent.ClusterID),
				slog.String("error", err.Error()),
			)
			agent.cancel()
			return
		}
		h.handleMessage(agent, &msg)
	}
}

// writePump sends messages from the agent's sendCh to the WebSocket and
// emits server-initiated WebSocket-level pings on a steady cadence so any
// HTTP-level idle timeout in the network path (ingress, k3d serverlb, LBs)
// observes a fresh frame on the connection and never terminates it.
func (h *Hub) writePump(ctx context.Context, agent *AgentConnection) {
	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pingTicker.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, writeTimeout)
			if err := agent.Conn.Ping(pingCtx); err != nil {
				pingCancel()
				if ctx.Err() != nil {
					return
				}
				h.log.Warn("ping error",
					slog.String("cluster_id", agent.ClusterID),
					slog.String("error", err.Error()),
				)
				agent.cancel()
				return
			}
			pingCancel()
		case msg := <-agent.sendCh:
			writeCtx, writeCancel := context.WithTimeout(ctx, writeTimeout)
			if err := wsjson.Write(writeCtx, agent.Conn, msg); err != nil {
				writeCancel()
				if ctx.Err() != nil {
					return
				}
				h.log.Error("write error",
					slog.String("cluster_id", agent.ClusterID),
					slog.String("error", err.Error()),
				)
				agent.cancel()
				return
			}
			writeCancel()
		}
	}
}

// removeAgent removes an agent from the hub if it matches the current registration.
func (h *Hub) removeAgent(agent *AgentConnection) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Only remove if the registered agent is the same instance (avoids removing a replacement).
	if current, ok := h.agents[agent.ClusterID]; ok && current == agent {
		delete(h.agents, agent.ClusterID)
	}
}

// GetAgent returns the connection for a cluster, or nil if not connected.
func (h *Hub) GetAgent(clusterID string) *AgentConnection {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agents[clusterID]
}

// SendToAgent sends a message to a specific agent.
// Returns an error if the agent is not connected or the send buffer is full.
func (h *Hub) SendToAgent(clusterID string, msg *protocol.Message) error {
	h.mu.RLock()
	agent, ok := h.agents[clusterID]
	h.mu.RUnlock()

	if !ok {
		return fmt.Errorf("agent for cluster %q not connected", clusterID)
	}

	select {
	case agent.sendCh <- msg:
		return nil
	default:
		return fmt.Errorf("send buffer full for cluster %q", clusterID)
	}
}

// BroadcastToAll sends a message to all connected agents.
func (h *Hub) BroadcastToAll(msg *protocol.Message) {
	h.mu.RLock()
	agents := make([]*AgentConnection, 0, len(h.agents))
	for _, a := range h.agents {
		agents = append(agents, a)
	}
	h.mu.RUnlock()

	for _, agent := range agents {
		select {
		case agent.sendCh <- msg:
		default:
			h.log.Warn("broadcast: send buffer full, skipping",
				slog.String("cluster_id", agent.ClusterID),
			)
		}
	}
}

// ConnectedClusters returns a list of connected cluster IDs.
func (h *Hub) ConnectedClusters() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	ids := make([]string, 0, len(h.agents))
	for id := range h.agents {
		ids = append(ids, id)
	}
	return ids
}
