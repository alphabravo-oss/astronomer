package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/agentlifecycle"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// stateUpdateMinInterval is the minimum gap between two cluster.k8s_changed
// fan-outs for the same coalescing key. The agent already rate-limits at 1s
// per object name and may supply a narrower payload.CoalesceKey; this
// server-side limiter is a belt-and-suspenders against multiple agents
// misbehaving and against flood bursts that would otherwise spam SSE clients.
const stateUpdateMinInterval = 500 * time.Millisecond

// handleMessage dispatches incoming messages from an agent by type.
func (h *Hub) handleMessage(conn *AgentConnection, msg *protocol.Message) {
	h.log.Debug("handler received message", slog.String("type", string(msg.Type)), slog.String("cluster_id", conn.ClusterID))
	switch msg.Type {
	case protocol.MsgPong:
		h.handlePong(conn, msg)

	case protocol.MsgHeartbeat:
		h.handleHeartbeat(conn, msg)

	case protocol.MsgMetrics:
		h.handleMetrics(conn, msg)

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

	case protocol.MsgDecommissionAck:
		// Cluster decommission ACK: response to a server-initiated
		// MsgDecommission. Routed back to the per-call stream the
		// decommission reconciler set up before sending the request.
		h.routeToStream(conn, msg)

	case protocol.MsgAgentUpgradeResult:
		h.handleAgentUpgradeResult(conn, msg)

	case protocol.MsgStateUpdate:
		h.handleStateUpdate(conn, msg)

	case protocol.MsgMirrorEvent:
		h.handleMirrorEvent(conn, msg)

	case protocol.MsgApiserverAudit:
		h.handleApiserverAudit(conn, msg)

	case protocol.MsgDeliveryStateRequest:
		h.handleDeliveryStateRequest(conn, msg)

	case protocol.MsgDeliveryStatus:
		h.handleDeliveryStatus(conn, msg)

	case protocol.MsgError:
		h.handleError(conn, msg)

	default:
		h.log.Warn("unknown message type",
			slog.String("type", string(msg.Type)),
			slog.String("cluster_id", conn.ClusterID),
		)
	}
}

