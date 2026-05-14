package tunnel

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
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
// authenticated, or if no JWT manager is wired (dev/test mode). Delegates to
// the shared auth.AuthorizeStreamRequest helper.
func (ec *ExecConsumer) authenticate(r *http.Request) bool {
	_, ok := iauth.AuthorizeStreamRequest(r, ec.queries, ec.jwt)
	return ok
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

	// Multi-replica WS hand-off. If the cluster's tunnel is owned by a
	// sibling pod (the redis locator knows), forward the entire
	// HTTP-Upgrade to that pod so the WS lands where the agent
	// tunnel terminates. Without this the K8S_STREAM_FRAME replies
	// arrive on the agent-owning pod, find no matching stream on this
	// pod, and the WS dies with "no stream found for message".
	if ForwardWSToOwnerPod(ec.hub, ec.log, w, r, clusterID) {
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

	ec.ProxyToAgent(r.Context(), conn, clusterID, namespace, pod, container)
}

// ProxyToAgent runs the exec relay between an already-upgraded WebSocket
// connection and the cluster agent. The caller is responsible for the WS
// Accept handshake and for closing `conn` after this returns.
//
// This is the same code path HandleExec uses post-upgrade; it's exposed so
// session-aware front-doors (e.g. the kubectl_shell WS handler, which needs
// to validate a session row before bridging) can reuse the relay without
// going through a 307 redirect onto /api/v1/ws/exec/. Browsers — Firefox in
// particular — do not portably follow redirects on WS handshakes.
func (ec *ExecConsumer) ProxyToAgent(ctx context.Context, conn *websocket.Conn, clusterID, namespace, pod, container string) {
	ec.proxyToAgent(ctx, conn, clusterID, namespace, pod, container, nil)
}

// ProxyToAgentWithInputRecorder is the audited variant used by the
// kubectl-shell front door. onInput receives the raw payload bytes of
// each inbound frame (the JSON envelope from the browser) before the
// relay forwards it. The callback runs synchronously in the read loop
// — the kubectl_shell handler keeps it cheap (append to a line buffer,
// fire-and-forget INSERT on newline) precisely because of that.
//
// When onInput is nil this behaves identically to ProxyToAgent.
func (ec *ExecConsumer) ProxyToAgentWithInputRecorder(ctx context.Context, conn *websocket.Conn, clusterID, namespace, pod, container string, onInput func([]byte)) {
	ec.proxyToAgent(ctx, conn, clusterID, namespace, pod, container, onInput)
}

// proxyToAgent is the unified relay implementation. The exported entry
// points differ only in whether they pass a non-nil onInput callback.
func (ec *ExecConsumer) proxyToAgent(ctx context.Context, conn *websocket.Conn, clusterID, namespace, pod, container string, onInput func([]byte)) {
	// Get agent connection from hub.
	agent := ec.hub.GetAgent(clusterID)
	if agent == nil {
		ec.log.Warn("no agent connected for cluster", slog.String("cluster_id", clusterID))
		// Best-effort error frame so the UI surfaces the problem instead of
		// silently disconnecting.
		_ = writeFrontendError(ctx, conn, "Cluster agent not connected")
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
		_ = writeFrontendError(ctx, conn, "failed to create stream")
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
		_ = writeFrontendError(ctx, conn, "failed to start exec session")
		conn.Close(websocket.StatusInternalError, "failed to start exec session")
		return
	}

	// `done` is closed by whichever goroutine detects end-of-session first.
	// Whoever closes it should also cancel `relayCtx` so the *other* loop
	// notices and unblocks. We use a derived context so the read loop (which
	// blocks on conn.Read) and the write loop (which blocks on stream.DataCh)
	// can both be unblocked when either side ends.
	relayCtx, cancelRelay := context.WithCancel(ctx)
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

			// Fire the audit hook before forwarding. Both the recorder
			// and the relay see the same bytes; if the recorder is
			// slow, the relay still ships the keystroke promptly
			// because onInput is a cheap-by-contract callback.
			if onInput != nil {
				onInput(data)
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
			// IMPORTANT: protocol.Message.Payload is json.RawMessage, which
			// MUST contain valid JSON — the whole envelope is re-marshaled
			// to the agent over the tunnel. Putting raw bytes in here
			// produces invalid JSON for any non-numeric input (e.g. a
			// keystroke "i" → "payload":i) and json.Unmarshal on the agent
			// side drops the frame, so stdin never reaches the shell.
			//
			// We JSON-encode the data string here; the agent's
			// HandleExecInput unmarshals it back into a string before
			// writing to the SPDY stdin pipe.
			encoded, err := json.Marshal(env.Data)
			if err != nil {
				return nil, true
			}
			return &protocol.Message{
				Type:      protocol.MsgExecInput,
				StreamID:  streamID,
				ClusterID: clusterID,
				Timestamp: time.Now().UTC(),
				Payload:   encoded,
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
	// Fallback: treat the whole frame as raw stdin bytes so a hand-rolled
	// debugging client (websocat piping shell commands) still works. Wrap
	// the bytes as a JSON string so the marshaled message stays valid.
	encoded, err := json.Marshal(string(data))
	if err != nil {
		return nil, true
	}
	return &protocol.Message{
		Type:      protocol.MsgExecInput,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   encoded,
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
