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
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
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
	DBID         uuid.UUID
	Conn         *websocket.Conn
	Streams      *StreamManager
	cancel       context.CancelFunc
	sendCh       chan *protocol.Message
}

// Hub manages all connected agent tunnels.
//
// The agent map is sharded; only the hub's other
// mutable state (publisher, stateLim init) is guarded by Hub.mu. Most
// hot paths — SendToAgent, GetAgent, register/unregister — hit only
// the agent shard's lock, so SendToAgent("A") and SendToAgent("B")
// contend only if their clusterIDs hash to the same shard (1/16).
type Hub struct {
	mu        sync.RWMutex // protects publisher + stateLim + mirror only
	agents    *shardedAgents
	log       *slog.Logger
	validator AgentTokenValidator
	// publisher receives connect/disconnect/heartbeat lifecycle events for
	// fan-out to SSE subscribers. Optional; nil-safe.
	publisher LifecyclePublisher
	// regAdvancer is the wizard phase machine. Optional; when set,
	// the hub calls OnAgentConnected on first CONNECT_ACK so the
	// cluster advances `awaiting_agent` → `connected` automatically.
	regAdvancer RegistrationAdvancer
	// stateLim collapses redundant STATE_UPDATE fan-outs at the (cluster_id,
	// kind, namespace) granularity. Lazily constructed on first use so the
	// hub remains zero-value-safe.
	stateLim *stateUpdateLimiter
	// mirror routes sprint-069 MsgMirrorEvent frames into the
	// mirrored_* DB tables. Optional; nil-safe so existing tests that
	// don't wire CRD-mirror v2 stay green.
	mirror MirrorIngester
}

// MirrorIngester is the narrow handler-side interface the Hub calls
// when an agent emits a MIRROR_EVENT frame. The implementation lives in
// internal/crd (RouteMirrorEvent) and depends on *sqlc.Queries; we keep
// the surface as an interface so the tunnel package doesn't import
// internal/crd directly (which would drag controller-runtime into the
// tunnel build path).
type MirrorIngester interface {
	RouteMirrorEvent(ctx context.Context, clusterID uuid.UUID, payload protocol.MirrorEventPayload) error
}

// SetMirrorIngester attaches the sprint-069 mirror router (set once at
// startup). Nil-safe.
func (h *Hub) SetMirrorIngester(m MirrorIngester) {
	h.mu.Lock()
	h.mirror = m
	h.mu.Unlock()
}

// LifecyclePublisher is the interface the events bus implements; the tunnel
// package depends on the interface, not the bus, to keep import direction clean.
type LifecyclePublisher interface {
	Publish(eventType string, data any)
}

// RegistrationAdvancer is the slice of the registration.Service surface
// the hub needs. Declared as an interface here so the tunnel package
// doesn't pull in internal/registration as a hard dependency (which
// would import-cycle since registration depends on sqlc and could
// later depend on tunnel for a metrics hook).
type RegistrationAdvancer interface {
	OnAgentConnected(ctx context.Context, clusterID uuid.UUID, agentVersion string) error
}

// SetPublisher attaches a lifecycle publisher (set once at startup).
func (h *Hub) SetPublisher(p LifecyclePublisher) {
	h.mu.Lock()
	h.publisher = p
	h.mu.Unlock()
}

