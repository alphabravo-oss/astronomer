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

// frameClass is the send-path priority class of an outbound frame. Everything
// the agent emits is multiplexed over one WebSocket, so the queueing policy —
// which queue a frame joins, and what happens when that queue is full — has to
// depend on what losing the frame actually costs.
type frameClass string

const (
	// frameControl is a session-lifecycle frame or the ONLY reply a server-side
	// originator will ever receive for a request it is blocked on. Losing one
	// strands a caller (up to a 10-minute helm context) or makes the agent look
	// dead. Control frames ride their own queue so bulk traffic can neither
	// starve nor collapse them, and an exhausted control queue is the one
	// remaining condition that force-closes the connection.
	frameControl frameClass = "control"
	// frameStream is per-stream data whose loss must be surfaced, not hidden:
	// a missing chunk in a chunked unary body would otherwise be reassembled
	// into a truncated HTTP 200, and a missing watch event would silently leave
	// the UI stale. Stream frames — including their terminal end frames, which stay
	// on the same queue so they can never overtake the data they terminate —
	// fail exactly one stream, never the connection.
	frameStream frameClass = "stream"
	// frameBestEffort is drop-safe telemetry: coarse invalidation hints and
	// mirror rows that a later informer event, resync, or reconnect replay
	// re-derives. These are the only frames the agent silently discards.
	frameBestEffort frameClass = "best_effort"
)

const (
	// sendQueueSize bounds the data (stream + best-effort) queue. Kept at the
	// historical 256: once producers are paced by SendBlocking the queue only
	// has to absorb a burst, not a backlog, and the memory ceiling here is set
	// by frame size rather than slot count (256 * 16 KiB stream chunks ~ 5.6
	// MiB against a 512Mi container limit). Growing it would buy latency
	// hiding, not correctness, and would multiply the worst-case footprint of
	// the large unary responses that already dominate agent memory.
	sendQueueSize = 256
	// controlQueueHeadroom is what controlQueueSize adds on top of the dispatch
	// bounds to cover the producers that are not one-reply-per-permit: the two
	// periodic health tickers, one audit batch at a time (SendAuditBatch blocks
	// on its ack), one pong per inbound heartbeat, the CONNECT frame and the
	// decommission ack.
	controlQueueHeadroom = 8
	// sendQueueWait bounds how long SendBlocking waits for queue room before
	// applying the frame's drop policy. It is the backpressure signal producers
	// feel. Deliberately an order of magnitude under the server's WebSocket
	// ping deadline (20s tick, 10s per-ping pong timeout in
	// internal/tunnel/server.go): nothing that waits here runs on readLoop or
	// holds the connection's write mutex, but keeping the bound small means even
	// a pathological chain of waits resolves long before the server reaps the
	// session. A wait also aborts immediately when the connection context is
	// cancelled, and since writeLoop now cancels that context on its own exit,
	// a dead writeLoop no longer parks producers here for the full timeout.
	sendQueueWait = 2 * time.Second
)

// replayRatePerSecond caps how fast a reconnect replay re-emits cached informer
// objects (state_subscriber.replayAll, mirror_subscriber.replayAll).
// Backpressure alone (SendBlocking) already stops a replay from overrunning the
// socket, but it does not stop a fast socket from delivering a 10k-frame burst
// to the management plane in one go. The server discards most of that anyway —
// it applies its own 500ms per-(cluster, kind, namespace) limiter to
// STATE_UPDATE (internal/tunnel/handler.go stateUpdateMinInterval) — so
// spending CPU and SSE fan-out on it is pure waste. 500/s replays a
// 10k-object cluster in ~20s, far inside the informer's own 30m resync and the
// 2-minute staleness threshold the metrics publisher uses to mark a cluster
// disconnected.
//
// A var, not a const, so tests can run a 5000-object replay without waiting out
// the production rate.
var replayRatePerSecond = 500

