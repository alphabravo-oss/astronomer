package tunnel

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
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
//	?ticket=<short-lived-ticket>               (browser path)
//
// Without `SetAuth`, the handler accepts unauthenticated connections
// (dev/test mode).
type LogsConsumer struct {
	hub         *Hub
	log         *slog.Logger
	jwt         *auth.JWTManager
	queries     middleware.TokenUserQuerier
	tickets     *auth.StreamTicketStore
	auditWriter any
	rbacEngine  *rbac.Engine
	rbacQuerier middleware.RBACQuerier
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

func (lc *LogsConsumer) SetStreamTickets(tickets *auth.StreamTicketStore) {
	if lc == nil {
		return
	}
	lc.tickets = tickets
}

func (lc *LogsConsumer) SetAuditWriter(auditWriter any) {
	if lc == nil {
		return
	}
	lc.auditWriter = auditWriter
}

// SetAuthorization wires the RBAC engine + binding querier so HandleLogs can
// enforce per-cluster permissions on the Authorization-header path (the
// browser ?ticket= path is already RBAC-gated at ticket issuance). Both
// arguments are optional; when nil the per-cluster check is skipped, matching
// the optional-auth contract of SetAuth (dev/test runs without RBAC wired).
func (lc *LogsConsumer) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	if lc == nil {
		return
	}
	lc.rbacEngine = engine
	lc.rbacQuerier = querier
}

// authorizeCluster reports whether userID holds clusters:read on clusterID
// within namespace. Pod logs frequently contain secrets, so streaming them
// requires the same clusters:read gate the stream-ticket issuance path
// (internal/handler/stream_tickets.go) enforces for logs tickets. The concrete
// namespace is threaded through so a namespace-scoped binding grants access in
// the pod's namespace; cluster-wide bindings still pass for any namespace. When
// the RBAC engine/querier are not wired the check is skipped.
func (lc *LogsConsumer) authorizeCluster(ctx context.Context, userID, clusterID uuid.UUID, namespace string) bool {
	if lc.rbacEngine == nil || lc.rbacQuerier == nil {
		return true
	}
	bindings, err := lc.rbacQuerier.GetUserBindings(ctx, userID.String())
	if err != nil {
		return false
	}
	return lc.rbacEngine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbRead, clusterID, uuid.Nil, namespace)
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

	// Multi-replica WS hand-off — same rationale as exec_consumer.go, and do it
	// BEFORE consuming the one-use stream ticket. Forward the upgrade to the
	// sibling pod that owns the tunnel before we authenticate/Accept, so log
	// frames flow on the agent-owning pod and don't drop into "no stream
	// found". Authenticating first would burn the browser's single-use
	// ?ticket= on this non-owner pod, so the owner pod's re-Validate returns
	// not-found and 401s the session. The sibling that terminates the stream
	// runs its own authenticateStreamRequest + authorizeCluster below.
	if ForwardWSToOwnerPod(lc.hub, lc.log, w, r, clusterID) {
		return
	}

	userID, ok := authenticateStreamRequest(r, lc.queries, lc.jwt, lc.tickets, auth.StreamKindLogs)
	if !ok {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		return
	}

	// Per-cluster authorization. Identity alone is not enough — without this
	// any authenticated user could stream logs (often containing secrets) from
	// pods on ANY cluster regardless of their RBAC bindings.
	clusterUUID, err := uuid.Parse(clusterID)
	if err != nil || !lc.authorizeCluster(r.Context(), userID, clusterUUID, namespace) {
		http.Error(w, `{"error":"you do not have permission to perform this action"}`, http.StatusForbidden)
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
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "closed")
	}()
	recordStreamOpenAudit(r, lc.auditWriter, userID, "pod.logs.opened", clusterID, namespace, pod, container)

	// Get agent connection from hub.
	agent := lc.hub.GetAgent(clusterID)
	if agent == nil {
		lc.log.Warn("no agent connected for cluster", slog.String("cluster_id", clusterID))
		// Send a structured error frame to the client before closing so the UI
		// can surface "agent not connected" instead of a silent hang.
		_ = logsWriteFrontendError(r.Context(), conn, "agent_not_connected", "Cluster agent is not connected")
		_ = conn.Close(websocket.StatusInternalError, "Cluster agent not connected")
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
		_ = conn.Close(websocket.StatusInternalError, "failed to create stream")
		return
	}
	defer agent.Streams.CloseStream(streamID)
	// Tell the agent to stop tailing when this WS ends. Without this, on
	// client disconnect we close only our LOCAL stream — the agent's
	// follow=true kubelet stream + its pump goroutine keep running forever
	// (its sendFn still succeeds because the tunnel WS is up), leaking one
	// goroutine + one open kubelet log connection per opened-then-closed
	// follow view. Mirrors exec_consumer.go sending MsgExecEnd on read-loop
	// exit; agent/logs.go:HandleLogStop cancels the session context and the
	// goroutine drains. Registered after CloseStream so it runs first (LIFO):
	// signal the agent, then tear down the local stream.
	defer func() {
		stopMsg := &protocol.Message{
			Type:      protocol.MsgLogStop,
			StreamID:  streamID,
			ClusterID: clusterID,
			Timestamp: time.Now().UTC(),
		}
		_ = lc.hub.SendToAgent(clusterID, stopMsg)
	}()

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
		_ = conn.Close(websocket.StatusInternalError, "failed to start log stream")
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
