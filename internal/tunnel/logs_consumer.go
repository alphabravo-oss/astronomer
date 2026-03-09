package tunnel

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// LogsConsumer handles WebSocket connections for log streaming.
// Route: /api/v1/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/
type LogsConsumer struct {
	hub *Hub
	log *slog.Logger
}

// NewLogsConsumer creates a new LogsConsumer.
func NewLogsConsumer(hub *Hub, log *slog.Logger) *LogsConsumer {
	if log == nil {
		log = slog.Default()
	}
	return &LogsConsumer{hub: hub, log: log}
}

// HandleLogs upgrades to WebSocket and relays log data from the cluster agent
// to the frontend client.
func (lc *LogsConsumer) HandleLogs(w http.ResponseWriter, r *http.Request) {
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
		lc.log.Error("websocket accept failed", slog.String("error", err.Error()))
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "closed")

	// Get agent connection from hub.
	agent := lc.hub.GetAgent(clusterID)
	if agent == nil {
		lc.log.Warn("no agent connected for cluster", slog.String("cluster_id", clusterID))
		conn.Close(websocket.StatusInternalError, "Cluster agent not connected")
		return
	}

	// Create stream for log session.
	streamID := uuid.New().String()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		lc.log.Error("failed to create stream",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		conn.Close(websocket.StatusInternalError, "failed to create stream")
		return
	}
	defer agent.Streams.CloseStream(streamID)

	// Parse query parameters for log options.
	follow := r.URL.Query().Get("follow") == "true"
	tailLines := 0
	if tl := r.URL.Query().Get("tail_lines"); tl != "" {
		if n, err := strconv.Atoi(tl); err == nil {
			tailLines = n
		}
	}
	timestamps := r.URL.Query().Get("timestamps") == "true"

	// Send LOG_START to agent.
	startPayload, _ := json.Marshal(protocol.LogStartPayload{
		Namespace:  namespace,
		Pod:        pod,
		Container:  container,
		Follow:     follow,
		TailLines:  tailLines,
		Timestamps: timestamps,
	})

	startMsg := &protocol.Message{
		Type:      protocol.MsgLogStart,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   startPayload,
	}

	if err := lc.hub.SendToAgent(clusterID, startMsg); err != nil {
		lc.log.Error("failed to send LOG_START",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		conn.Close(websocket.StatusInternalError, "failed to start log stream")
		return
	}

	ctx := r.Context()

	// Forward LOG_DATA from agent → frontend WS.
	// On LOG_END or client disconnect, clean up.
	for {
		select {
		case data, ok := <-stream.DataCh:
			if !ok {
				return
			}
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				lc.log.Debug("write to frontend failed", slog.String("error", err.Error()))
				return
			}
		case <-stream.DoneCh:
			return
		case <-ctx.Done():
			return
		}
	}
}