// controlQueueSize bounds the control queue. Unlike the data queue this is not
// a tuned number, it is DERIVED, because the control queue is the one queue
// whose exhaustion still force-closes the connection (recordSendDrop) and that
// policy is only sound while its producers are bounded by something else.
//
// They are: readLoop admits at most inflightLimit(buffered) + inflightLimit(stream)
// concurrent handlers, and each produces at most one control-class reply
// (K8S_RESPONSE, HELM_RESULT, SERVICE_PROXY_RESPONSE, RBAC_SYNC_RESULT). Sizing
// the queue at that sum plus headroom makes "the control queue cannot be filled
// by admitted work" structural instead of coincidental — a fixed 64 was already
// four times smaller than the 256 concurrent Helm ops dispatchStream admits,
// each of which replies with a control-class HELM_RESULT.
//
// It costs nothing in memory: the frames exist either way, held by their
// handler goroutines; the slot count only decides whether they queue or take
// the tunnel down.
func controlQueueSize(cfg *AgentConfig) int {
	return inflightLimit(cfg, dispatchBuffered) + inflightLimit(cfg, dispatchStream) + controlQueueHeadroom
}

// classifyFrame maps a message type to its send-path class. Unlisted types
// default to frameStream: it is the middle policy (fail one stream, don't drop
// silently, don't kill the connection), so a new message type added without
// touching this function degrades sanely rather than either vanishing or
// gaining the power to close the tunnel.
func classifyFrame(t protocol.MessageType) frameClass {
	switch t {
	case protocol.MsgConnect, protocol.MsgDisconnect, protocol.MsgHeartbeat, protocol.MsgPong,
		protocol.MsgMetrics, protocol.MsgHealthResult,
		protocol.MsgK8sResponse, protocol.MsgHelmResult, protocol.MsgHelmStatusResult,
		protocol.MsgRBACSyncResult, protocol.MsgServiceProxyResponse, protocol.MsgProxyResponse,
		protocol.MsgDecommissionAck, protocol.MsgAgentUpgradeResult, protocol.MsgError,
		protocol.MsgApiserverAudit, protocol.MsgDesiredStateRequest, protocol.MsgApplyStatus:
		return frameControl
	case protocol.MsgStateUpdate, protocol.MsgMirrorEvent:
		return frameBestEffort
	default:
		return frameStream
	}
}

// TunnelClient manages the WebSocket connection to the server.
type TunnelClient struct {
	config   *AgentConfig
	conn     *websocket.Conn
	log      *slog.Logger
	handlers map[protocol.MessageType]MessageHandler
	// sendCh carries stream + best-effort frames; controlCh carries control
	// frames. writeLoop drains controlCh with strict priority, which is what
	// makes "a saturated data path can never collapse the control channel"
	// structural rather than a matter of timing.
	sendCh    chan *protocol.Message
	controlCh chan *protocol.Message

	// inflightBuffered and inflightStreams are counting semaphores over the
	// handler goroutines readLoop spawns, one per dispatch class (see
	// inflight.go). Without them a peer that sends faster than the agent
	// handles gets one goroutine per message, each free to buffer a full
	// response body — two concurrent large LISTs already exceed the 512Mi
	// container limit, and an OOMKilled agent takes its cluster unmanaged.
	inflightBuffered chan struct{}
	inflightStreams  chan struct{}

	mu        sync.RWMutex
	connected bool
	// onConnChange (M4) is fired on every connect/disconnect transition so the
	// readiness reporter reflects live tunnel state. Guarded by mu.
	onConnChange func(bool)

	// auditIngestToken is the scoped clusters:write API token the server
	// delivers in CONNECT_ACK for PATH A HTTP audit delivery. Empty until/
	// unless the server issues one. Guarded by mu.
	auditIngestToken string

	// failCloseOnce ensures the buffer-full eager close only
	// fires once per connection — repeated congestion shouldn't
	// hammer tc.conn.Close. Reset by dial() on each new connection.
	failCloseOnce *sync.Once

	// auditAcks holds the pending apiserver-audit ack waiters, keyed by
	// BatchID. tunnelAuditSender registers a channel before sending a batch and
	// blocks on it; readLoop routes the matching MsgApiserverAuditAck to it.
	// Guarded by auditAcksMu. On disconnect, failAuditAckWaiters drains every
	// pending waiter so the blocked sender returns an error and holds its
	// checkpoint rather than waiting out its full timeout.
	auditAcksMu sync.Mutex
	auditAcks   map[string]chan protocol.ApiserverAuditAckPayload
	// auditAckTimeout bounds the SendAuditBatch wait. Zero means use the
	// package default (auditAckTimeout const); set by tests to a short value.
	auditAckTimeout time.Duration

	// tokenPersister is invoked before a CONNECT_ACK-delivered durable token is
	// activated in memory. pendingAgentToken survives reconnect attempts within
	// this process when Kubernetes persistence is temporarily unavailable; the
	// old credential remains active and therefore is never falsely adopted.
	tokenPersister    func(context.Context, *AgentConfig, string) error
	pendingAgentToken string
}