func (h *Hub) handleDeliveryStateRequest(conn *AgentConnection, msg *protocol.Message) {
	provider := h.deliveryStateProvider()
	if provider == nil {
		h.sendDeliveryStateError(conn, msg, "delivery_state_unavailable")
		return
	}
	clusterID, err := uuid.Parse(conn.ClusterID)
	if err != nil {
		h.log.Error("authenticated tunnel has invalid cluster ID", slog.String("cluster_id", conn.ClusterID))
		h.sendDeliveryStateError(conn, msg, "invalid_authenticated_cluster")
		return
	}
	var request protocol.DeliveryStateRequestV2
	decoder := json.NewDecoder(strings.NewReader(string(msg.Payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.log.Warn("invalid delivery state request", slog.String("cluster_id", conn.ClusterID), slog.String("error", err.Error()))
		h.sendDeliveryStateError(conn, msg, "invalid_delivery_state_request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.sendDeliveryStateError(conn, msg, "invalid_delivery_state_request")
		return
	}
	response, err := provider.Snapshot(context.Background(), clusterID, request)
	if err != nil {
		h.log.Warn("delivery state provider failed", slog.String("cluster_id", conn.ClusterID), slog.String("error", err.Error()))
		h.sendDeliveryStateError(conn, msg, "delivery_state_unavailable")
		return
	}
	body, err := json.Marshal(response)
	if err != nil {
		h.log.Error("marshal delivery state response", slog.String("cluster_id", conn.ClusterID), slog.String("error", err.Error()))
		h.sendDeliveryStateError(conn, msg, "delivery_state_unavailable")
		return
	}
	if err := h.SendToAgent(conn.ClusterID, &protocol.Message{
		Type: protocol.MsgDeliveryStateResponse, StreamID: msg.StreamID, RequestID: msg.RequestID,
		ClusterID: conn.ClusterID, Timestamp: time.Now().UTC(), Payload: body,
	}); err != nil {
		h.log.Warn("send delivery state response failed", slog.String("cluster_id", conn.ClusterID), slog.String("error", err.Error()))
	}
}

func (h *Hub) sendDeliveryStateError(conn *AgentConnection, msg *protocol.Message, code string) {
	_ = h.SendToAgent(conn.ClusterID, &protocol.Message{
		Type: protocol.MsgDeliveryStateResponse, StreamID: msg.StreamID, RequestID: msg.RequestID,
		ClusterID: conn.ClusterID, Timestamp: time.Now().UTC(), Error: code,
	})
}

func (h *Hub) handleDeliveryStatus(conn *AgentConnection, msg *protocol.Message) {
	sink := h.deliveryStatusSink()
	if sink == nil {
		h.log.Warn("delivery status dropped because no sink is configured", slog.String("cluster_id", conn.ClusterID))
		return
	}
	clusterID, err := uuid.Parse(conn.ClusterID)
	if err != nil || conn.DBID == uuid.Nil || conn.SessionID == "" {
		h.log.Error("delivery status arrived on an invalid authenticated session", slog.String("cluster_id", conn.ClusterID))
		return
	}
	var payload protocol.DeliveryStatusV2
	decoder := json.NewDecoder(strings.NewReader(string(msg.Payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		h.log.Warn("invalid delivery status", slog.String("cluster_id", conn.ClusterID), slog.String("error", err.Error()))
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.log.Warn("delivery status contains trailing JSON", slog.String("cluster_id", conn.ClusterID))
		return
	}
	if err := sink.Ingest(context.Background(), clusterID, conn.DBID, conn.SessionID, payload); err != nil {
		h.log.Warn("delivery status persistence failed", slog.String("cluster_id", conn.ClusterID), slog.String("error", err.Error()))
	}
}

// handlePong processes PONG responses from agents.
func (h *Hub) handlePong(conn *AgentConnection, _ *protocol.Message) {
	h.log.Debug("pong received", slog.String("cluster_id", conn.ClusterID))
	h.persistPing(conn)
}

// handleHeartbeat processes HEARTBEAT messages from agents.
func (h *Hub) handleHeartbeat(conn *AgentConnection, msg *protocol.Message) {
	h.log.Debug("heartbeat received",
		slog.String("cluster_id", conn.ClusterID),
		slog.Int("payload_len", len(msg.Payload)),
	)
	h.persistPing(conn)
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
		"connected":                 true,
		"degraded":                  len(payload.DegradedReasons) > 0,
		"source":                    "agent-heartbeat",
		"heartbeat_schema_version":  payload.SchemaVersion,
		"agent_build_sha":           payload.AgentBuildSHA,
		"privilege_profile":         payload.PrivilegeProfile,
		"available_apis":            payload.AvailableAPIs,
		"enabled_features":          payload.EnabledFeatures,
		"denied_features":           payload.DeniedFeatures,
		"last_successful_action":    payload.LastSuccessfulAction,
		"last_successful_action_at": payload.LastSuccessfulActionAt,
		"degraded_reasons":          payload.DegradedReasons,
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

	h.reconcileAgentLifecycle(conn, clusterID, payload)

	// Fan out a heartbeat tick so SSE subscribers can flip "Last heartbeat"
	// timestamps and pulse status indicators without polling.
	h.publishHeartbeat(conn.ClusterID, payload)
}

func (h *Hub) reconcileAgentLifecycle(conn *AgentConnection, clusterID uuid.UUID, payload protocol.HeartbeatPayload) {
	if payload.AgentVersion != "" {
		affected, err := h.validator.MarkRunningAgentUpgradeSucceededByVersion(context.Background(), sqlc.MarkRunningAgentUpgradeSucceededByVersionParams{
			ClusterID:     clusterID,
			TargetVersion: payload.AgentVersion,
		})
		if err != nil {
			h.log.Warn("failed to reconcile agent upgrade version",
				slog.String("cluster_id", conn.ClusterID),
				slog.String("agent_version", payload.AgentVersion),
				slog.String("error", err.Error()),
			)
		} else if affected > 0 {
			h.log.Info("agent upgrade confirmed by heartbeat",
				slog.String("cluster_id", conn.ClusterID),
				slog.String("agent_version", payload.AgentVersion),
				slog.Int64("operations", affected),
			)
		}
	}

	op, err := h.validator.ClaimPendingAgentLifecycleOperation(context.Background(), clusterID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			h.log.Warn("failed to claim pending agent lifecycle operation",
				slog.String("cluster_id", conn.ClusterID),
				slog.String("error", err.Error()),
			)
		}
		return
	}
	h.dispatchAgentLifecycleOperation(conn, op)
}

func (h *Hub) dispatchAgentLifecycleOperation(conn *AgentConnection, op sqlc.AgentLifecycleOperation) {
	switch op.OperationType {
	case agentlifecycle.OperationTypeUpgrade:
		payload := protocol.AgentUpgradePayload{
			OperationID:   op.ID.String(),
			ClusterID:     conn.ClusterID,
			TargetVersion: op.TargetVersion,
			TargetImage:   op.TargetImage,
			// The plan's rollback image, persisted in operation_spec when the
			// operation was queued. Empty is fine and common: the agent then
			// falls back to the image it is currently running, which is the
			// only one it can prove is pullable.
			RollbackImage: agentUpgradeRollbackImage(op.OperationSpec),
		}
		body, err := json.Marshal(payload)
		if err != nil {
			h.completeAgentLifecycleOperation(op.ID, agentlifecycle.StatusFailed, "failed to encode agent upgrade payload: "+err.Error())
			return
		}
		msg := &protocol.Message{
			Type:      protocol.MsgAgentUpgrade,
			ClusterID: conn.ClusterID,
			Timestamp: time.Now().UTC(),
			Payload:   body,
		}
		if err := h.SendToAgent(conn.ClusterID, msg); err != nil {
			h.completeAgentLifecycleOperation(op.ID, agentlifecycle.StatusFailed, "failed to send agent upgrade command: "+err.Error())
			return
		}
		h.log.Info("agent upgrade command sent",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("operation_id", op.ID.String()),
			slog.String("target_version", op.TargetVersion),
			slog.String("target_image", op.TargetImage),
		)
	default:
		h.completeAgentLifecycleOperation(op.ID, agentlifecycle.StatusFailed, "unsupported agent lifecycle operation type: "+op.OperationType)
	}
}

// agentUpgradeRollbackImage pulls the rollback image out of the operation spec
// persisted at queue time (internal/handler/cluster_agents.go writes the whole
// upgrade plan under "plan"). It is best-effort: a spec that is absent, invalid
// or from an older schema yields "", and the agent falls back to the image it
// is currently running.
func agentUpgradeRollbackImage(spec json.RawMessage) string {
	if len(spec) == 0 {
		return ""
	}
	var envelope struct {
		Plan struct {
			RollbackImage string `json:"rollback_image"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(spec, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Plan.RollbackImage)
}

func (h *Hub) handleAgentUpgradeResult(conn *AgentConnection, msg *protocol.Message) {
	if h.validator == nil {
		return
	}
	var payload protocol.AgentUpgradeResultPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.log.Warn("invalid AGENT_UPGRADE_RESULT payload",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}
	if payload.ClusterID != "" && payload.ClusterID != conn.ClusterID {
		h.log.Warn("agent upgrade result cluster mismatch",
			slog.String("connection_cluster_id", conn.ClusterID),
			slog.String("payload_cluster_id", payload.ClusterID),
		)
		return
	}
	operationID, err := uuid.Parse(payload.OperationID)
	if err != nil {
		h.log.Warn("agent upgrade result has invalid operation id",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("operation_id", payload.OperationID),
		)
		return
	}
	// A patch ack is NOT a successful upgrade. An agent that reports
	// rollout_started has only committed the Deployment change; with strategy
	// Recreate it is about to be terminated by its own rollout and cannot know
	// whether the replacement comes back. The operation stays `running` and the
	// success edge is MarkRunningAgentUpgradeSucceededByVersion above — the
	// replacement agent reconnecting and heartbeating the target version. A
	// failure arrives either as an explicit rolled_back result from the
	// rolled-back agent, or from the stuck-operation sweeper.
	if payload.Phase == protocol.AgentUpgradePhaseRolloutStarted {
		h.log.Info("agent upgrade rollout started; awaiting replacement agent",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("operation_id", payload.OperationID),
			slog.String("observed_image", payload.ObservedImage),
			slog.String("rollback_image", payload.RollbackImage),
		)
		return
	}
	if payload.Success {
		// Two callers land here.
		//
		// Legacy: agents predating the self-upgrade hardening send no Phase and
		// have no watchdog, so their ack is the only signal that will ever
		// arrive. Keeping today's behavior for them is deliberate — the
		// alternative would leave every already-deployed agent's upgrade stuck
		// in `running` until the sweeper failed it.
		//
		// Hardened: Phase="succeeded" is the watchdog's own verdict, relayed by
		// the replacement agent keyed on OPERATION ID. It is redundant with
		// MarkRunningAgentUpgradeSucceededByVersion above and idempotent with
		// it, and it is what keeps success from hinging entirely on the
		// heartbeat's version string matching the operation's target_version.
		h.completeAgentLifecycleOperation(operationID, agentlifecycle.StatusSucceeded, "")
		h.log.Info("agent upgrade command completed",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("operation_id", payload.OperationID),
			slog.String("observed_image", payload.ObservedImage),
		)
		return
	}
	lastError := payload.Error
	if lastError == "" {
		lastError = payload.Message
	}
	if lastError == "" {
		lastError = "agent upgrade command failed"
	}
	h.completeAgentLifecycleOperation(operationID, agentlifecycle.StatusFailed, lastError)
	h.log.Warn("agent upgrade command failed",
		slog.String("cluster_id", conn.ClusterID),
		slog.String("operation_id", payload.OperationID),
		slog.String("error", lastError),
	)
}

func (h *Hub) completeAgentLifecycleOperation(id uuid.UUID, status, lastError string) {
	if h.validator == nil {
		return
	}
	if _, err := h.validator.CompleteAgentLifecycleOperation(context.Background(), sqlc.CompleteAgentLifecycleOperationParams{
		ID:        id,
		Status:    status,
		LastError: lastError,
	}); err != nil {
		h.log.Warn("failed to update agent lifecycle operation",
			slog.String("operation_id", id.String()),
			slog.String("status", status),
			slog.String("error", err.Error()),
		)
	}
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
		"cluster_id":                clusterID,
		"last_heartbeat":            time.Now().UTC().Format(time.RFC3339),
		"agent_version":             payload.AgentVersion,
		"agent_build_sha":           payload.AgentBuildSHA,
		"heartbeat_schema_version":  payload.SchemaVersion,
		"kubernetes_version":        payload.KubernetesVersion,
		"node_count":                payload.NodeCount,
		"pod_count":                 payload.PodCount,
		"cpu_usage_percent":         payload.CPUUsagePercent,
		"memory_usage_percent":      payload.MemoryUsagePercent,
		"distribution":              payload.Distribution,
		"privilege_profile":         payload.PrivilegeProfile,
		"available_apis":            payload.AvailableAPIs,
		"enabled_features":          payload.EnabledFeatures,
		"denied_features":           payload.DeniedFeatures,
		"last_successful_action":    payload.LastSuccessfulAction,
		"last_successful_action_at": payload.LastSuccessfulActionAt,
		"degraded_reasons":          payload.DegradedReasons,
	})
}

// handleMetrics processes METRICS messages from agents. Unlike HEARTBEAT,
// these frames carry the richer node/namespace snapshot emitted on the slower
// metrics ticker. We persist the aggregate health snapshot and fan out an
// immediate cluster.metrics event so subscribers do not have to wait for the
// background publisher loop to notice the change.
func (h *Hub) handleMetrics(conn *AgentConnection, msg *protocol.Message) {
	if h.validator == nil {
		return
	}
	clusterID, err := uuid.Parse(conn.ClusterID)
	if err != nil {
		h.log.Warn("invalid cluster id on metrics", slog.String("cluster_id", conn.ClusterID))
		return
	}
	var payload protocol.MetricsPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.log.Warn("invalid metrics payload",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}

	conditions, _ := json.Marshal(map[string]any{
		"connected":         true,
		"source":            "agent-metrics",
		"timestamp":         payload.Timestamp,
		"metrics_available": payload.MetricsAvailable,
	})
	if _, err := h.validator.UpsertClusterHealthStatus(context.Background(), sqlc.UpsertClusterHealthStatusParams{
		ClusterID:          clusterID,
		CpuUsagePercent:    payload.ClusterCPUUsage,
		MemoryUsagePercent: payload.ClusterMemoryUsage,
		PodCount:           int32(payload.ClusterPodCount),
		NodeCount:          int32(payload.ClusterNodeCount),
		Conditions:         conditions,
	}); err != nil {
		h.log.Warn("failed to upsert cluster health from metrics", slog.String("error", err.Error()))
	}

	// C3 / M13: stamp last_metrics_at ONLY when the agent actually returned a
	// metrics sample (MetricsAvailable=true). A frozen stream sends no frame, so
	// last_metrics_at simply ages; a cluster with no metrics-server sends
	// MetricsAvailable=false forever, so it never gets stamped (stays NULL). The
	// periodic worker sweep reads this column to tell "MetricsStale" apart from
	// "NoMetricsServer". The heartbeat handler and worker sweep never touch it.
	if payload.MetricsAvailable {
		if err := h.validator.TouchClusterMetricsSample(context.Background(), clusterID); err != nil {
			h.log.Warn("failed to touch cluster metrics sample", slog.String("error", err.Error()))
		}
	}

	h.mu.RLock()
	p := h.publisher
	h.mu.RUnlock()
	if p == nil {
		return
	}
	p.Publish("cluster.metrics", map[string]any{
		"cluster_id":        conn.ClusterID,
		"cpu_percentage":    payload.ClusterCPUUsage,
		"memory_percentage": payload.ClusterMemoryUsage,
		"pod_count":         payload.ClusterPodCount,
		"node_count":        payload.ClusterNodeCount,
		"timestamp":         payload.Timestamp,
		"metrics_available": payload.MetricsAvailable,
		"nodes":             payload.Nodes,
		"namespaces":        payload.Namespaces,
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

	// If the stream was already closed due to an earlier overflow (but not yet
	// removed from the map by the consumer's deferred CloseStream), drop this
	// frame WITHOUT delivering it. Delivering a post-overflow frame — most
	// importantly a trailing End frame — would let the unary reassembler
	// concatenate a gap-riddled body and return it as HTTP 200. Refusing to
	// deliver anything after the gap guarantees the reassembler only ever sees
	// a gap-free prefix followed by the closed DoneCh, which it surfaces as an
	// error (-> 502) instead of a silently truncated success.
	if stream.IsClosed() {
		observability.RecordDroppedEvent("tunnel_stream_route", "stream_closed")
		return
	}

	// Non-blocking send to avoid blocking the read loop. A full channel means
	// the consumer fell behind a burst; we must NOT silently drop the frame.
	// For a chunked unary response a dropped middle chunk yields a truncated
	// body returned as 200 with no error; for a watch it silently loses a
	// MODIFIED/DELETED event (stale UI or delivery state). Instead we
	// close the stream so loss is surfaced: the unary reassembler turns the
	// closed DoneCh into a 502, and watch/exec/log consumers return so the
	// client reconnects and re-lists rather than missing events.
	select {
	case stream.DataCh <- msg.Payload:
	default:
		observability.RecordDroppedEvent("tunnel_stream_route", "channel_full")
		h.log.Warn("stream data channel full, closing stream to surface loss",
			slog.String("type", string(msg.Type)),
			slog.String("stream_id", streamID),
			slog.String("cluster_id", conn.ClusterID),
		)
		stream.Close()
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
		tunnelStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("invalid", "unknown")...).Inc()
		h.log.Warn("invalid STATE_UPDATE payload",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}
	tunnelStateUpdatesReceivedTotal.WithLabelValues(observability.MetricValues(payload.Kind)...).Inc()
	h.log.Debug("received MsgStateUpdate",
		slog.String("cluster_id", conn.ClusterID),
		slog.String("kind", payload.Kind),
		slog.String("namespace", payload.Namespace),
		slog.String("name", payload.Name),
	)

	limiter := h.stateLimiter()
	key := fmt.Sprintf("%s|%s", conn.ClusterID, stateUpdateKey(payload))
	if !limiter.allow(key) {
		tunnelStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("rate_limited", payload.Kind)...).Inc()
		h.log.Debug("MsgStateUpdate rate-limited", slog.String("key", key))
		return
	}

	h.mu.RLock()
	p := h.publisher
	h.mu.RUnlock()
	if p == nil {
		tunnelStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("no_publisher", payload.Kind)...).Inc()
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
	tunnelStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("published", payload.Kind)...).Inc()
}

func stateUpdateKey(payload protocol.StateUpdatePayload) string {
	if payload.CoalesceKey != "" {
		return payload.CoalesceKey
	}
	return fmt.Sprintf("%s|%s|%s", payload.Kind, payload.Namespace, payload.Name)
}

// stateLimiter lazily initializes (under the hub mutex) and returns the
// shared per-(cluster, kind, namespace) rate limiter for state-update
// fan-out. Lazy init keeps the hub zero-value safe for tests that don't
// route any STATE_UPDATEs.
//
// Double-checked locking: the common case (limiter already built) reads
// h.stateLim under the shared RLock so a fleet-scale STATE_UPDATE flood
// no longer serializes every state frame — and blocks concurrent RLock
// readers (publishHeartbeat, handleMetrics, publish) — on the hub-wide
// EXCLUSIVE write lock. Only the first call per hub takes the write lock
// to construct the limiter.
func (h *Hub) stateLimiter() *stateUpdateLimiter {
	h.mu.RLock()
	lim := h.stateLim
	h.mu.RUnlock()
	if lim != nil {
		return lim
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Re-check under the write lock: a racing caller may have constructed
	// it between our RUnlock and Lock.
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
	// calls counts allow() invocations so the O(N) eviction sweep can be
	// amortized to once per evictSampleEvery calls rather than running on
	// every call once the map exceeds the size threshold.
	calls uint64
}

func newStateUpdateLimiter(minInterval time.Duration) *stateUpdateLimiter {
	return &stateUpdateLimiter{
		last:        make(map[string]time.Time),
		minInterval: minInterval,
		now:         time.Now,
	}
}

// allow gates an emit on a fresh key. Cluster/kind/namespace tuples
// are usually a small bounded set, but ephemeral namespaces (CI,
// preview envs) and short-lived custom resources can churn — without
// eviction, long-lived servers grow this map without bound. evictIfDue
// runs inline on every Nth call to amortize the cost.
func (r *stateUpdateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.evictLocked(now)
	if prev, ok := r.last[key]; ok && now.Sub(prev) < r.minInterval {
		return false
	}
	r.last[key] = now
	return true
}

// stateLimiterEvictAfter is how long an unused key is retained.
// 100x the minInterval — long enough that an item recently emitted
// gets at least one rate-limited follow-up before its entry decays.
const stateLimiterEvictAfter = 60 * time.Second

// evictSampleEvery is how often (in allow() calls) the O(N) eviction
// sweep is permitted to run. Amortizes the sweep so a churny key set
// that keeps the map above the size threshold does not pay a full map
// scan on every single state frame.
const evictSampleEvery = 256

// evictLocked runs lazily inside Allow; not a separate goroutine so
// tests don't have to gate on a background tick. mu is already held.
func (r *stateUpdateLimiter) evictLocked(now time.Time) {
	// Amortize the O(N) sweep to once per evictSampleEvery calls. A pure
	// len(r.last) threshold made the sweep run on EVERY call whenever the
	// map stayed above the threshold (churny ephemeral namespaces / short-
	// lived CRs never let it drop back below), defeating amortization. Gate
	// on an actual call counter so the scan runs at most once per interval.
	r.calls++
	if r.calls%evictSampleEvery != 0 {
		return
	}
	if len(r.last) < evictSampleEvery {
		return
	}
	cutoff := now.Add(-stateLimiterEvictAfter)
	for k, t := range r.last {
		if t.Before(cutoff) {
			delete(r.last, k)
		}
	}
}

// handleMirrorEvent routes a sprint-069 MIRROR_EVENT frame into the
// management-plane mirror tables via the registered MirrorIngester.
// Nil-safe: when no ingester is wired (test fakes, pre-migration boots)
// the frame is logged at DEBUG and dropped so the agent doesn't pile
// up retries.
func (h *Hub) handleMirrorEvent(conn *AgentConnection, msg *protocol.Message) {
	h.mu.RLock()
	ingester := h.mirror
	h.mu.RUnlock()
	if ingester == nil {
		h.log.Debug("MIRROR_EVENT received but no ingester wired", slog.String("cluster_id", conn.ClusterID))
		return
	}
	var payload protocol.MirrorEventPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.log.Warn("invalid MIRROR_EVENT payload",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}
	clusterID, err := uuid.Parse(conn.ClusterID)
	if err != nil {
		h.log.Warn("MIRROR_EVENT from cluster with invalid UUID",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}
	if err := ingester.RouteMirrorEvent(context.Background(), clusterID, payload); err != nil {
		// Failure is logged but never propagated back to the agent —
		// the agent's next resync (mirrorResyncPeriod) will re-emit,
		// and periodic prune cleans up if a row stays stale.
		h.log.Warn("MIRROR_EVENT ingest failed",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("kind", payload.Kind),
			slog.String("name", payload.Name),
			slog.String("error", err.Error()),
		)
	}
}

// handleApiserverAudit persists a batch of kube-apiserver audit events the
// agent tailed and forwarded over the tunnel. The cluster ID is taken from the
// AUTHENTICATED connection (conn.ClusterID), NOT from the payload — that is the
// security property of carrying the batch over the tunnel rather than the open
// HTTP ingest endpoint. Nil-safe: when no persister is wired the frame is
// logged at DEBUG and dropped.
func (h *Hub) handleApiserverAudit(conn *AgentConnection, msg *protocol.Message) {
	h.mu.RLock()
	persister := h.auditPersister
	h.mu.RUnlock()
	if persister == nil {
		h.log.Debug("APISERVER_AUDIT received but no persister wired", slog.String("cluster_id", conn.ClusterID))
		return
	}
	var payload protocol.ApiserverAuditPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.log.Warn("invalid APISERVER_AUDIT payload",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}
	clusterID, err := uuid.Parse(conn.ClusterID)
	if err != nil {
		h.log.Warn("APISERVER_AUDIT from cluster with invalid UUID",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}
	accepted, skipped, err := persister.PersistAuditEvents(context.Background(), clusterID, payload.Events)
	if err != nil {
		h.log.Warn("APISERVER_AUDIT persist failed",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("error", err.Error()),
		)
		// Ack the failure so the agent knows to hold its checkpoint and
		// re-forward — silence would otherwise look like a lost batch and
		// only resolve on the agent's ack timeout. Skipped on the legacy
		// no-BatchID path (the agent isn't waiting on an ack there).
		h.sendApiserverAuditAck(conn, protocol.ApiserverAuditAckPayload{
			BatchID: payload.BatchID,
			OK:      false,
			Error:   err.Error(),
		})
		return
	}
	h.log.Debug("APISERVER_AUDIT persisted",
		slog.String("cluster_id", conn.ClusterID),
		slog.Int("accepted", accepted),
		slog.Int("skipped", skipped),
	)
	h.sendApiserverAuditAck(conn, protocol.ApiserverAuditAckPayload{
		BatchID:  payload.BatchID,
		OK:       true,
		Accepted: accepted,
		Skipped:  skipped,
	})
}

// sendApiserverAuditAck sends a MsgApiserverAuditAck back to the SAME agent
// connection that sent the batch. A missing BatchID means the batch came over
// the legacy fire-and-forget path (or the HTTP sender, which acks via status
// code), so there is no agent waiter to satisfy and we skip the frame.
func (h *Hub) sendApiserverAuditAck(conn *AgentConnection, ack protocol.ApiserverAuditAckPayload) {
	if ack.BatchID == "" {
		return
	}
	body, err := json.Marshal(ack)
	if err != nil {
		h.log.Warn("marshal APISERVER_AUDIT_ACK failed",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}
	msg := &protocol.Message{
		Type:      protocol.MsgApiserverAuditAck,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}
	// Send directly on the originating connection's channel so the ack races
	// back to the same agent that is blocked waiting on this BatchID. A full
	// buffer drops the ack; the agent's bounded wait times out and re-forwards.
	select {
	case conn.sendCh <- msg:
	default:
		h.log.Warn("APISERVER_AUDIT_ACK dropped: send buffer full",
			slog.String("cluster_id", conn.ClusterID),
			slog.String("batch_id", ack.BatchID),
		)
	}
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
