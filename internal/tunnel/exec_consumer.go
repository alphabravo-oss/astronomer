package tunnel

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ExecConsumer handles WebSocket connections for pod exec.
// Route: /api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/
type ExecConsumer struct {
	hub *Hub
	log *slog.Logger
}

// NewExecConsumer creates a new ExecConsumer.
func NewExecConsumer(hub *Hub, log *slog.Logger) *ExecConsumer {
	if log == nil {
		log = slog.Default()
	}
	return &ExecConsumer{hub: hub, log: log}
}

// HandleExec upgrades to WebSocket and relays exec I/O between the frontend
// client and the cluster agent.
func (ec *ExecConsumer) HandleExec(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	namespace := chi.URLParam(r, "namespace")
	pod := chi.URLParam(r, "pod")
	container := chi.URLParam(r, "container")

	if clusterID == "" || namespace == "" || pod == "" || container == "" {
		http.Error(w, `{"error":"cluster_id, namespace, pod, and container are required"}`, http.StatusBadRequest)
		return
	}

	// Accept WebSocket from frontend client.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		ec.log.Error("websocket accept failed", slog.String("error", err.Error()))
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "closed")

	// Get agent connection from hub.
	agent := ec.hub.GetAgent(clusterID)
	if agent == nil {
		ec.log.Warn("no agent connected for cluster", slog.String("cluster_id", clusterID))
		conn.Close(websocket.StatusInternalError, "Cluster agent not connected")
		return
	}

	// Create stream for exec session.
	streamID := uuid.New().String()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		ec.log.Error("failed to create stream",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		conn.Close(websocket.StatusInternalError, "failed to create stream")
		return
	}
	defer agent.Streams.CloseStream(streamID)

	// Send EXEC_START to agent with pod details.
	startPayload, _ := json.Marshal(protocol.ExecStartPayload{
		Namespace: namespace,
		Pod:       pod,
		Container: container,
		Command:   []string{"/bin/sh"},
		TTY:       true,
		Stdin:     true,
	})

	startMsg := &protocol.Message{
		Type:      protocol.MsgExecStart,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   startPayload,
	}

	if err := ec.hub.SendToAgent(clusterID, startMsg); err != nil {
		ec.log.Error("failed to send EXEC_START",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		conn.Close(websocket.StatusInternalError, "failed to start exec session")
		return
	}

	ctx := r.Context()
	done := make(chan struct{})

	// Write loop: EXEC_OUTPUT from agent → frontend WS.
	go func() {
		defer close(done)
		for {
			select {
			case data, ok := <-stream.DataCh:
				if !ok {
					return
				}
				if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
					ec.log.Debug("write to frontend failed", slog.String("error", err.Error()))
					return
				}
			case <-stream.DoneCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Read loop: frontend WS → EXEC_INPUT/EXEC_RESIZE messages to agent.
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				ec.log.Debug("read from frontend failed", slog.String("error", err.Error()))
				// Send EXEC_END to agent.
				endMsg := &protocol.Message{
					Type:      protocol.MsgExecEnd,
					StreamID:  streamID,
					ClusterID: clusterID,
					Timestamp: time.Now().UTC(),
				}
				_ = ec.hub.SendToAgent(clusterID, endMsg)
				return
			}

			// Try to detect if this is a resize message.
			msgType := protocol.MsgExecInput
			var resize protocol.ExecResizePayload
			if json.Unmarshal(data, &resize) == nil && resize.Width > 0 && resize.Height > 0 {
				msgType = protocol.MsgExecResize
			}

			tunnelMsg := &protocol.Message{
				Type:      msgType,
				StreamID:  streamID,
				ClusterID: clusterID,
				Timestamp: time.Now().UTC(),
				Payload:   data,
			}

			if err := ec.hub.SendToAgent(clusterID, tunnelMsg); err != nil {
				ec.log.Error("failed to send to agent",
					slog.String("cluster_id", clusterID),
					slog.String("error", err.Error()),
				)
				return
			}
		}
	}()

	// Wait for the write loop to finish (agent closed stream or context cancelled).
	<-done
}
