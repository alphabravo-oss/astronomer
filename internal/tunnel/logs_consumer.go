package tunnel

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// LogsConsumer handles WebSocket connections for log streaming.
// Route: /api/v1/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/
//
// Auth contract:
//
//	Authorization: Bearer <jwt-or-api-token>   (XHR / curl)
//
// — or, because browser `new WebSocket(...)` cannot set custom headers —
//
//	?token=<jwt-or-api-token>                  (browser fallback)
//
// Either path is validated against the same JWTManager and TokenUserQuerier
// the rest of the API uses. Without `SetAuth`, the handler accepts
// unauthenticated connections (dev/test mode).
type LogsConsumer struct {
	hub     *Hub
	log     *slog.Logger
	jwt     *auth.JWTManager
	queries middleware.TokenUserQuerier
}

// NewLogsConsumer creates a new LogsConsumer.
func NewLogsConsumer(hub *Hub, log *slog.Logger) *LogsConsumer {
	if log == nil {
		log = slog.Default()
	}
	return &LogsConsumer{hub: hub, log: log}
}

// SetAuth wires the JWT manager + token querier so HandleLogs can authenticate
// connections before performing the WebSocket upgrade. Both arguments are
// optional; when nil the handler accepts unauthenticated connections.
func (lc *LogsConsumer) SetAuth(jwt *auth.JWTManager, queries middleware.TokenUserQuerier) {
	if lc == nil {
		return
	}
	lc.jwt = jwt
	lc.queries = queries
}

// authenticate validates the request via Authorization header (preferred)
// or `?token=` query parameter (browser-WS fallback). Delegates to the
// shared auth.AuthorizeStreamRequest helper so the three long-lived stream
// endpoints (WS logs, WS exec, SSE events) share a single validation path.
func (lc *LogsConsumer) authenticate(r *http.Request) bool {
	_, ok := auth.AuthorizeStreamRequest(r, lc.queries, lc.jwt)
	return ok
}

// HandleLogs upgrades to WebSocket and relays log data from the cluster agent
// to the frontend client.
func (lc *LogsConsumer) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if !lc.authenticate(r) {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		return
	}

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
		// Send a structured error frame to the client before closing so the UI
		// can surface "agent not connected" instead of a silent hang.
		_ = logsWriteFrontendError(r.Context(), conn, "agent_not_connected", "Cluster agent is not connected")
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
		_ = logsWriteFrontendError(r.Context(), conn, "stream_create_failed", err.Error())
		conn.Close(websocket.StatusInternalError, "failed to create stream")
		return
	}
	defer agent.Streams.CloseStream(streamID)

	// Parse query parameters for log options.
	follow := r.URL.Query().Get("follow") == "true"
	tailLines := 0
	if tl := r.URL.Query().Get("tail_lines"); tl != "" {
		if n, err := strconv.Atoi(tl); err == nil && n > 0 {
			tailLines = n
		}
	} else if tl := r.URL.Query().Get("tailLines"); tl != "" {
		if n, err := strconv.Atoi(tl); err == nil && n > 0 {
			tailLines = n
		}
	}
	// since_seconds is mutually exclusive with tail_lines in the UI
	// (Rancher-style picker). Validate at the WS query-param boundary —
	// reject non-positive values rather than passing them on to kubelet.
	var sinceSeconds *int64
	if ss := r.URL.Query().Get("since_seconds"); ss != "" {
		if n, err := strconv.ParseInt(ss, 10, 64); err == nil && n > 0 {
			sinceSeconds = &n
		}
	} else if ss := r.URL.Query().Get("sinceSeconds"); ss != "" {
		if n, err := strconv.ParseInt(ss, 10, 64); err == nil && n > 0 {
			sinceSeconds = &n
		}
	}
	// Always request timestamps from kubelet so the frontend can render real
	// per-line times instead of a constant "received-at" value. The
	// translation loop below parses the RFC3339Nano prefix back out.
	timestamps := true

	// Send LOG_START to agent.
	startPayload, _ := json.Marshal(protocol.LogStartPayload{
		Namespace:    namespace,
		Pod:          pod,
		Container:    container,
		Follow:       follow,
		TailLines:    tailLines,
		SinceSeconds: sinceSeconds,
		Timestamps:   timestamps,
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
		_ = logsWriteFrontendError(r.Context(), conn, "log_start_failed", err.Error())
		conn.Close(websocket.StatusInternalError, "failed to start log stream")
		return
	}

	ctx := r.Context()

	// Forward LOG_DATA from agent → frontend WS, translating the agent's
	// `{"line":"..."}` envelope into the `{timestamp, message, container,
	// level}` shape the frontend's PodLog type expects.
	// On LOG_END or client disconnect, clean up.
	for {
		select {
		case data, ok := <-stream.DataCh:
			if !ok {
				return
			}
			out := translateLogLine(data, container)
			if err := conn.Write(ctx, websocket.MessageText, out); err != nil {
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

// translateLogLine converts the agent's tunnel envelope `{"line":"..."}` into
// the frontend's PodLog JSON. When kubelet is asked for timestamps, the line
// is prefixed with an RFC3339Nano timestamp followed by a single space; we
// split that off so the UI can render a per-line time. If parsing fails we
// fall back to `time.Now()` and pass the full line through unchanged.
func translateLogLine(raw []byte, container string) []byte {
	var env struct {
		Line string `json:"line"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		// Not the expected envelope — forward as-is, wrapped.
		env.Line = string(raw)
	}

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	msg := env.Line
	if sp := strings.IndexByte(env.Line, ' '); sp > 0 {
		if _, err := time.Parse(time.RFC3339Nano, env.Line[:sp]); err == nil {
			ts = env.Line[:sp]
			msg = env.Line[sp+1:]
		}
	}

	out, _ := json.Marshal(map[string]string{
		"timestamp": ts,
		"message":   msg,
		"container": container,
	})
	return out
}

// writeFrontendError sends a structured error frame to the frontend WS so the
// UI can surface a clear error rather than a silent close.
func logsWriteFrontendError(ctx context.Context, conn *websocket.Conn, code, message string) error {
	out, _ := json.Marshal(map[string]string{
		"type":    "error",
		"code":    code,
		"message": message,
	})
	return conn.Write(ctx, websocket.MessageText, out)
}
