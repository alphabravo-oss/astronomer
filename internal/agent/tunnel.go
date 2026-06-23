package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/coder/websocket"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// MessageHandler processes an incoming tunnel message.
type MessageHandler func(ctx context.Context, msg *protocol.Message) (*protocol.Message, error)

// TunnelClient manages the WebSocket connection to the server.
type TunnelClient struct {
	config   *AgentConfig
	conn     *websocket.Conn
	log      *slog.Logger
	handlers map[protocol.MessageType]MessageHandler
	sendCh   chan *protocol.Message

	mu        sync.RWMutex
	connected bool

	// auditIngestToken is the scoped clusters:write API token the server
	// delivers in CONNECT_ACK for PATH A HTTP audit delivery. Empty until/
	// unless the server issues one. Guarded by mu.
	auditIngestToken string

	// failCloseOnce ensures the buffer-full eager close only
	// fires once per connection — repeated congestion shouldn't
	// hammer tc.conn.Close. Reset by dial() on each new connection.
	failCloseOnce *sync.Once
}

// NewTunnelClient creates a new tunnel client with the given configuration.
func NewTunnelClient(cfg *AgentConfig, log *slog.Logger) *TunnelClient {
	return &TunnelClient{
		config:        cfg,
		log:           log,
		handlers:      make(map[protocol.MessageType]MessageHandler),
		sendCh:        make(chan *protocol.Message, 256),
		failCloseOnce: &sync.Once{},
	}
}

// RegisterHandler registers a handler for a specific message type.
func (tc *TunnelClient) RegisterHandler(msgType protocol.MessageType, handler MessageHandler) {
	tc.handlers[msgType] = handler
}

// IsConnected returns the current connection status.
func (tc *TunnelClient) IsConnected() bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.connected
}

// AuditIngestToken returns the scoped apiserver-audit ingest token delivered
// by the server in CONNECT_ACK (PATH A), or "" if none was issued. Used to
// decide whether to wire an httpAuditSender.
func (tc *TunnelClient) AuditIngestToken() string {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.auditIngestToken
}

func (tc *TunnelClient) setConnected(v bool) {
	tc.mu.Lock()
	tc.connected = v
	tc.mu.Unlock()
}

// Connect establishes the WebSocket connection and runs the read/write loops.
// It blocks until ctx is cancelled or a fatal error occurs.
func (tc *TunnelClient) Connect(ctx context.Context) error {
	if err := tc.dial(ctx); err != nil {
		return fmt.Errorf("initial connection failed: %w", err)
	}

	tc.run(ctx)
	return nil
}

// dial performs the WebSocket handshake and the CONNECT/CONNECT_ACK exchange.
func (tc *TunnelClient) dial(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/v1/ws/agent/tunnel/%s/", tc.config.ServerURL, tc.config.ClusterID)
	tc.log.Info("dialing server", "url", url)

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+tc.config.AgentToken)

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	// Default read limit (32 KiB) is too small for proxied k8s API responses.
	conn.SetReadLimit(16 << 20)

	// Send CONNECT message.
	connectPayload := protocol.ConnectPayload{
		ClusterID:    tc.config.ClusterID,
		AgentID:      tc.config.AgentID,
		AgentVersion: version.Version,
		Token:        tc.config.AgentToken,
	}
	payloadBytes, err := json.Marshal(connectPayload)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "marshal error")
		return fmt.Errorf("marshal connect payload: %w", err)
	}

	connectMsg := &protocol.Message{
		Type:      protocol.MsgConnect,
		ClusterID: tc.config.ClusterID,
		Timestamp: time.Now().UTC(),
		Payload:   payloadBytes,
	}

	if err := tc.writeMessage(ctx, conn, connectMsg); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "write error")
		return fmt.Errorf("send CONNECT: %w", err)
	}

	// Wait for CONNECT_ACK with a 10-second timeout.
	ackCtx, ackCancel := context.WithTimeout(ctx, 10*time.Second)
	defer ackCancel()

	ackMsg, err := tc.readMessage(ackCtx, conn)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "read error")
		return fmt.Errorf("read CONNECT_ACK: %w", err)
	}
	if ackMsg.Type != protocol.MsgConnectAck {
		_ = conn.Close(websocket.StatusProtocolError, "expected CONNECT_ACK")
		return fmt.Errorf("expected CONNECT_ACK, got %s", ackMsg.Type)
	}

	var ack protocol.ConnectAckPayload
	if err := json.Unmarshal(ackMsg.Payload, &ack); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "unmarshal error")
		return fmt.Errorf("unmarshal CONNECT_ACK: %w", err)
	}
	if !ack.Accepted {
		_ = conn.Close(websocket.StatusNormalClosure, "rejected")
		return fmt.Errorf("connection rejected: %s", ack.Reason)
	}
	if ack.AgentToken != "" && ack.AgentToken != tc.config.AgentToken {
		tc.config.AgentToken = ack.AgentToken
		if err := persistRotatedToken(ctx, tc.config, ack.AgentToken); err != nil {
			tc.log.Warn("failed to persist rotated agent token", "error", err)
		} else {
			tc.log.Info("rotated durable agent token")
		}
	}
	// PATH A delivery: capture the scoped apiserver-audit ingest token if the
	// server issued one, so an httpAuditSender can be wired on top of it. Never
	// logged — it is a credential. Empty when the server doesn't issue one (the
	// audit tailer then keeps using its configured sender).
	if ack.AuditIngestToken != "" {
		tc.mu.Lock()
		tc.auditIngestToken = ack.AuditIngestToken
		tc.mu.Unlock()
	}

	tc.mu.Lock()
	tc.conn = conn
	// Reset the buffer-full eager-close gate for the new
	// connection so congestion on a previous session doesn't suppress
	// the safety mechanism on this one.
	tc.failCloseOnce = &sync.Once{}
	tc.mu.Unlock()
	tc.setConnected(true)
	tc.log.Info("connected to server", "cluster_id", tc.config.ClusterID)
	return nil
}