// NewTunnelClient creates a new tunnel client with the given configuration.
func NewTunnelClient(cfg *AgentConfig, log *slog.Logger) *TunnelClient {
	return &TunnelClient{
		config:           cfg,
		log:              log,
		handlers:         make(map[protocol.MessageType]MessageHandler),
		sendCh:           make(chan *protocol.Message, sendQueueSize),
		controlCh:        make(chan *protocol.Message, controlQueueSize(cfg)),
		inflightBuffered: make(chan struct{}, inflightLimit(cfg, dispatchBuffered)),
		inflightStreams:  make(chan struct{}, inflightLimit(cfg, dispatchStream)),
		failCloseOnce:    &sync.Once{},
		auditAcks:        make(map[string]chan protocol.ApiserverAuditAckPayload),
		tokenPersister:   persistRotatedToken,
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
	listener := tc.onConnChange
	tc.mu.Unlock()
	// M4: notify the readiness reporter on EVERY transition (connect AND
	// disconnect) so /readyz reflects the live tunnel state instead of a flag
	// that was latched true on first connect and never reset on drop.
	if listener != nil {
		listener(v)
	}
}

// SetConnectionListener registers a callback invoked on every tunnel
// connect/disconnect transition. Set once at startup; nil-safe.
func (tc *TunnelClient) SetConnectionListener(fn func(bool)) {
	tc.mu.Lock()
	tc.onConnChange = fn
	tc.mu.Unlock()
}

// Connect establishes the WebSocket connection and runs the read/write loops.
// It blocks until ctx is cancelled or a fatal error occurs.
func (tc *TunnelClient) Connect(ctx context.Context) error {
	if err := tc.dial(ctx); err != nil {
		// L20: an initial-connect failure must NOT be fatal — exiting here lands
		// the agent in CrashLoopBackOff (5-min kubelet backoff + an alarming pod
		// status) during the join window, when the server may simply not be
		// reachable yet. Fall into the SAME jittered reconnect loop a mid-session
		// drop uses; only ctx cancellation ends it. Mirrors connect2/localcluster.
		tc.log.Warn("initial connection failed; entering reconnect loop", "error", err)
		if rerr := tc.reconnectLoop(ctx); rerr != nil {
			return rerr // only returns on ctx cancel
		}
	}

	tc.run(ctx)
	return nil
}

// dial performs the WebSocket handshake and the CONNECT/CONNECT_ACK exchange.
func (tc *TunnelClient) dial(ctx context.Context) error {
	if err := tc.persistPendingAgentToken(ctx); err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v1/ws/agent/tunnel/%s/", tc.config.ServerURL, tc.config.ClusterID)
	tc.log.Info("dialing server", "url", url)

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+tc.config.AgentToken)

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	dialOpts := &websocket.DialOptions{
		HTTPHeader: headers,
	}
	// Server-CA pinning: when a CA bundle/checksum is configured, dial through
	// an http.Client whose transport carries the pinned tls.Config. When none
	// is configured BuildTLSConfig returns nil and we leave HTTPClient unset, so
	// websocket.Dial uses its default OS-trust transport (no behavior change).
	tlsCfg, err := BuildTLSConfig(tc.config.CACert, tc.config.CAChecksum)
	if err != nil {
		return fmt.Errorf("build tls config: %w", err)
	}
	if tlsCfg != nil {
		dialOpts.HTTPClient = &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		}
	}

	conn, _, err := websocket.Dial(dialCtx, url, dialOpts)
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
	if migrated, err := tc.persistAcceptedAgentToken(ctx, ack.AgentToken); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "durable credential persistence failed")
		return err
	} else if migrated {
		tc.log.Info("rotated durable agent token")
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

// persistAcceptedAgentToken runs only after Accepted=true. This ordering is the
// cluster-binding proof that permits bootstrap/legacy material to migrate into
// active identity even when the server returns an empty or identical ACK token.
func (tc *TunnelClient) persistAcceptedAgentToken(ctx context.Context, ackToken string) (bool, error) {
	if tc == nil || tc.config == nil {
		return false, fmt.Errorf("agent config is required")
	}
	if tc.config.CredentialSource == credentialSourceBootstrap && (ackToken == "" || ackToken == tc.config.AgentToken) {
		return false, fmt.Errorf("accepted bootstrap connection did not provide a distinct durable agent credential")
	}
	durableToken := ackToken
	if durableToken == "" {
		durableToken = tc.config.AgentToken
	}
	legacyImageFirst := tc.config.CredentialSource == credentialSourceLegacy && tc.config.LegacyLayoutConfigured
	needsIdentityMigration := tc.config.CredentialSource == credentialSourceBootstrap || (tc.config.CredentialSource == credentialSourceLegacy && !legacyImageFirst)
	needsRotation := ackToken != "" && ackToken != tc.config.AgentToken
	if tc.config.CredentialSource == CredentialSourceEnvironment {
		if needsRotation {
			tc.config.AgentToken = ackToken
		}
		return needsRotation, nil
	}
	if !needsIdentityMigration && !needsRotation {
		return false, nil
	}
	if err := tc.persistAndActivateAgentToken(ctx, durableToken); err != nil {
		return false, err
	}
	return true, nil
}

func (tc *TunnelClient) persistAndActivateAgentToken(ctx context.Context, token string) error {
	if tc == nil || tc.config == nil || token == "" {
		return fmt.Errorf("durable agent credential is required")
	}
	persist := tc.tokenPersister
	if persist == nil {
		persist = persistRotatedToken
	}
	writeCtx, cancel := context.WithTimeout(ctx, credentialWriteTimeout)
	defer cancel()
	if err := persist(writeCtx, tc.config, token); err != nil {
		tc.pendingAgentToken = token
		return fmt.Errorf("persist durable agent credential before activation: %w", err)
	}
	tc.config.AgentToken = token
	if !usesLegacyCredentialStorage(tc.config) {
		tc.config.CredentialSource = CredentialSourceIdentity
	}
	tc.pendingAgentToken = ""
	return nil
}

func (tc *TunnelClient) persistPendingAgentToken(ctx context.Context) error {
	if tc == nil || tc.pendingAgentToken == "" {
		return nil
	}
	if err := tc.persistAndActivateAgentToken(ctx, tc.pendingAgentToken); err != nil {
		return fmt.Errorf("retry pending durable agent credential persistence: %w", err)
	}
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
			// Mirror readLoop: writeLoop only returns on loopCtx cancellation or
			// on a write error, and a write error means this connection is dead.
			// Without this cancel loopCtx stays live, sendCh is never drained
			// again, and wg.Wait() blocks until readLoop independently returns —
			// which only happens once the peer or failClose closes the socket.
			// On the ordinary shutdown route (loopCtx already cancelled by
			// readLoop or by the parent ctx) cancelling again is a no-op, so this
			// cannot tear the connection down early.
			loopCancel()
		}()

		wg.Wait()
		loopCancel()
		tc.setConnected(false)
		// Wake any apiserver-audit sender blocked on an ack for this now-dead
		// connection so it returns an error and holds its checkpoint instead of
		// waiting out its full timeout.
		tc.failAuditAckWaiters()

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
			// Non-blocking on purpose: readLoop is the only reader, and
			// stalling it stops pongs and gets the agent reaped.
			if err := tc.Send(pong); err != nil {
				tc.log.Error("failed to send pong", "error", err)
			}
			continue
		}

		if msg.Type == protocol.MsgApiserverAuditAck {
			tc.routeAuditAck(msg)
			continue
		}

		handler, ok := tc.handlers[msg.Type]
		if !ok {
			tc.log.Warn("no handler for message type", "type", msg.Type)
			continue
		}

		// Bound the dispatch. acquireDispatch never blocks, so readLoop keeps
		// draining the socket at full rate whatever the handlers are doing:
		// this is the only reader, and a stalled one stops pongs and gets the
		// agent reaped by the server's ping deadline. Over-limit messages are
		// answered immediately with a retryable rejection instead.
		class := classifyDispatch(msg.Type)
		if !tc.acquireDispatch(class) {
			tc.shedInbound(msg, class)
			continue
		}

		// The permit is handed to the handler on its context so a handler whose
		// real work outlives the call (LOG_START, EXEC_START) can transfer it to
		// the session goroutine; see dispatchPermit.
		permit := newDispatchPermit(func() { tc.releaseDispatch(class) })
		dispatchCtx := withDispatchPermit(ctx, permit)

		go func(m *protocol.Message, c dispatchClass, p *dispatchPermit) {
			defer p.finishHandler()
			resp, err := handler(dispatchCtx, m)
			synthesized := false
			if err != nil {
				tc.log.Error("handler error", "type", m.Type, "error", err)
				resp = handlerErrorReply(m, err)
				synthesized = true
			}
			if resp == nil {
				return
			}
			if sendErr := tc.sendHandlerReply(dispatchCtx, resp, c, synthesized); sendErr != nil {
				tc.log.Error("failed to send response", "error", sendErr)
			}
		}(msg, class, permit)
	}
}