// SetRegistrationAdvancer wires the wizard phase machine. When set,
// the first heartbeat from a cluster in `awaiting_agent` advances it
// to `connected` (and on through `ready`/`provisioning` per the
// install_baseline choice).
func (h *Hub) SetRegistrationAdvancer(a RegistrationAdvancer) {
	h.mu.Lock()
	h.regAdvancer = a
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
	GetClusterAgentTokenByClusterID(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterAgentToken, error)
	GetClusterAgentTokenByToken(ctx context.Context, token string) (sqlc.ClusterAgentToken, error)
	UpsertClusterAgentToken(ctx context.Context, arg sqlc.UpsertClusterAgentTokenParams) (sqlc.ClusterAgentToken, error)
	TouchClusterAgentToken(ctx context.Context, id uuid.UUID) error
	UpdateClusterHeartbeat(ctx context.Context, arg sqlc.UpdateClusterHeartbeatParams) error
	UpsertClusterHealthStatus(ctx context.Context, arg sqlc.UpsertClusterHealthStatusParams) (sqlc.ClusterHealthStatus, error)
	CreateAgentConnection(ctx context.Context, arg sqlc.CreateAgentConnectionParams) (sqlc.AgentConnection, error)
	DisconnectActiveConnectionsByCluster(ctx context.Context, clusterID uuid.UUID) error
	UpdateAgentConnectionStatus(ctx context.Context, arg sqlc.UpdateAgentConnectionStatusParams) error
	UpdateAgentConnectionPing(ctx context.Context, id uuid.UUID) error
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
		agents:    newShardedAgents(),
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
	ackPayload := protocol.ConnectAckPayload{Accepted: true}
	if h.validator != nil {
		clusterID, err := uuid.Parse(payload.ClusterID)
		if err != nil {
			h.log.Warn("invalid cluster id in connect payload", slog.String("cluster_id", payload.ClusterID))
			conn.Close(websocket.StatusProtocolError, "invalid cluster_id")
			return
		}
		tokenKind, durableToken, err := h.validateAndMaybeRotateToken(ctx, clusterID, payload)
		if err != nil {
			h.log.Warn("agent authentication failed",
				slog.String("cluster_id", payload.ClusterID),
				slog.String("error", err.Error()),
			)
			h.publish("agent.failed", payload.ClusterID, "", payload.AgentVersion)
			conn.Close(websocket.StatusPolicyViolation, err.Error())
			return
		}
		if tokenKind == "registration" && durableToken != "" && durableToken != payload.Token {
			ackPayload.AgentToken = durableToken
		}
	}

	// 3. Generate session ID and send CONNECT_ACK.
	sessionID := fmt.Sprintf("session-%s-%d", payload.ClusterID, time.Now().UnixNano())
	ackPayload.SessionID = sessionID
	ackPayload.ServerVersion = ""
	ackBody, _ := json.Marshal(ackPayload)

	ackMsg := &protocol.Message{
		Type:    protocol.MsgConnectAck,
		Payload: ackBody,
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
	h.persistConnect(ctx, agent)

	// Disconnect any existing connection for this cluster. If we replaced one,
	// surface that as an agent.reconnecting hint so subscribers can show the
	// transition explicitly (the cluster.connected fan-out below covers the
	// happy path; this distinguishes "first connect" from "reconnect").
	wasReconnect := false
	if existing := h.agents.Set(payload.ClusterID, agent); existing != nil {
		observability.WithEvent(h.log, "agent_reconnecting").Info("replacing existing agent connection",
			slog.String("cluster_id", payload.ClusterID),
			slog.String("old_session", existing.SessionID),
		)
		existing.cancel()
		existing.Streams.CloseAll()
		wasReconnect = true
	}
	// Always count the register — first-connect and reconnect alike — so
	// the rate over a window captures "is this cluster flapping?" rather
	// than just "is anything ever happening at all".
	recordAgentReconnect(payload.ClusterID)
	if wasReconnect {
		h.publish("agent.reconnecting", payload.ClusterID, sessionID, payload.AgentVersion)
	}

	observability.WithEvent(h.log, "agent_connected").Info("agent connected",
		slog.String("cluster_id", payload.ClusterID),
		slog.String("agent_id", payload.AgentID),
		slog.String("agent_version", payload.AgentVersion),
		slog.String("session_id", sessionID),
	)
	h.publish("cluster.connected", payload.ClusterID, sessionID, payload.AgentVersion)

	// Wizard phase advance — wired by cmd/server when the registration
	// service is constructed. Best-effort: when the cluster wasn't
	// in awaiting_agent (e.g. legacy non-wizard registration) the
	// service no-ops the transition. We don't fail the connect on a
	// transition error.
	h.mu.RLock()
	advancer := h.regAdvancer
	h.mu.RUnlock()
	if advancer != nil {
		if clusterUUID, perr := uuid.Parse(payload.ClusterID); perr == nil {
			if err := advancer.OnAgentConnected(ctx, clusterUUID, payload.AgentVersion); err != nil {
				h.log.Warn("registration advance on agent_connected failed",
					slog.String("cluster_id", payload.ClusterID),
					slog.String("error", err.Error()),
				)
			}
		}
	}

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

	observability.WithEvent(h.log, "agent_disconnected").Info("agent disconnected",
		slog.String("cluster_id", agent.ClusterID),
		slog.String("session_id", agent.SessionID),
	)
	h.persistDisconnect(context.Background(), agent)
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
		recordAgentMessage(agent.ClusterID, "inbound")
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
			recordAgentMessage(agent.ClusterID, "outbound")
		}
	}
}

// removeAgent removes an agent from the hub if it matches the current registration.
func (h *Hub) removeAgent(agent *AgentConnection) {
	h.agents.DeleteIfSame(agent.ClusterID, agent)
}

// GetAgent returns the connection for a cluster, or nil if not connected.
func (h *Hub) GetAgent(clusterID string) *AgentConnection {
	return h.agents.Get(clusterID)
}

// SendToAgent sends a message to a specific agent.
// Returns an error if the agent is not connected or the send buffer is full.
func (h *Hub) SendToAgent(clusterID string, msg *protocol.Message) error {
	agent := h.agents.Get(clusterID)
	if agent == nil {
		return fmt.Errorf("agent for cluster %q not connected", clusterID)
	}

	select {
	case agent.sendCh <- msg:
		return nil
	default:
		return fmt.Errorf("send buffer full for cluster %q", clusterID)
	}
}