// run starts the read/write loops and handles reconnection.
func (tc *TunnelClient) run(ctx context.Context) {
	for {
		var wg sync.WaitGroup
		loopCtx, loopCancel := context.WithCancel(ctx)

		wg.Add(2)
		go func() {
			defer wg.Done()
			tc.readLoop(loopCtx)
			loopCancel()
		}()
		go func() {
			defer wg.Done()
			tc.writeLoop(loopCtx)
		}()

		wg.Wait()
		loopCancel()
		tc.setConnected(false)

		// Check if parent context is done.
		if ctx.Err() != nil {
			tc.log.Info("context cancelled, stopping tunnel")
			return
		}

		tc.log.Warn("connection lost, attempting reconnect")
		if err := tc.reconnectLoop(ctx); err != nil {
			tc.log.Error("reconnect failed permanently", "error", err)
			return
		}
	}
}

// BackoffDuration calculates the exponential backoff duration for a given attempt.
// Deterministic: no jitter. Used by tests; production code uses
// BackoffDurationWithJitter.
func BackoffDuration(attempt int, baseSeconds, maxSeconds int) time.Duration {
	backoff := float64(baseSeconds) * math.Pow(2, float64(attempt))
	if backoff > float64(maxSeconds) {
		backoff = float64(maxSeconds)
	}
	return time.Duration(backoff) * time.Second
}

// BackoffDurationWithJitter applies +/- 25% jitter to the exponential backoff
// to spread reconnect storms across many agents. The cap is applied AFTER
// computing the base exponential, so the jittered value can briefly exceed
// the cap by up to 25% — that's the point: if all agents disconnect at once,
// they should not all retry in lockstep at exactly maxSeconds.
//
// The jitter factor is uniformly distributed in [0.75, 1.25].
func BackoffDurationWithJitter(attempt int, baseSeconds, maxSeconds int, rng *rand.Rand) time.Duration {
	backoff := float64(baseSeconds) * math.Pow(2, float64(attempt))
	if backoff > float64(maxSeconds) {
		backoff = float64(maxSeconds)
	}
	// 25% jitter: factor in [0.75, 1.25].
	var jitter float64
	if rng != nil {
		jitter = 0.75 + rng.Float64()*0.5
	} else {
		jitter = 0.75 + rand.Float64()*0.5
	}
	return time.Duration(backoff*jitter) * time.Second
}

// reconnectLoop attempts to reconnect with jittered exponential backoff.
//
// At attempt=0 the loop uses InitialReconnectSpread instead of the normal
// exponential — a uniform random delay in [0, base) — so a synchronised
// disconnect (e.g. every agent in the fleet observing a server pod restart
// at the same wall-clock second) doesn't translate into a stampede against
// the same DB + auth path on the surviving replicas. With base=1s and 500
// agents, the previous code packed every reconnect into a 1.25s window
// (the ±25% jitter only); the spread now smears them across a full 1s
// before the exponential takes over.
func (tc *TunnelClient) reconnectLoop(ctx context.Context) error {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for attempt := 0; ; attempt++ {
		var wait time.Duration
		if attempt == 0 {
			wait = InitialReconnectSpread(tc.config.ReconnectBackoff, rng)
		} else {
			wait = BackoffDurationWithJitter(attempt, tc.config.ReconnectBackoff, tc.config.MaxReconnect, rng)
		}
		tc.log.Info("reconnecting", "attempt", attempt+1, "backoff", wait)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}

		if err := tc.dial(ctx); err != nil {
			tc.log.Warn("reconnect attempt failed", "attempt", attempt+1, "error", err)
			continue
		}

		tc.log.Info("reconnected successfully", "attempt", attempt+1)
		return nil
	}
}

