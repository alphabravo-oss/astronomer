package tunnel

import (
	"encoding/json"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"log/slog"
	"time"
)

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
