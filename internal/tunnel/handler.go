package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// stateUpdateMinInterval is the minimum gap between two cluster.k8s_changed
// fan-outs for the same (cluster, kind, namespace) tuple. The agent already
// rate-limits at 1s per (kind, ns, name); this server-side limiter is a
// belt-and-suspenders against multiple agents misbehaving and against a
// flood of distinct names within a single namespace (e.g. ReplicaSet rolling
// every Pod under one Deployment).
const stateUpdateMinInterval = 500 * time.Millisecond

// handleMessage dispatches incoming messages from an agent by type.
func (h *Hub) handleMessage(conn *AgentConnection, msg *protocol.Message) {
	h.log.Debug("handler received message", slog.String("type", string(msg.Type)), slog.String("cluster_id", conn.ClusterID))
	switch msg.Type {
	case protocol.MsgPong:
		h.handlePong(conn, msg)

	case protocol.MsgHeartbeat:
		h.handleHeartbeat(conn, msg)

	case protocol.MsgK8sResponse:
		h.routeToStream(conn, msg)

	case protocol.MsgK8sStreamFrame:
		h.routeToStream(conn, msg)

	case protocol.MsgHelmResult:
		h.routeToStream(conn, msg)

	case protocol.MsgExecOutput, protocol.MsgExecEnd:
		h.routeToStream(conn, msg)

	case protocol.MsgLogData, protocol.MsgLogEnd:
		h.routeToStream(conn, msg)

	case protocol.MsgHealthResult:
		h.routeToStream(conn, msg)

	case protocol.MsgRBACSyncResult:
		h.routeToStream(conn, msg)

	case protocol.MsgStateUpdate:
		h.handleStateUpdate(conn, msg)

	case protocol.MsgError:
		h.handleError(conn, msg)

	default:
		h.log.Warn("unknown message type",
			slog.String("type", string(msg.Type)),
			slog.String("cluster_id", conn.ClusterID),
		)
	}
}

// handlePong processes PONG responses from agents.
func (h *Hub) handlePong(conn *AgentConnection, _ *protocol.Message) {
	h.log.Debug("pong received", slog.String("cluster_id", conn.ClusterID))
	// In a full implementation, this would update last_ping in the database.
}

// handleHeartbeat processes HEARTBEAT messages from agents.
func (h *Hub) handleHeartbeat(conn *AgentConnection, msg *protocol.Message) {
	h.log.Debug("heartbeat received",
		slog.String("cluster_id", conn.ClusterID),
		slog.Int("payload_len", len(msg.Payload)),
	)
	if h.validator == nil {
		return
	}
	clusterID, err := uuid.Parse(conn.ClusterID)
	if err != nil {
		h.log.Warn("invalid cluster id on heartbeat", slog.String("cluster_id", conn.ClusterID))
		return
	}
	var payload protocol.HeartbeatPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.log.Warn("invalid heartbeat payload", slog.String("error", err.Error()))
		return
	}
	if err := h.validator.UpdateClusterHeartbeat(context.Background(), sqlc.UpdateClusterHeartbeatParams{
		ID:                clusterID,
		AgentVersion:      payload.AgentVersion,
		KubernetesVersion: payload.KubernetesVersion,
		NodeCount:         int32(payload.NodeCount),
		Distribution:      payload.Distribution,
	}); err != nil {
		h.log.Warn("failed to update cluster heartbeat", slog.String("error", err.Error()))
	}
	conditions, _ := json.Marshal(map[string]any{
		"connected": true,
		"source":    "agent-heartbeat",
	})
	if _, err := h.validator.UpsertClusterHealthStatus(context.Background(), sqlc.UpsertClusterHealthStatusParams{
		ClusterID:          clusterID,
		CpuUsagePercent:    payload.CPUUsagePercent,
		MemoryUsagePercent: payload.MemoryUsagePercent,
		PodCount:           int32(payload.PodCount),
		NodeCount:          int32(payload.NodeCount),
		Conditions:         conditions,
	}); err != nil {
		h.log.Warn("failed to upsert cluster health from heartbeat", slog.String("error", err.Error()))
	}

	// Fan out a heartbeat tick so SSE subscribers can flip "Last heartbeat"
	// timestamps and pulse status indicators without polling.
	h.publishHeartbeat(conn.ClusterID, payload)
}

// publishHeartbeat emits a cluster.heartbeat event to any attached publisher.
// Kept separate from the per-event publish helper in server.go because the
// heartbeat payload includes lightweight liveness numbers (cpu/mem/pods) the
// dashboard wants to surface immediately rather than waiting on the next
// metrics tick.
func (h *Hub) publishHeartbeat(clusterID string, payload protocol.HeartbeatPayload) {
	h.mu.RLock()
	p := h.publisher
	h.mu.RUnlock()
	if p == nil {
		return
	}
	p.Publish("cluster.heartbeat", map[string]any{
		"cluster_id":           clusterID,
		"last_heartbeat":       time.Now().UTC().Format(time.RFC3339),
		"agent_version":        payload.AgentVersion,
		"kubernetes_version":   payload.KubernetesVersion,
		"node_count":           payload.NodeCount,
		"pod_count":            payload.PodCount,
		"cpu_usage_percent":    payload.CPUUsagePercent,
		"memory_usage_percent": payload.MemoryUsagePercent,
		"distribution":         payload.Distribution,
	})
}