// Disconnect forcibly tears down the WS tunnel for a cluster. Used by the
// cluster decommission reconciler after MsgDecommission has been delivered
// and ACKed — once the server revokes the agent's registration token, any
// new dial attempt fails and any in-flight K8s proxy calls would block
// indefinitely on the now-uncredentialed agent. Calling Disconnect cancels
// the per-agent context, which cascades into both readPump and writePump
// exiting and the WebSocket being closed cleanly.
//
// No-op (returns false) when no agent is currently registered for that
// cluster ID; callers may want to log a warning in that case so operators
// know the cleanup-by-RPC path was unavailable. Idempotent — multiple
// Disconnect calls are safe; the second one just observes the entry gone
// from the map.
func (h *Hub) Disconnect(clusterID string) bool { return h.disconnectImpl(clusterID) }

// Drain gracefully closes every connected agent in parallel. Called from
// the server's Shutdown path before httpServer.Shutdown so agents
// reconnect to a surviving replica immediately, instead of waiting out
// the 20s WS-ping timeout that the previous shutdown path produced.
//
// Snapshots the agent set under the lock, then closes each agent OUTSIDE
// the lock in parallel. Closing serially under the lock would multiply
// close latency by N. Each close cancels the agent's context and shuts
// its stream manager — the agent's read loop observes the close
// immediately and re-dials, hitting the Service load balancer which
// routes to a healthy sibling pod.
//
// Returns the number of agents that were drained.
func (h *Hub) Drain() int {
	snapshot := h.agents.DrainAll()
	if len(snapshot) == 0 {
		return 0
	}
	observability.WithEvent(h.log, "hub_drain_started").Info("draining all agent connections",
		slog.Int("count", len(snapshot)),
	)
	for _, agent := range snapshot {
		go func(a *AgentConnection) {
			a.cancel()
			a.Streams.CloseAll()
		}(agent)
	}
	return len(snapshot)
}

// disconnectImpl is the unexported worker for the public Disconnect.
// Kept separate so Drain doesn't recurse into a re-lock.
func (h *Hub) disconnectImpl(clusterID string) bool {
	// Remove first so HandleWebSocket's removeAgent() at the bottom of
	// the read/write loop doesn't have to race with us.
	agent := h.agents.Delete(clusterID)
	if agent == nil {
		return false
	}
	observability.WithEvent(h.log, "agent_forced_disconnect").Info("forcibly disconnecting agent",
		slog.String("cluster_id", clusterID),
		slog.String("session_id", agent.SessionID),
	)
	agent.cancel()
	agent.Streams.CloseAll()
	return true
}

// SendDecommission sends a MsgDecommission to the agent and waits for
// MsgDecommissionAck. The returned `connected` flag is true if an agent was
// registered for the cluster at the moment of send — the cluster decommission
// reconciler uses this to distinguish "agent unreachable, skip with warning"
// from "agent reachable but bombed". `wait` bounds how long we wait for the
// ACK; on timeout we return the connected flag but a nil ack and a
// context.DeadlineExceeded error.
func (h *Hub) SendDecommission(ctx context.Context, clusterID string, payload protocol.DecommissionPayload, wait time.Duration) (*protocol.DecommissionAckPayload, bool, error) {
	agent := h.agents.Get(clusterID)
	if agent == nil {
		return nil, false, nil
	}
	streamID := uuid.NewString()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		return nil, true, fmt.Errorf("create decommission stream: %w", err)
	}
	defer agent.Streams.CloseStream(streamID)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, true, fmt.Errorf("marshal decommission payload: %w", err)
	}
	msg := &protocol.Message{
		Type:      protocol.MsgDecommission,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}
	if err := h.SendToAgent(clusterID, msg); err != nil {
		return nil, true, fmt.Errorf("send decommission: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	select {
	case data := <-stream.DataCh:
		var ack protocol.DecommissionAckPayload
		if err := json.Unmarshal(data, &ack); err != nil {
			return nil, true, fmt.Errorf("decode decommission ack: %w", err)
		}
		return &ack, true, nil
	case <-stream.DoneCh:
		return nil, true, fmt.Errorf("decommission stream closed before ack")
	case <-waitCtx.Done():
		return nil, true, waitCtx.Err()
	}
}

// BroadcastToAll sends a message to all connected agents. Snapshots
// the agent set across all shards (briefly locking each in turn) then
// sends OUTSIDE the locks — at 500 agents the previous serial-under-
// lock implementation made BroadcastToAll latency O(N * send-latency)
// and held the hub mutex the whole time.
func (h *Hub) BroadcastToAll(msg *protocol.Message) {
	agents := h.agents.Snapshot()
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
	return h.agents.ConnectedIDs()
}

