package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"nhooyr.io/websocket"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ExecConsumer handles WebSocket connections for pod exec.
// Route: /api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/
//
// Wire protocol between this consumer and the frontend (browser xterm.js):
//
//	frontend → consumer (JSON text frames):
//	  {"type":"stdin",  "data": "<utf-8 keystrokes>"}
//	  {"type":"resize", "cols": <int>, "rows": <int>}
//	  {"type":"auth",   "token": "<jwt-or-api-token>"}      // accepted but no-op; prefer ?token=
//
//	consumer → frontend (JSON text frames):
//	  {"type":"output", "data": "<stdout/stderr chunk>"}
//	  {"type":"error",  "message": "<reason>"}
//	  {"type":"end",    "reason":  "<completed|...>"}
//
// The wire format BETWEEN the consumer and the cluster agent (EXEC_START /
// EXEC_INPUT / EXEC_RESIZE / EXEC_OUTPUT / EXEC_END) is unchanged — this
// consumer is purely a translator between that tunnel protocol and the
// browser-friendly envelope above. Coordinating a wire change with the agent
// is out of scope for this fix.
type ExecConsumer struct {
	hub     *Hub
	log     *slog.Logger
	jwt     *iauth.JWTManager
	queries appmiddleware.TokenUserQuerier
}

// NewExecConsumer creates a new ExecConsumer.
func NewExecConsumer(hub *Hub, log *slog.Logger) *ExecConsumer {
	if log == nil {
		log = slog.Default()
	}
	return &ExecConsumer{hub: hub, log: log}
}

// SetAuth wires the JWT manager + token querier so the handler can validate
// the `?token=` query parameter. Browser WebSocket clients cannot set custom
// Authorization headers, so query-param auth is the only viable scheme for
// this endpoint. Both arguments are optional; when nil the handler accepts
// unauthenticated connections (used by tests / dev runs without auth wired).
func (ec *ExecConsumer) SetAuth(jwt *iauth.JWTManager, queries appmiddleware.TokenUserQuerier) {
	if ec == nil {
		return
	}
	ec.jwt = jwt
	ec.queries = queries
}

// authenticate validates the request via Authorization header (preferred) or
// `?token=` query parameter (browser WebSocket fallback). Returns true if
// authenticated, or if no JWT manager is wired (dev/test mode).
func (ec *ExecConsumer) authenticate(r *http.Request) bool {
	if ec.jwt == nil {
		return true
	}
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		return false
	}
	if strings.HasPrefix(token, "astro_") {
		if ec.queries == nil {
			return false
		}
		hash := sha256.Sum256([]byte(token))
		hashStr := hex.EncodeToString(hash[:])
		apiToken, err := ec.queries.GetTokenByHash(r.Context(), hashStr)
		if err != nil {
			return false
		}
		if apiToken.ExpiresAt.Valid && apiToken.ExpiresAt.Time.Before(time.Now()) {
			return false
		}
		dbUser, err := ec.queries.GetUserByID(r.Context(), apiToken.UserID)
		if err != nil || !dbUser.IsActive {
			return false
		}
		return true
	}
	claims, err := ec.jwt.ValidateToken(token)
	if err != nil {
		return false
	}
	if claims.UserID == uuid.Nil {
		return false
	}
	return true
}