// routeToStream routes a message to the appropriate waiting stream.
func (h *Hub) routeToStream(conn *AgentConnection, msg *protocol.Message) {
	streamID := msg.StreamID
	if streamID == "" {
		streamID = msg.RequestID
	}
	if streamID == "" {
		h.log.Warn("message has no stream_id or request_id, cannot route",
			slog.String("type", string(msg.Type)),
			slog.String("cluster_id", conn.ClusterID),
		)
		return
	}

	stream, ok := conn.Streams.GetStream(streamID)
	if !ok {
		h.log.Warn("no stream found for message",
			slog.String("type", string(msg.Type)),
			slog.String("stream_id", streamID),
			slog.String("cluster_id", conn.ClusterID),
		)
		return
	}

	// Non-blocking send to avoid blocking the read loop.
	select {
	case stream.DataCh <- msg.Payload:
	default:
		h.log.Warn("stream data channel full, dropping message",
			slog.String("stream_id", streamID),
			slog.String("cluster_id", conn.ClusterID),
		)
	}
}

// handleStateUpdate translates a STATE_UPDATE from the agent into a
// `cluster.k8s_changed` SSE event. The server applies its own per-(cluster,
// kind, namespace) rate limiter on top of the agent's per-name limiter so a
// well-formed agent emitting a thousand distinct Pod updates inside a
// Deployment rollout still results in at most ~2 SSE events per second per
// namespace — the dashboard only needs an invalidation hint, not a fire-hose.
func (h *Hub) handleStateUpdate(conn *AgentConnection, msg *protocol.Message) {
	var payload protocol.StateUpdatePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.log.Warn("invalid STATE_UPDATE payload",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}
	h.log.Debug("received MsgStateUpdate",
		slog.String("cluster_id", conn.ClusterID),
		slog.String("kind", payload.Kind),
		slog.String("namespace", payload.Namespace),
		slog.String("name", payload.Name),
	)

	limiter := h.stateLimiter()
	key := fmt.Sprintf("%s|%s|%s", conn.ClusterID, payload.Kind, payload.Namespace)
	if !limiter.allow(key) {
		h.log.Debug("MsgStateUpdate rate-limited", slog.String("key", key))
		return
	}

	h.mu.RLock()
	p := h.publisher
	h.mu.RUnlock()
	if p == nil {
		h.log.Warn("MsgStateUpdate received but no publisher set")
		return
	}
	h.log.Debug("publishing cluster.k8s_changed",
		slog.String("cluster_id", conn.ClusterID),
		slog.String("kind", payload.Kind),
	)
	p.Publish("cluster.k8s_changed", map[string]any{
		"cluster_id":       conn.ClusterID,
		"op":               string(payload.Op),
		"kind":             payload.Kind,
		"api_group":        payload.APIGroup,
		"api_version":      payload.APIVersion,
		"namespace":        payload.Namespace,
		"name":             payload.Name,
		"resource_version": payload.ResourceVersion,
	})
}

// stateLimiter lazily initializes (under the hub mutex) and returns the
// shared per-(cluster, kind, namespace) rate limiter for state-update
// fan-out. Lazy init keeps the hub zero-value safe for tests that don't
// route any STATE_UPDATEs.
func (h *Hub) stateLimiter() *stateUpdateLimiter {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stateLim == nil {
		h.stateLim = newStateUpdateLimiter(stateUpdateMinInterval)
	}
	return h.stateLim
}

// stateUpdateLimiter is a minimal per-key rate limiter shared across the hub.
// It mirrors the agent-side data structure (map + mutex), but uses a tighter
// interval because the server is downstream of the agent's already-coalesced
// stream and only needs to soak edge cases.
type stateUpdateLimiter struct {
	mu          sync.Mutex
	last        map[string]time.Time
	minInterval time.Duration
	now         func() time.Time
}

func newStateUpdateLimiter(minInterval time.Duration) *stateUpdateLimiter {
	return &stateUpdateLimiter{
		last:        make(map[string]time.Time),
		minInterval: minInterval,
		now:         time.Now,
	}
}

// allow gates an emit on a fresh key. We don't bother with eviction here:
// the key cardinality is bounded by (#clusters x #kinds x #namespaces),
// which is small in practice (thousands at most), and the entries are
// 24-byte strings + a time.Time. A long-lived server can afford that.
func (r *stateUpdateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if prev, ok := r.last[key]; ok && now.Sub(prev) < r.minInterval {
		return false
	}
	r.last[key] = now
	return true
}

// handleError processes ERROR messages from agents.
func (h *Hub) handleError(conn *AgentConnection, msg *protocol.Message) {
	h.log.Error("agent reported error",
		slog.String("cluster_id", conn.ClusterID),
		slog.String("stream_id", msg.StreamID),
	)

	// Route to stream if stream_id or request_id is present so the caller gets the error.
	if msg.StreamID != "" || msg.RequestID != "" {
		h.routeToStream(conn, msg)
	}
}