// InitialReconnectSpread returns a uniform random delay in [0, baseSeconds)
// seconds, used by the first reconnect attempt to spread synchronised
// fleet-wide reconnects across the configured base window. The minimum
// is clamped to 100ms so a misconfigured base=0 doesn't busy-loop.
//
// Exported so tests can exercise the distribution without reaching into
// reconnectLoop's internals.
func InitialReconnectSpread(baseSeconds int, rng *rand.Rand) time.Duration {
	if baseSeconds <= 0 {
		baseSeconds = 1
	}
	var f float64
	if rng != nil {
		f = rng.Float64()
	} else {
		f = rand.Float64()
	}
	d := time.Duration(f * float64(baseSeconds) * float64(time.Second))
	if d < 100*time.Millisecond {
		d = 100 * time.Millisecond
	}
	return d
}

// readLoop reads messages from the WebSocket and dispatches them to handlers.
func (tc *TunnelClient) readLoop(ctx context.Context) {
	for {
		msg, err := tc.readMessage(ctx, tc.conn)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			tc.log.Error("read error", "error", err)
			return
		}

		tc.log.Debug("received message", "type", msg.Type, "stream_id", msg.StreamID)

		if msg.Type == protocol.MsgHeartbeat {
			pong := &protocol.Message{
				Type:      protocol.MsgPong,
				Timestamp: time.Now().UTC(),
			}
			if err := tc.Send(pong); err != nil {
				tc.log.Error("failed to send pong", "error", err)
			}
			continue
		}

		handler, ok := tc.handlers[msg.Type]
		if !ok {
			tc.log.Warn("no handler for message type", "type", msg.Type)
			continue
		}

		go func(m *protocol.Message) {
			resp, err := handler(ctx, m)
			if err != nil {
				tc.log.Error("handler error", "type", m.Type, "error", err)
				errPayload, _ := json.Marshal(protocol.ErrorPayload{
					Code:    "HANDLER_ERROR",
					Message: err.Error(),
				})
				resp = &protocol.Message{
					Type:      protocol.MsgError,
					StreamID:  m.StreamID,
					Timestamp: time.Now().UTC(),
					Payload:   errPayload,
				}
			}
			if resp != nil {
				if sendErr := tc.Send(resp); sendErr != nil {
					tc.log.Error("failed to send response", "error", sendErr)
				}
			}
		}(msg)
	}
}

// writeLoop sends messages from the send channel to the WebSocket.
func (tc *TunnelClient) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-tc.sendCh:
			if err := tc.writeMessage(ctx, tc.conn, msg); err != nil {
				if ctx.Err() != nil {
					return
				}
				tc.log.Error("write error", "error", err)
				return
			}
		}
	}
}

// Send queues a message for sending over the tunnel.
func (tc *TunnelClient) Send(msg *protocol.Message) error {
	select {
	case tc.sendCh <- msg:
		return nil
	default:
		observability.RecordDroppedEvent("agent_tunnel_send", "channel_full")
		// Dropping the message leaves the
		// server-side originator waiting on stream.DataCh until ctx
		// timeout (10 minutes for helm). Force an async WS close so
		// the server's CloseAll (server.go:305) wakes every in-flight
		// stream immediately and the agent re-dials on the reconnect
		// loop. failCloseOnce dedupes repeated congestion within the
		// same connection; dial() resets it on the next attempt.
		go tc.failClose("send buffer full")
		return fmt.Errorf("send channel full, closing tunnel; type=%s", msg.Type)
	}
}

// failClose force-closes the WebSocket once per connection. Used by
// Send() when sendCh is saturated so the server detects the failure
// immediately instead of waiting out the originator's context.
func (tc *TunnelClient) failClose(reason string) {
	tc.mu.RLock()
	once := tc.failCloseOnce
	conn := tc.conn
	tc.mu.RUnlock()
	if once == nil {
		return
	}
	once.Do(func() {
		tc.log.Warn("force-closing tunnel due to congestion", "reason", reason)
		tc.setConnected(false)
		if conn != nil {
			_ = conn.Close(websocket.StatusInternalError, reason)
		}
	})
}

// Close gracefully closes the tunnel connection.
func (tc *TunnelClient) Close() error {
	tc.setConnected(false)
	if tc.conn != nil {
		return tc.conn.Close(websocket.StatusNormalClosure, "agent shutting down")
	}
	return nil
}

// readMessage reads and decodes a single Message from the connection.
func (tc *TunnelClient) readMessage(ctx context.Context, conn *websocket.Conn) (*protocol.Message, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var msg protocol.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal message: %w", err)
	}
	return &msg, nil
}

// writeMessage encodes and writes a Message to the connection.
func (tc *TunnelClient) writeMessage(ctx context.Context, conn *websocket.Conn, msg *protocol.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	return conn.Write(ctx, websocket.MessageText, data)
}