func (h *Hub) persistConnect(ctx context.Context, agent *AgentConnection) {
	if h.validator == nil {
		return
	}
	clusterID, err := uuid.Parse(agent.ClusterID)
	if err != nil {
		h.log.Warn("failed to persist agent connection: invalid cluster id",
			slog.String("cluster_id", agent.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}
	if err := h.validator.DisconnectActiveConnectionsByCluster(ctx, clusterID); err != nil {
		h.log.Warn("failed to disconnect existing agent sessions",
			slog.String("cluster_id", agent.ClusterID),
			slog.String("error", err.Error()),
		)
	}
	row, err := h.validator.CreateAgentConnection(ctx, sqlc.CreateAgentConnectionParams{
		ClusterID:    clusterID,
		AgentID:      agent.AgentID,
		SessionID:    agent.SessionID,
		Status:       "connected",
		ChannelName:  "",
		PodName:      "",
		NodeName:     "",
		AgentVersion: agent.AgentVersion,
	})
	if err != nil {
		h.log.Warn("failed to persist agent connection",
			slog.String("cluster_id", agent.ClusterID),
			slog.String("session_id", agent.SessionID),
			slog.String("error", err.Error()),
		)
		return
	}
	agent.DBID = row.ID
}

func (h *Hub) persistDisconnect(ctx context.Context, agent *AgentConnection) {
	if h.validator == nil || agent.DBID == uuid.Nil {
		return
	}
	disconnectedAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	if err := h.validator.UpdateAgentConnectionStatus(ctx, sqlc.UpdateAgentConnectionStatusParams{
		ID:             agent.DBID,
		Status:         "disconnected",
		DisconnectedAt: disconnectedAt,
	}); err != nil {
		h.log.Warn("failed to persist agent disconnect",
			slog.String("cluster_id", agent.ClusterID),
			slog.String("session_id", agent.SessionID),
			slog.String("error", err.Error()),
		)
	}
}

func (h *Hub) persistPing(agent *AgentConnection) {
	if h.validator == nil || agent.DBID == uuid.Nil {
		return
	}
	if err := h.validator.UpdateAgentConnectionPing(context.Background(), agent.DBID); err != nil {
		h.log.Warn("failed to persist agent ping",
			slog.String("cluster_id", agent.ClusterID),
			slog.String("session_id", agent.SessionID),
			slog.String("error", err.Error()),
		)
	}
}

func (h *Hub) validateAndMaybeRotateToken(ctx context.Context, clusterID uuid.UUID, payload protocol.ConnectPayload) (string, string, error) {
	registrationToken, regErr := h.validator.GetRegistrationTokenByToken(ctx, payload.Token)
	if regErr == nil {
		if registrationToken.ClusterID != clusterID {
			return "", "", fmt.Errorf("registration token does not match cluster")
		}
		if err := h.validator.MarkRegistrationTokenUsed(ctx, registrationToken.ID); err != nil {
			return "", "", fmt.Errorf("failed to mark token used")
		}
		durable, err := h.ensureClusterAgentToken(ctx, clusterID)
		if err != nil {
			return "", "", fmt.Errorf("failed to issue agent token")
		}
		return "registration", durable, nil
	}

	agentToken, agentErr := h.validator.GetClusterAgentTokenByToken(ctx, payload.Token)
	if agentErr == nil {
		if agentToken.ClusterID != clusterID {
			return "", "", fmt.Errorf("agent token does not match cluster")
		}
		if err := h.validator.TouchClusterAgentToken(ctx, agentToken.ID); err != nil {
			h.log.Warn("failed to touch cluster agent token",
				slog.String("cluster_id", payload.ClusterID),
				slog.String("error", err.Error()),
			)
		}
		return "agent", agentToken.Token, nil
	}

	return "", "", fmt.Errorf("invalid registration token")
}

func (h *Hub) ensureClusterAgentToken(ctx context.Context, clusterID uuid.UUID) (string, error) {
	if tok, err := h.validator.GetClusterAgentTokenByClusterID(ctx, clusterID); err == nil && tok.Token != "" {
		if err := h.validator.TouchClusterAgentToken(ctx, tok.ID); err != nil {
			h.log.Warn("failed to touch existing cluster agent token",
				slog.String("cluster_id", clusterID.String()),
				slog.String("error", err.Error()),
			)
		}
		return tok.Token, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.URLEncoding.EncodeToString(b)
	row, err := h.validator.UpsertClusterAgentToken(ctx, sqlc.UpsertClusterAgentTokenParams{
		ClusterID: clusterID,
		Token:     token,
	})
	if err != nil {
		return "", err
	}
	return row.Token, nil
}