// handlerErrorReply renders a handler error as a routable frame. StreamID is
// echoed so the server-side originator can match it to the request it is
// blocked on.
func handlerErrorReply(msg *protocol.Message, err error) *protocol.Message {
	payload, _ := json.Marshal(protocol.ErrorPayload{
		Code:    "HANDLER_ERROR",
		Message: err.Error(),
	})
	return &protocol.Message{
		Type:      protocol.MsgError,
		StreamID:  msg.StreamID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

// sendHandlerReply queues a handler's reply, choosing the send-path class from
// the DISPATCH class the request was admitted under rather than from the reply's
// own type. It blocks: this always runs on the handler's own goroutine, never on
// readLoop, and a handler reply is the only answer a server-side originator will
// get.
//
// A synthesized error reply for an EXEMPT message is deliberately forced onto
// the DATA queue. MsgError classifies as frameControl, and an exhausted control
// queue force-closes the tunnel — a policy that is only sound for the bounded
// producers the queue is sized for (one reply per in-flight permit). Exempt
// types are ungated by construction, and ExecHandler.HandleExecInput returns an
// error for EVERY frame naming an unknown stream, so a run of EXEC_INPUT /
// EXEC_RESIZE for a dead session is an unbounded producer. On the control queue
// that would turn a congested socket into the teardown of every watch, exec
// session and heartbeat on the connection — precisely what shedInbound refuses
// to do, one branch away. On the data queue the reply is counted and dropped,
// costing that one originator its own timeout and nothing else.
//
// A real (non-synthesized) reply keeps its own class: those are produced one per
// permit even for exempt types (DECOMMISSION_ACK, AGENT_UPGRADE_RESULT), and
// they are exactly the frames the control queue exists to protect.
func (tc *TunnelClient) sendHandlerReply(ctx context.Context, resp *protocol.Message, class dispatchClass, synthesized bool) error {
	if synthesized && class == dispatchExempt {
		return tc.enqueueClass(ctx, resp, frameStream, sendQueueWait)
	}
	return tc.SendBlocking(ctx, resp)
}

// writeLoop sends messages from the send channels to the WebSocket, draining
// controlCh with strict priority over sendCh. Control frames come from a
// bounded set of producers (one reply per in-flight request plus the health
// tickers), so the priority cannot starve data traffic; it only guarantees that
// a heartbeat or a request reply never queues behind a bulk replay.
func (tc *TunnelClient) writeLoop(ctx context.Context) {
	for {
		// Non-blocking control drain first. When controlCh is empty this falls
		// straight through to the combined select below, which blocks.
		select {
		case <-ctx.Done():
			return
		case msg := <-tc.controlCh:
			if !tc.writeQueued(ctx, msg) {
				return
			}
			continue
		default:
		}

		select {
		case <-ctx.Done():
			return
		case msg := <-tc.controlCh:
			if !tc.writeQueued(ctx, msg) {
				return
			}
		case msg := <-tc.sendCh:
			if !tc.writeQueued(ctx, msg) {
				return
			}
		}
	}
}

// writeQueued writes one queued frame. It returns false when writeLoop must
// return, which run() turns into a full connection teardown + re-dial.
func (tc *TunnelClient) writeQueued(ctx context.Context, msg *protocol.Message) bool {
	if err := tc.writeMessage(ctx, tc.conn, msg); err != nil {
		if ctx.Err() != nil {
			return false
		}
		tc.log.Error("write error", "error", err)
		return false
	}
	return true
}

// Send queues a message without blocking, applying the frame's class policy if
// its queue is full. Retained for producers that must never block — above all
// readLoop's pong, since stalling the reader stops pongs and gets the agent
// reaped by the server. Everything with a context should use SendBlocking so
// backpressure reaches the producer instead of becoming a drop.
func (tc *TunnelClient) Send(msg *protocol.Message) error {
	return tc.enqueue(nil, msg, 0)
}

// SendBlocking queues a message, waiting up to sendQueueWait for room. This is
// the backpressure path: a producer that cannot get a slot is slowed to the
// rate the socket actually drains rather than discarding work or taking the
// connection down. It returns as soon as ctx is done, so a producer bound to
// the connection context stops immediately when the connection dies instead of
// waiting out the full timeout.
func (tc *TunnelClient) SendBlocking(ctx context.Context, msg *protocol.Message) error {
	return tc.enqueue(ctx, msg, sendQueueWait)
}

// SendFunc returns a ctx-bound sender in the fire-and-forget
// func(*protocol.Message) error shape the streaming handlers and the health /
// reconcile loops already take. Handlers keep their signatures and gain
// backpressure plus per-connection cancellation for free.
func (tc *TunnelClient) SendFunc(ctx context.Context) func(*protocol.Message) error {
	return func(msg *protocol.Message) error {
		return tc.SendBlocking(ctx, msg)
	}
}

// enqueue places msg on the queue for the class its type maps to.
func (tc *TunnelClient) enqueue(ctx context.Context, msg *protocol.Message, wait time.Duration) error {
	return tc.enqueueClass(ctx, msg, classifyFrame(msg.Type), wait)
}

// enqueueClass places msg on the queue for an explicitly chosen class. wait <= 0
// (or a nil ctx) means try once and give up — the non-blocking Send path.
//
// The class is a parameter rather than always derived from msg.Type because one
// caller legitimately disagrees with the type's default: shedInbound sends
// control-typed rejection frames that are produced at the peer's send rate, and
// unbounded producers must stay off the control queue whose exhaustion policy is
// to close the connection.
func (tc *TunnelClient) enqueueClass(ctx context.Context, msg *protocol.Message, class frameClass, wait time.Duration) error {
	q := tc.sendCh
	if class == frameControl {
		q = tc.controlCh
	}

	select {
	case q <- msg:
		agentTunnelSendQueueDepth.WithLabelValues(observability.MetricValues(string(class))...).Set(float64(len(q)))
		return nil
	default:
	}

	if wait > 0 && ctx != nil {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case q <- msg:
			agentTunnelSendQueueDepth.WithLabelValues(observability.MetricValues(string(class))...).Set(float64(len(q)))
			return nil
		case <-ctx.Done():
			return tc.recordSendDrop(class, msg, "tunnel_closed", ctx.Err())
		case <-timer.C:
		}
	}
	return tc.recordSendDrop(class, msg, "channel_full", nil)
}

// recordSendDrop counts an undeliverable frame and applies its class policy.
//
// Only an exhausted CONTROL queue force-closes the connection, and only while
// we still believe we are connected. A control frame is either session
// lifecycle or the one reply a server-side originator is blocked on, so
// dropping it silently would leave that caller waiting out its own context (10
// minutes for helm); the eager close makes the server's CloseAll wake every
// in-flight stream at once and the agent re-dials.
//
// Stream and best-effort frames deliberately do NOT close the connection: their
// loss is a per-stream or per-hint failure, and taking down a channel shared by
// every watch, exec session, heartbeat and decommission RPC is a far larger
// outage than the frame is worth. This is safe to relax now that writeLoop
// cancels the connection context on its own exit — failClose is no longer the
// only recovery path for a dead writeLoop.
func (tc *TunnelClient) recordSendDrop(class frameClass, msg *protocol.Message, reason string, cause error) error {
	observability.RecordDroppedEvent("agent_tunnel_send", reason)
	agentTunnelSendDroppedTotal.WithLabelValues(observability.MetricValues(string(class), reason)...).Inc()

	if class == frameControl && reason == "channel_full" && tc.IsConnected() {
		// failCloseOnce dedupes repeated congestion within the same connection;
		// dial() resets it on the next attempt.
		go tc.failClose("control queue full")
		return fmt.Errorf("control queue full, closing tunnel; type=%s", msg.Type)
	}
	if cause != nil {
		return fmt.Errorf("tunnel send aborted; type=%s: %w", msg.Type, cause)
	}
	// Deliberately not logged here: this is the path that fires under sustained
	// congestion, so a line per frame would be its own flood. The counter above
	// is the signal, and every caller already logs its own drop with context.
	return fmt.Errorf("send queue full, dropped %s frame; type=%s", class, msg.Type)
}

// registerAuditAck creates and stores a pending ack channel for batchID. The
// channel is buffered (size 1) so routeAuditAck never blocks delivering the ack
// even if the waiter has already given up. Returns a cleanup func the caller
// MUST defer to remove the entry on ack or timeout.
func (tc *TunnelClient) registerAuditAck(batchID string) (<-chan protocol.ApiserverAuditAckPayload, func()) {
	ch := make(chan protocol.ApiserverAuditAckPayload, 1)
	tc.auditAcksMu.Lock()
	tc.auditAcks[batchID] = ch
	tc.auditAcksMu.Unlock()
	return ch, func() {
		tc.auditAcksMu.Lock()
		delete(tc.auditAcks, batchID)
		tc.auditAcksMu.Unlock()
	}
}

// routeAuditAck delivers a MsgApiserverAuditAck to the sender waiting on its
// BatchID. An ack for an unknown BatchID (already timed-out / cleaned up) is
// dropped.
func (tc *TunnelClient) routeAuditAck(msg *protocol.Message) {
	var ack protocol.ApiserverAuditAckPayload
	if err := json.Unmarshal(msg.Payload, &ack); err != nil {
		tc.log.Warn("invalid APISERVER_AUDIT_ACK payload", "error", err)
		return
	}
	tc.auditAcksMu.Lock()
	ch, ok := tc.auditAcks[ack.BatchID]
	tc.auditAcksMu.Unlock()
	if !ok {
		tc.log.Debug("APISERVER_AUDIT_ACK for unknown batch", "batch_id", ack.BatchID)
		return
	}
	// Non-blocking: ch is buffered and the waiter consumes at most one ack.
	select {
	case ch <- ack:
	default:
	}
}

// failAuditAckWaiters delivers a synthetic OK=false ack to every pending
// apiserver-audit waiter, used on disconnect so blocked senders return an error
// and hold their checkpoint immediately. The map is reset so the same waiters
// aren't signalled twice.
func (tc *TunnelClient) failAuditAckWaiters() {
	tc.auditAcksMu.Lock()
	waiters := tc.auditAcks
	tc.auditAcks = make(map[string]chan protocol.ApiserverAuditAckPayload)
	tc.auditAcksMu.Unlock()
	for batchID, ch := range waiters {
		select {
		case ch <- protocol.ApiserverAuditAckPayload{BatchID: batchID, OK: false, Error: "tunnel disconnected"}:
		default:
		}
	}
}

// auditAckTimeout bounds how long a tunnelAuditSender blocks waiting for the
// server's MsgApiserverAuditAck before giving up and reporting the batch
// un-acked (so the tailer holds its checkpoint and re-forwards).
const auditAckTimeout = 30 * time.Second

// SendAuditBatch sends a MsgApiserverAudit frame tagged with batchID and BLOCKS
// until the server acks it (OK=true → nil), the server reports a persist failure
// (OK=false → error), the bounded auditAckTimeout elapses, or the tunnel
// disconnects (failAuditAckWaiters wakes us). Returning a non-nil error keeps
// the AuditTailer's checkpoint pinned so the idempotent ingest re-receives the
// batch. Implements the ack-before-checkpoint contract used by tunnelAuditSender.
func (tc *TunnelClient) SendAuditBatch(ctx context.Context, batchID string, payload []byte) error {
	ackCh, cleanup := tc.registerAuditAck(batchID)
	defer cleanup()

	msg := &protocol.Message{
		Type:      protocol.MsgApiserverAudit,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
	if err := tc.SendBlocking(ctx, msg); err != nil {
		return err
	}

	wait := tc.auditAckTimeout
	if wait <= 0 {
		wait = auditAckTimeout
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("apiserver-audit ack timeout for batch %s", batchID)
	case ack := <-ackCh:
		if !ack.OK {
			if ack.Error != "" {
				return fmt.Errorf("apiserver-audit batch %s rejected: %s", batchID, ack.Error)
			}
			return fmt.Errorf("apiserver-audit batch %s rejected", batchID)
		}
		return nil
	}
}

// failClose force-closes the WebSocket once per connection. Used ONLY when the
// control queue is saturated, so the server detects the failure immediately
// instead of waiting out the originator's context. Data-path congestion no
// longer reaches here — see recordSendDrop.
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
