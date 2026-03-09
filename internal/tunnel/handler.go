package tunnel

import (
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// handleMessage dispatches incoming messages from an agent by type.
func (h *Hub) handleMessage(conn *AgentConnection, msg *protocol.Message) {
	switch msg.Type {
	case protocol.MsgPong:
		h.handlePong(conn, msg)

	case protocol.MsgHeartbeat:
		h.handleHeartbeat(conn, msg)

	case protocol.MsgK8sResponse:
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
	// In a full implementation, this would update cluster health + agent status in the database.
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