func bearerToken(h string) string {
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
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

	if !ec.authenticate(r) {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
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
		// Best-effort error frame so the UI surfaces the problem instead of
		// silently disconnecting.
		_ = writeFrontendError(r.Context(), conn, "Cluster agent not connected")
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
		_ = writeFrontendError(r.Context(), conn, "failed to create stream")
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
		_ = writeFrontendError(r.Context(), conn, "failed to start exec session")
		conn.Close(websocket.StatusInternalError, "failed to start exec session")
		return
	}

	// `done` is closed by whichever goroutine detects end-of-session first.
	// Whoever closes it should also cancel `relayCtx` so the *other* loop
	// notices and unblocks. We use a derived context so the read loop (which
	// blocks on conn.Read) and the write loop (which blocks on stream.DataCh)
	// can both be unblocked when either side ends.
	relayCtx, cancelRelay := context.WithCancel(r.Context())
	defer cancelRelay()

	writeDone := make(chan struct{})
	readDone := make(chan struct{})

	// Write loop: EXEC_OUTPUT / EXEC_END from agent → frontend WS, translated
	// into the browser-friendly envelope.
	go func() {
		defer close(writeDone)
		defer cancelRelay()
		for {
			select {
			case data, ok := <-stream.DataCh:
				if !ok {
					return
				}
				if !translateAndSendToFrontend(relayCtx, conn, data, ec.log) {
					return
				}
			case <-stream.DoneCh:
				return
			case <-relayCtx.Done():
				return
			}
		}
	}()

	// Read loop: frontend WS → EXEC_INPUT/EXEC_RESIZE messages to agent.
	go func() {
		defer close(readDone)
		defer cancelRelay()
		for {
			_, data, err := conn.Read(relayCtx)
			if err != nil {
				ec.log.Debug("read from frontend failed", slog.String("error", err.Error()))
				// Send EXEC_END to agent so the SPDY session shuts down cleanly.
				endMsg := &protocol.Message{
					Type:      protocol.MsgExecEnd,
					StreamID:  streamID,
					ClusterID: clusterID,
					Timestamp: time.Now().UTC(),
				}
				_ = ec.hub.SendToAgent(clusterID, endMsg)
				return
			}

			tunnelMsg, skip := translateFromFrontend(data, streamID, clusterID)
			if skip {
				continue
			}
			if tunnelMsg == nil {
				continue
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

	// Wait for either side to finish. Whichever loop exits first cancels
	// relayCtx, which unblocks the other.
	select {
	case <-writeDone:
	case <-readDone:
	}
	<-writeDone
	<-readDone
}

// frontendInbound is the typed envelope the browser sends.
type frontendInbound struct {
	Type  string `json:"type"`
	Data  string `json:"data,omitempty"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
	Token string `json:"token,omitempty"`
}

// translateFromFrontend turns one JSON envelope from the browser into the
// matching tunnel message for the agent. Returns (msg, skip) — when skip is
// true the caller should drop the frame without forwarding (e.g. `auth`
// frames or malformed input). Unrecognized payloads fall back to being
// forwarded as raw stdin so a hand-rolled debugging client still works.
func translateFromFrontend(data []byte, streamID, clusterID string) (*protocol.Message, bool) {
	var env frontendInbound
	if err := json.Unmarshal(data, &env); err == nil && env.Type != "" {
		switch env.Type {
		case "stdin", "input":
			// Send raw keystrokes as the payload — the agent writes payload
			// bytes verbatim to the SPDY stdin pipe.
			return &protocol.Message{
				Type:      protocol.MsgExecInput,
				StreamID:  streamID,
				ClusterID: clusterID,
				Timestamp: time.Now().UTC(),
				Payload:   json.RawMessage([]byte(env.Data)),
			}, false
		case "resize":
			resize := protocol.ExecResizePayload{
				Width:  env.Cols,
				Height: env.Rows,
			}
			payload, _ := json.Marshal(resize)
			return &protocol.Message{
				Type:      protocol.MsgExecResize,
				StreamID:  streamID,
				ClusterID: clusterID,
				Timestamp: time.Now().UTC(),
				Payload:   payload,
			}, false
		case "auth":
			// Auth is handled at the HTTP upgrade via Authorization header /
			// ?token=. Inline auth frames are accepted for backwards-compat
			// with the old frontend but are not validated here.
			return nil, true
		case "end", "close":
			return &protocol.Message{
				Type:      protocol.MsgExecEnd,
				StreamID:  streamID,
				ClusterID: clusterID,
				Timestamp: time.Now().UTC(),
			}, false
		}
	}
	// Fallback: treat as raw stdin so non-JSON clients keep working.
	return &protocol.Message{
		Type:      protocol.MsgExecInput,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   json.RawMessage(data),
	}, false
}

// translateAndSendToFrontend converts one agent payload into a browser-
// friendly envelope and writes it to the WS. Returns false on write failure
// so the caller exits its loop.
//
// Agent payloads on the stream channel are either:
//   - {"stream":"stdout|stderr","data":"<chunk>"}         (EXEC_OUTPUT)
//   - {"reason":"<text>"}                                 (EXEC_END, success)
//   - {"error":"<text>"}                                  (EXEC_END, error)
//
// The MessageType isn't carried down to the stream channel, so we
// disambiguate by inspecting fields — the same approach
// internal/tunnel/originators.go takes.
func translateAndSendToFrontend(ctx context.Context, conn *websocket.Conn, data []byte, log *slog.Logger) bool {
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		// Not JSON — forward raw text so xterm still renders something.
		return safeWrite(ctx, conn, data, log)
	}

	if errStr, ok := probe["error"].(string); ok && errStr != "" {
		frame, _ := json.Marshal(map[string]string{"type": "error", "message": errStr})
		if !safeWrite(ctx, conn, frame, log) {
			return false
		}
		// Treat error as terminal — the agent has closed the SPDY exec.
		end, _ := json.Marshal(map[string]string{"type": "end", "reason": "error"})
		_ = safeWrite(ctx, conn, end, log)
		return false
	}

	if reason, ok := probe["reason"].(string); ok {
		frame, _ := json.Marshal(map[string]string{"type": "end", "reason": reason})
		_ = safeWrite(ctx, conn, frame, log)
		return false
	}

	chunk, _ := probe["data"].(string)
	streamName, _ := probe["stream"].(string)
	if chunk == "" {
		// Empty / unknown shape — emit nothing rather than crashing.
		return true
	}
	frame, _ := json.Marshal(map[string]string{
		"type":   "output",
		"stream": streamName,
		"data":   chunk,
	})
	return safeWrite(ctx, conn, frame, log)
}

func safeWrite(ctx context.Context, conn *websocket.Conn, data []byte, log *slog.Logger) bool {
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		log.Debug("write to frontend failed", slog.String("error", err.Error()))
		return false
	}
	return true
}

func writeFrontendError(ctx context.Context, conn *websocket.Conn, msg string) error {
	frame, _ := json.Marshal(map[string]string{"type": "error", "message": msg})
	return conn.Write(ctx, websocket.MessageText, frame)
}
