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
	mu     sync.RWMutex
	agents map[string]*AgentConnection // keyed by clusterID
	log    *slog.Logger
}

// NewHub creates a new Hub.
func NewHub(log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		agents: make(map[string]*AgentConnection),
		log:    log,
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

	// TODO: validate token against DB (check cluster exists and token matches).
	// For now, accept any non-empty token. This will be wired to the DB layer.

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

	// Disconnect any existing connection for this cluster.
	h.mu.Lock()
	if existing, ok := h.agents[payload.ClusterID]; ok {
		h.log.Info("replacing existing agent connection",
			slog.String("cluster_id", payload.ClusterID),
			slog.String("old_session", existing.SessionID),
		)
		existing.cancel()
		existing.Streams.CloseAll()
	}
	h.agents[payload.ClusterID] = agent
	h.mu.Unlock()

	h.log.Info("agent connected",
		slog.String("cluster_id", payload.ClusterID),
		slog.String("agent_id", payload.AgentID),
		slog.String("agent_version", payload.AgentVersion),
		slog.String("session_id", sessionID),
	)

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

// writePump sends messages from the agent's sendCh to the WebSocket.
func (h *Hub) writePump(ctx context.Context, agent *AgentConnection) {
	for {
		select {
		case <-ctx.Done():
			return
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
