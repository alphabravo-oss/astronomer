// Package tunnel implements the server side of the multiplexed JSON
// WebSocket tunnel between the management server and remote cluster agents.
//
// Liveness model
// --------------
// The agent is the SOLE originator of HEARTBEAT messages. The agent emits a
// HEARTBEAT periodically (config.HeartbeatInterval, default 30s) carrying
// node/pod count and lightweight cluster metadata, and a separate METRICS
// frame on a slower ticker (config.MetricsInterval, default 60s).
//
// The server does NOT actively ping the agent. If the WebSocket read goroutine
// is silent for longer than the underlying TCP keepalive (handled by
// github.com/coder/websocket and the OS), the connection is torn down. Database
// liveness is updated whenever a HEARTBEAT lands (see handleHeartbeat).
//
// PONG is reserved for cases where the SERVER explicitly sends a HEARTBEAT
// (e.g. for a future server-initiated probe); the agent's readLoop already
// replies with PONG. Today the server never originates HEARTBEAT, so PONG
// frames should not appear on the wire — the handlePong handler exists only
// to avoid logging spurious "unknown message type" warnings if a future
// version of either side starts probing.
package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/agentcompat"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel/connectauth"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	// connectTimeout is the maximum time to wait for the initial CONNECT message.
	connectTimeout = 10 * time.Second

	// writeTimeout is the maximum time to wait for a write to complete.
	writeTimeout = 10 * time.Second

	// sendChannelSize is the buffer size for the per-agent send channel.
	sendChannelSize = 256

	// A4 audit actions. Both satisfy the audit-action contract regex
	// (^[a-z]+(\.[a-z0-9_]+)+$); the contract test scans internal/handler/*.go
	// only, so — like the existing agent.token.* tunnel actions — there is
	// nothing to register in a central catalog.
	actionAgentConnected  = "agent.connected"
	actionAgentAuthFailed = "agent.auth_failed"
)

// AgentConnection represents a connected agent.
type AgentConnection struct {
	ClusterID    string
	AgentID      string
	AgentVersion string
	SessionID    string
	DBID         uuid.UUID
	Conn         *websocket.Conn
	Streams      *StreamManager
	cancel       context.CancelFunc
	sendCh       chan *protocol.Message
}

// Hub manages all connected agent tunnels.
//
// The agent map is sharded; only the hub's other
// mutable state (publisher, stateLim init) is guarded by Hub.mu. Most
// hot paths — SendToAgent, GetAgent, register/unregister — hit only
// the agent shard's lock, so SendToAgent("A") and SendToAgent("B")
// contend only if their clusterIDs hash to the same shard (1/16).
type Hub struct {
	mu        sync.RWMutex // protects publisher + stateLim + mirror only
	agents    *shardedAgents
	log       *slog.Logger
	validator AgentTokenValidator
	// publisher receives connect/disconnect/heartbeat lifecycle events for
	// fan-out to SSE subscribers. Optional; nil-safe.
	publisher LifecyclePublisher
	// regAdvancer is the wizard phase machine. Optional; when set,
	// the hub calls OnAgentConnected on first CONNECT_ACK so the
	// cluster advances `awaiting_agent` → `connected` automatically.
	regAdvancer RegistrationAdvancer
	// stateLim collapses redundant STATE_UPDATE fan-outs at the (cluster_id,
	// kind, namespace) granularity. Lazily constructed on first use so the
	// hub remains zero-value-safe.
	stateLim *stateUpdateLimiter
	// mirror routes sprint-069 MsgMirrorEvent frames into the
	// mirrored_* DB tables. Optional; nil-safe so existing tests that
	// don't wire CRD-mirror v2 stay green.
	mirror MirrorIngester
	// auditPersister persists APISERVER_AUDIT batches under the
	// authenticated session's cluster ID. Optional; nil-safe so installs
	// without apiserver-audit wiring drop the frames.
	auditPersister AuditPersister
	// locator publishes the cluster_id → this-pod address mapping into
	// redis on each accepted WS so sibling replicas can reverse-proxy
	// to us. Optional: nil collapses to single-pod behavior (the proxy
	// 503s when its local Hub doesn't hold the agent).
	locator *Locator
	// ingestIssuer mints the scoped apiserver-audit ingest token (PATH A)
	// delivered in CONNECT_ACK. Optional; nil-safe so installs that don't
	// wire it simply never issue a token (the agent falls back to tunnel
	// delivery). The issuer get-or-creates the reserved service identity +
	// cluster-scoped clusters:update grant and mints a fresh scoped token.
	ingestIssuer AuditIngestIssuer
	// desiredState renders the Fleet-style PULL desired state for a cluster in
	// response to a MsgDesiredStateRequest. Optional; nil-safe so installs that
	// don't wire the pull subsystem reply with an ERROR frame ("desired state
	// not available") and the agent falls back to its existing paths.
	desiredState DesiredStateProvider
	// connLimiter throttles tunnel CONNECT attempts by SOURCE IP after repeated
	// auth FAILURES (A4 / M5). Optional; nil-safe so test hubs stay unthrottled.
	// Shared with the tunnel2 /connect path so both connect surfaces present one
	// cross-path per-IP view.
	connLimiter *ConnectFailureLimiter
	// clockSkew bounds the allowed drift between the CONNECT envelope timestamp
	// and the server clock (A4 / L13). <=0 disables the replay check.
	clockSkew time.Duration
}

// SetConnectLimiter wires the A4 connect failure-limiter and the timestamp
// replay skew window (set once at startup). Nil-safe: a nil limiter leaves the
// connect path unthrottled, and skew<=0 disables the replay check.
func (h *Hub) SetConnectLimiter(lim *ConnectFailureLimiter, skew time.Duration) {
	h.mu.Lock()
	h.connLimiter = lim
	h.clockSkew = skew
	h.mu.Unlock()
}

// connectLimiter returns the wired limiter (or nil) under the read lock.
func (h *Hub) connectLimiter() *ConnectFailureLimiter {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.connLimiter
}

// connectClockSkew returns the configured replay window under the read lock.
func (h *Hub) connectClockSkew() time.Duration {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clockSkew
}

// connectTimestampOutsideSkew is the L13 replay predicate. It returns true only
// when the CONNECT envelope timestamp is wildly out of range and must be
// rejected. Two leniency guards keep honest agents safe: skew<=0 disables the
// check, and a zero timestamp (older agent that never stamps the envelope) is
// never rejected. The window is symmetric — stale replays (now-ts > skew) and
// far-future clocks (now-ts < -skew) are both caught.
func connectTimestampOutsideSkew(now, ts time.Time, skew time.Duration) bool {
	if skew <= 0 || ts.IsZero() {
		return false
	}
	d := now.Sub(ts)
	return d > skew || d < -skew
}

// ConnectClientIP derives the canonical client IP for the tunnel upgrade. It
// reuses middleware.RemoteIPAddr (XFF-first / X-Real-IP / host-of-RemoteAddr —
// the same trust policy every audited HTTP request uses) so the limiter key and
// the audited Event.IPAddress agree. Returns a non-empty key always ("unknown"
// when nothing parses) so the limiter never collapses distinct clients.
func ConnectClientIP(r *http.Request) (string, *netip.Addr) {
	addr := middleware.RemoteIPAddr(r)
	if addr != nil {
		return addr.String(), addr
	}
	if r != nil && r.RemoteAddr != "" {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
			return host, nil
		}
		return r.RemoteAddr, nil
	}
	return "unknown", nil
}

// DesiredStateProvider renders the desired-state manifest set for a cluster.
// Satisfied by the server-side adapter that reuses the agent manifest renderer
// + baseline registry. Kept as an interface so the tunnel package does not
// import internal/handler (which would create an import cycle).
type DesiredStateProvider interface {
	DesiredState(ctx context.Context, clusterID string, currentRevision string) (protocol.DesiredStateResponsePayload, error)
}

// SetDesiredStateProvider attaches the PULL desired-state renderer (set once at
// startup). Nil-safe.
func (h *Hub) SetDesiredStateProvider(p DesiredStateProvider) {
	h.mu.Lock()
	h.desiredState = p
	h.mu.Unlock()
}

// desiredStateProvider returns the wired provider (or nil) under the read lock.
func (h *Hub) desiredStateProvider() DesiredStateProvider {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.desiredState
}

// AuditIngestIssuer mints the per-cluster scoped apiserver-audit ingest token
// delivered in CONNECT_ACK (PATH A). Satisfied by *sqlc.Queries via
// auth.IssueAgentIngestToken. Kept as an interface so the tunnel package
// doesn't grow a hard dependency on the issuance internals.
type AuditIngestIssuer interface {
	IssueIngestToken(ctx context.Context, clusterID uuid.UUID) (string, error)
}

// SetAuditIngestIssuer attaches the PATH A ingest-token issuer (set once at
// startup). Nil-safe.
func (h *Hub) SetAuditIngestIssuer(i AuditIngestIssuer) {
	h.mu.Lock()
	h.ingestIssuer = i
	h.mu.Unlock()
}

// SetLocator wires the cross-pod agent locator (set once at startup).
// nil-safe.
func (h *Hub) SetLocator(l *Locator) {
	h.mu.Lock()
	h.locator = l
	h.mu.Unlock()
}

// Locator returns the wired locator (or nil). ProxyHandler uses this
// for the cross-pod fallback when its local Hub doesn't own the agent.
func (h *Hub) Locator() *Locator {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.locator
}

// MirrorIngester is the narrow handler-side interface the Hub calls
// when an agent emits a MIRROR_EVENT frame. The implementation lives in
// internal/crd (RouteMirrorEvent) and depends on *sqlc.Queries; we keep
// the surface as an interface so the tunnel package doesn't import
// internal/crd directly (which would drag controller-runtime into the
// tunnel build path).
type MirrorIngester interface {
	RouteMirrorEvent(ctx context.Context, clusterID uuid.UUID, payload protocol.MirrorEventPayload) error
}

// SetMirrorIngester attaches the sprint-069 mirror router (set once at
// startup). Nil-safe.
func (h *Hub) SetMirrorIngester(m MirrorIngester) {
	h.mu.Lock()
	h.mirror = m
	h.mu.Unlock()
}

// AuditPersister is the narrow handler-side interface the Hub calls when an
// agent emits an APISERVER_AUDIT frame. It is satisfied by
// *handler.ApiserverAuditHandler.PersistAuditEvents. Kept as an interface so
// the tunnel package doesn't import internal/handler (which would import-cycle
// via the route wiring). The cluster ID passed in is always the AUTHENTICATED
// session's, never anything from the agent's payload.
type AuditPersister interface {
	PersistAuditEvents(ctx context.Context, clusterID uuid.UUID, events []json.RawMessage) (accepted, skipped int, err error)
}

// SetAuditPersister attaches the apiserver-audit persister (set once at
// startup). Nil-safe.
func (h *Hub) SetAuditPersister(p AuditPersister) {
	h.mu.Lock()
	h.auditPersister = p
	h.mu.Unlock()
}

// LifecyclePublisher is the interface the events bus implements; the tunnel
// package depends on the interface, not the bus, to keep import direction clean.
type LifecyclePublisher interface {
	Publish(eventType string, data any)
}

// RegistrationAdvancer is the slice of the registration.Service surface
// the hub needs. Declared as an interface here so the tunnel package
// doesn't pull in internal/registration as a hard dependency (which
// would import-cycle since registration depends on sqlc and could
// later depend on tunnel for a metrics hook).
type RegistrationAdvancer interface {
	OnAgentConnected(ctx context.Context, clusterID uuid.UUID, agentVersion string) error
}

// SetPublisher attaches a lifecycle publisher (set once at startup).
func (h *Hub) SetPublisher(p LifecyclePublisher) {
	h.mu.Lock()
	h.publisher = p
	h.mu.Unlock()
}

// SetRegistrationAdvancer wires the wizard phase machine. When set,
// the first heartbeat from a cluster in `awaiting_agent` advances it
// to `connected` (and on through `ready`/`provisioning` per the
// install_baseline choice).
func (h *Hub) SetRegistrationAdvancer(a RegistrationAdvancer) {
	h.mu.Lock()
	h.regAdvancer = a
	h.mu.Unlock()
}

func (h *Hub) publish(eventType string, clusterID, sessionID, agentVersion string) {
	h.mu.RLock()
	p := h.publisher
	h.mu.RUnlock()
	if p == nil {
		return
	}
	p.Publish(eventType, map[string]any{
		"cluster_id":    clusterID,
		"session_id":    sessionID,
		"agent_version": agentVersion,
	})
}

type AgentTokenValidator interface {
	GetRegistrationTokenByToken(ctx context.Context, token string) (sqlc.ClusterRegistrationToken, error)
	MarkRegistrationTokenUsed(ctx context.Context, id uuid.UUID) error
	GetClusterAgentTokenByClusterID(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterAgentToken, error)
	GetClusterAgentTokenByToken(ctx context.Context, token string) (sqlc.ClusterAgentToken, error)
	UpsertClusterAgentToken(ctx context.Context, arg sqlc.UpsertClusterAgentTokenParams) (sqlc.ClusterAgentToken, error)
	TouchClusterAgentToken(ctx context.Context, id uuid.UUID) error
	// MarkClusterAgentTokenAdopted (task A3) stamps adopted_at on the first
	// CONNECT that authenticates with the durable token. Anchors the
	// registration-token replay gate in validateAndMaybeRotateToken.
	MarkClusterAgentTokenAdopted(ctx context.Context, id uuid.UUID) error
	// Rotation grace (task A2). RotateClusterAgentToken performs the
	// mint-fresh / move-old-to-previous step atomically when a rotation is
	// pending; ClearPreviousClusterAgentTokenHash retires the old hash once
	// the agent reconnects with the new token.
	RotateClusterAgentToken(ctx context.Context, arg sqlc.RotateClusterAgentTokenParams) (sqlc.ClusterAgentToken, error)
	ClearPreviousClusterAgentTokenHash(ctx context.Context, id uuid.UUID) error
	UpdateClusterHeartbeat(ctx context.Context, arg sqlc.UpdateClusterHeartbeatParams) error
	UpsertClusterHealthStatus(ctx context.Context, arg sqlc.UpsertClusterHealthStatusParams) (sqlc.ClusterHealthStatus, error)
	// TouchClusterMetricsSample (C3 / M13) stamps last_metrics_at when a
	// non-empty metrics SAMPLE arrives. Called only from handleMetrics, so
	// last_metrics_at tracks real metrics frames separately from last_check.
	TouchClusterMetricsSample(ctx context.Context, clusterID uuid.UUID) error
	CreateAgentConnection(ctx context.Context, arg sqlc.CreateAgentConnectionParams) (sqlc.AgentConnection, error)
	DisconnectActiveConnectionsByCluster(ctx context.Context, clusterID uuid.UUID) error
	UpdateAgentConnectionStatus(ctx context.Context, arg sqlc.UpdateAgentConnectionStatusParams) error
	UpdateAgentConnectionPing(ctx context.Context, id uuid.UUID) error
	ClaimPendingAgentLifecycleOperation(ctx context.Context, clusterID uuid.UUID) (sqlc.AgentLifecycleOperation, error)
	CompleteAgentLifecycleOperation(ctx context.Context, arg sqlc.CompleteAgentLifecycleOperationParams) (sqlc.AgentLifecycleOperation, error)
	MarkRunningAgentUpgradeSucceededByVersion(ctx context.Context, arg sqlc.MarkRunningAgentUpgradeSucceededByVersionParams) (int64, error)
}

// NewHub creates a new Hub.
func NewHub(log *slog.Logger) *Hub {
	return NewHubWithValidator(log, nil)
}

// NewHubWithValidator creates a new Hub with optional DB-backed token validation.
func NewHubWithValidator(log *slog.Logger, validator AgentTokenValidator) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		agents:    newShardedAgents(),
		log:       log,
		validator: validator,
	}
}

// HandleWebSocket is the HTTP handler for WebSocket upgrade.
// Route: /api/v1/ws/agent/tunnel/{cluster_id}/
func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// A4 / M5: throttle by SOURCE IP BEFORE the WS upgrade so an IP that has
	// blown past the auth-failure threshold gets a clean 429 — no upgrade, no
	// DB token lookup, no goroutines. Per-IP only: other IPs are unaffected,
	// and a healthy agent (which never accumulates failures, and whose every
	// success Resets its bucket) is never blocked here.
	ipKey, ipAddr := ConnectClientIP(r)
	if lim := h.connectLimiter(); lim != nil {
		if blocked, retryAfter := lim.Blocked(ipKey); blocked {
			secs := int(math.Ceil(retryAfter.Seconds()))
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":"rate_limited"}`))
			return
		}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Allow any origin for agent connections; agents authenticate via token.
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.log.Error("websocket accept failed", slog.String("error", err.Error()))
		return
	}

	// Default github.com/coder/websocket read limit is 32 KiB which is too small for
	// proxied k8s API list responses. Bump to 16 MiB on the tunnel.
	conn.SetReadLimit(16 << 20)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 1. Read first message — must be CONNECT with valid token.
	connectCtx, connectCancel := context.WithTimeout(ctx, connectTimeout)
	defer connectCancel()

	var firstMsg protocol.Message
	if err := wsjson.Read(connectCtx, conn, &firstMsg); err != nil {
		h.log.Error("failed to read connect message", slog.String("error", err.Error()))
		_ = conn.Close(websocket.StatusProtocolError, "expected CONNECT message")
		return
	}

	if firstMsg.Type != protocol.MsgConnect {
		h.log.Warn("first message was not CONNECT", slog.String("type", string(firstMsg.Type)))
		_ = conn.Close(websocket.StatusProtocolError, "first message must be CONNECT")
		return
	}

	var payload protocol.ConnectPayload
	if err := json.Unmarshal(firstMsg.Payload, &payload); err != nil {
		h.log.Error("invalid connect payload", slog.String("error", err.Error()))
		_ = conn.Close(websocket.StatusProtocolError, "invalid CONNECT payload")
		return
	}

	// 2. Validate registration token.
	if payload.ClusterID == "" || payload.Token == "" {
		h.log.Warn("connect payload missing cluster_id or token")
		_ = conn.Close(websocket.StatusProtocolError, "cluster_id and token are required")
		return
	}

	// A4 / L13: lenient timestamp-skew replay defense. The agent stamps the
	// CONNECT envelope with time.Now().UTC(); we reject a handshake whose
	// timestamp is wildly out of range (stale replay when d>window, far-future
	// clock when d<-window). Two leniency guards keep honest agents safe:
	//   (1) Timestamp.IsZero() — an older agent that never stamps the envelope
	//       is NEVER hard-rejected (back-compat skip), and
	//   (2) clockSkew<=0 — the knob disables the check entirely.
	// This runs pre-DB so a stale frame costs no token lookup; we do NOT call
	// connLimiter.Fail on a skew rejection — a fleet on a bad NTP source behind
	// one NAT is told to fix its clock, not throttled (the frame is already
	// rejected before any DB probe, so there is no DoS exposure to limit).
	// FUTURE WORK (out of scope for A4): a full challenge-response nonce. A3
	// single-use registration + A2 rotation + bearer-token semantics already
	// gut replay value; timestamp-skew is the A4 deliverable.
	if connectTimestampOutsideSkew(time.Now().UTC(), firstMsg.Timestamp, h.connectClockSkew()) {
		h.log.Warn("connect timestamp outside allowed clock skew",
			slog.String("cluster_id", payload.ClusterID),
			slog.Time("connect_timestamp", firstMsg.Timestamp),
		)
		h.recordAgentAuthFailed(ctx, payload, ipAddr, r, "invalid", "timestamp_skew")
		h.publish("agent.failed", payload.ClusterID, "", payload.AgentVersion)
		_ = conn.Close(websocket.StatusPolicyViolation, "connect timestamp outside allowed clock skew")
		return
	}

	compatibility := agentcompat.Evaluate(payload.AgentVersion)
	if compatibility.Blocked {
		h.log.Warn("agent compatibility check failed",
			slog.String("cluster_id", payload.ClusterID),
			slog.String("agent_version", payload.AgentVersion),
			slog.String("compatibility_status", compatibility.Status),
			slog.String("reason", compatibility.Message),
		)
		h.publish("agent.failed", payload.ClusterID, "", payload.AgentVersion)
		_ = conn.Close(websocket.StatusPolicyViolation, compatibility.Message)
		return
	}
	ackPayload := protocol.ConnectAckPayload{Accepted: true}
	// tokenKind ("registration"/"agent") is threaded out of the validator block
	// so the success-audit site below can record which credential authenticated.
	var connectTokenKind string
	if h.validator != nil {
		clusterID, err := uuid.Parse(payload.ClusterID)
		if err != nil {
			// Malformed cluster UUID is a cheap PRE-DB rejection, not a
			// credential probe, so it does NOT count against the limiter.
			h.log.Warn("invalid cluster id in connect payload", slog.String("cluster_id", payload.ClusterID))
			_ = conn.Close(websocket.StatusProtocolError, "invalid cluster_id")
			return
		}
		tokenKind, durableToken, err := h.validateAndMaybeRotateToken(ctx, clusterID, payload)
		if err != nil {
			// A4 / M5: a failed DB-backed token validation is the credential-probe
			// surface — count it (per IP) and audit it (fail-open). M6: the
			// agent.auth_failed record carries cluster_id, IP, agent_version, and
			// token-kind for the forensic trail.
			if lim := h.connectLimiter(); lim != nil {
				lim.Fail(ipKey)
			}
			h.recordAgentAuthFailed(ctx, payload, ipAddr, r, "invalid", err.Error())
			h.log.Warn("agent authentication failed",
				slog.String("cluster_id", payload.ClusterID),
				slog.String("error", err.Error()),
			)
			h.publish("agent.failed", payload.ClusterID, "", payload.AgentVersion)
			_ = conn.Close(websocket.StatusPolicyViolation, err.Error())
			return
		}
		// A successful validation clears this IP's failure history — the airtight
		// guarantee that a healthy fleet behind one egress IP is never throttled.
		if lim := h.connectLimiter(); lim != nil {
			lim.Reset(ipKey)
		}
		connectTokenKind = tokenKind
		// Deliver a fresh durable token in the ACK whenever the server
		// minted one that differs from what the agent presented. This
		// covers both the registration->durable exchange and the rotation
		// grace path (agent presented its current token, a rotation was
		// pending, server minted+rotated to a new token).
		if durableToken != "" && durableToken != payload.Token &&
			(tokenKind == "registration" || tokenKind == "agent") {
			ackPayload.AgentToken = durableToken
		}

		// PATH A: mint (or re-mint) the scoped apiserver-audit ingest token for
		// this authenticated cluster and deliver its plaintext in CONNECT_ACK.
		// Best-effort: a failure here must not block the agent from connecting
		// (it can still use tunnel audit delivery), so we log and continue.
		h.mu.RLock()
		issuer := h.ingestIssuer
		h.mu.RUnlock()
		if issuer != nil {
			if token, ierr := issuer.IssueIngestToken(ctx, clusterID); ierr != nil {
				h.log.Warn("failed to mint apiserver-audit ingest token",
					slog.String("cluster_id", payload.ClusterID),
					slog.String("error", ierr.Error()),
				)
			} else {
				ackPayload.AuditIngestToken = token
			}
		}
	}

	// 3. Generate session ID and send CONNECT_ACK.
	sessionID := fmt.Sprintf("session-%s-%d", payload.ClusterID, time.Now().UnixNano())
	ackPayload.SessionID = sessionID
	ackPayload.ServerVersion = ""
	ackBody, _ := json.Marshal(ackPayload)

	ackMsg := &protocol.Message{
		Type:    protocol.MsgConnectAck,
		Payload: ackBody,
	}

	writeCtx, writeCancel := context.WithTimeout(ctx, writeTimeout)
	if err := wsjson.Write(writeCtx, conn, ackMsg); err != nil {
		writeCancel()
		h.log.Error("failed to send connect ack", slog.String("error", err.Error()))
		_ = conn.Close(websocket.StatusInternalError, "failed to send CONNECT_ACK")
		return
	}
	writeCancel()

	// 4. Register agent in hub.
	agent := &AgentConnection{
		ClusterID:    payload.ClusterID,
		AgentID:      payload.AgentID,
		AgentVersion: payload.AgentVersion,
		SessionID:    sessionID,
		Conn:         conn,
		Streams:      NewStreamManager(256),
		cancel:       cancel,
		sendCh:       make(chan *protocol.Message, sendChannelSize),
	}
	h.persistConnect(ctx, agent)

	// Disconnect any existing connection for this cluster. If we replaced one,
	// surface that as an agent.reconnecting hint so subscribers can show the
	// transition explicitly (the cluster.connected fan-out below covers the
	// happy path; this distinguishes "first connect" from "reconnect").
	wasReconnect := false
	if existing := h.agents.Set(payload.ClusterID, agent); existing != nil {
		observability.WithEvent(h.log, "agent_reconnecting").Info("replacing existing agent connection",
			slog.String("cluster_id", payload.ClusterID),
			slog.String("old_session", existing.SessionID),
		)
		existing.cancel()
		existing.Streams.CloseAll()
		wasReconnect = true
	}
	// Always count the register — first-connect and reconnect alike — so
	// the rate over a window captures "is this cluster flapping?" rather
	// than just "is anything ever happening at all".
	recordAgentReconnect(payload.ClusterID)
	if wasReconnect {
		h.publish("agent.reconnecting", payload.ClusterID, sessionID, payload.AgentVersion)
	}

	observability.WithEvent(h.log, "agent_connected").Info("agent connected",
		slog.String("cluster_id", payload.ClusterID),
		slog.String("agent_id", payload.AgentID),
		slog.String("agent_version", payload.AgentVersion),
		slog.String("session_id", sessionID),
	)
	// A4 / M6: durable forensic record of the successful join (cluster_id, IP,
	// agent_version, token-kind). Fail-open — a write error cannot block the
	// connection (audit.Record logs+swallows). Skipped in the validator==nil
	// dev path (no DB identity), same as the rotation helpers.
	h.recordAgentConnected(ctx, payload, ipAddr, r, connectTokenKind, sessionID, wasReconnect)
	h.publish("cluster.connected", payload.ClusterID, sessionID, payload.AgentVersion)

	// Cross-pod proxy fallback: publish "we own this cluster's WS" so
	// sibling replicas can reverse-proxy inbound /k8s, kubectl shell,
	// log-stream, and exec requests to us when their local Hub misses.
	// nil-safe when the locator isn't wired (single-replica installs).
	h.mu.RLock()
	loc := h.locator
	h.mu.RUnlock()
	loc.Set(ctx, payload.ClusterID)

	// Wizard phase advance — wired by cmd/server when the registration
	// service is constructed. Best-effort: when the cluster wasn't
	// in awaiting_agent (e.g. legacy non-wizard registration) the
	// service no-ops the transition. We don't fail the connect on a
	// transition error.
	h.mu.RLock()
	advancer := h.regAdvancer
	h.mu.RUnlock()
	if advancer != nil {
		if clusterUUID, perr := uuid.Parse(payload.ClusterID); perr == nil {
			if err := advancer.OnAgentConnected(ctx, clusterUUID, payload.AgentVersion); err != nil {
				h.log.Warn("registration advance on agent_connected failed",
					slog.String("cluster_id", payload.ClusterID),
					slog.String("error", err.Error()),
				)
			}
		}
	}

	// 5. Start read/write goroutines.
	var wg sync.WaitGroup
	wg.Add(2)

	// Write goroutine: sends messages from sendCh to the WebSocket.
	go func() {
		defer wg.Done()
		h.writePump(ctx, agent)
	}()

	// Read goroutine: reads messages from the WebSocket and dispatches them.
	go func() {
		defer wg.Done()
		h.readPump(ctx, agent)
	}()

	wg.Wait()

	// 6. On disconnect: remove from map, close connection.
	// removeAgent reports whether WE were still the registered agent. On a
	// same-pod reconnect, a newer HandleWebSocket may have already replaced
	// us in the map (h.agents.Set cancelled our ctx); in that case
	// DeleteIfSame is a no-op and returns false — we were superseded.
	stillOwner := h.removeAgent(agent)
	agent.Streams.CloseAll()
	_ = conn.Close(websocket.StatusNormalClosure, "disconnected")

	observability.WithEvent(h.log, "agent_disconnected").Info("agent disconnected",
		slog.String("cluster_id", agent.ClusterID),
		slog.String("session_id", agent.SessionID),
	)
	h.persistDisconnect(context.Background(), agent)
	h.publish("cluster.disconnected", agent.ClusterID, agent.SessionID, agent.AgentVersion)

	// Clear the locator entry so siblings stop forwarding to us — but ONLY
	// if we were still the registered owner. On a same-pod reconnect the
	// newer connection already ran loc.Set (installing a fresh refresh loop
	// under l.cancels[clusterID] and rewriting redis to this pod). If the
	// superseded goroutine called Delete here it would cancel the NEW
	// refresh loop and CAS-delete the redis key (whose value still equals
	// this pod's own address, so the compare matches) — leaving the cluster
	// live in the in-memory map but with no directory entry and no loop to
	// re-add one, so sibling replicas 503. Skipping Delete when superseded
	// leaves the newer connection's locator entry intact.
	// Best-effort; the TTL will reap stragglers if redis is down.
	if stillOwner {
		h.mu.RLock()
		disconnectLoc := h.locator
		h.mu.RUnlock()
		disconnectLoc.Delete(context.Background(), agent.ClusterID)
	}
}

// readPump reads messages from the WebSocket and dispatches them.
func (h *Hub) readPump(ctx context.Context, agent *AgentConnection) {
	for {
		var msg protocol.Message
		if err := wsjson.Read(ctx, agent.Conn, &msg); err != nil {
			if ctx.Err() != nil {
				return
			}
			h.log.Error("read error",
				slog.String("cluster_id", agent.ClusterID),
				slog.String("error", err.Error()),
			)
			agent.cancel()
			return
		}
		recordAgentMessage(agent.ClusterID, "inbound")
		h.handleMessage(agent, &msg)
	}
}

// writePump sends messages from the agent's sendCh to the WebSocket and
// emits server-initiated WebSocket-level pings on a steady cadence so any
// HTTP-level idle timeout in the network path (ingress, k3d serverlb, LBs)
// observes a fresh frame on the connection and never terminates it.
func (h *Hub) writePump(ctx context.Context, agent *AgentConnection) {
	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pingTicker.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, writeTimeout)
			if err := agent.Conn.Ping(pingCtx); err != nil {
				pingCancel()
				if ctx.Err() != nil {
					return
				}
				h.log.Warn("ping error",
					slog.String("cluster_id", agent.ClusterID),
					slog.String("error", err.Error()),
				)
				agent.cancel()
				return
			}
			pingCancel()
		case msg := <-agent.sendCh:
			writeCtx, writeCancel := context.WithTimeout(ctx, writeTimeout)
			if err := wsjson.Write(writeCtx, agent.Conn, msg); err != nil {
				writeCancel()
				if ctx.Err() != nil {
					return
				}
				h.log.Error("write error",
					slog.String("cluster_id", agent.ClusterID),
					slog.String("error", err.Error()),
				)
				agent.cancel()
				return
			}
			writeCancel()
			recordAgentMessage(agent.ClusterID, "outbound")
		}
	}
}

// removeAgent removes an agent from the hub if it matches the current
// registration. Returns true when this agent was still the registered owner
// and was removed; false when it had already been superseded by a newer
// connection (same-pod reconnect) — the caller uses this to avoid clobbering
// the newer connection's locator entry.
func (h *Hub) removeAgent(agent *AgentConnection) bool {
	return h.agents.DeleteIfSame(agent.ClusterID, agent)
}

// GetAgent returns the connection for a cluster, or nil if not connected.
func (h *Hub) GetAgent(clusterID string) *AgentConnection {
	return h.agents.Get(clusterID)
}

// SendToAgent sends a message to a specific agent.
// Returns an error if the agent is not connected or the send buffer is full.
func (h *Hub) SendToAgent(clusterID string, msg *protocol.Message) error {
	agent := h.agents.Get(clusterID)
	if agent == nil {
		return fmt.Errorf("agent for cluster %q not connected", clusterID)
	}

	select {
	case agent.sendCh <- msg:
		return nil
	default:
		return fmt.Errorf("send buffer full for cluster %q", clusterID)
	}
}

// Disconnect forcibly tears down the WS tunnel for a cluster. Used by the
// cluster decommission reconciler after MsgDecommission has been delivered
// and ACKed — once the server revokes the agent's registration token, any
// new dial attempt fails and any in-flight K8s proxy calls would block
// indefinitely on the now-uncredentialed agent. Calling Disconnect cancels
// the per-agent context, which cascades into both readPump and writePump
// exiting and the WebSocket being closed cleanly.
//
// No-op (returns false) when no agent is currently registered for that
// cluster ID; callers may want to log a warning in that case so operators
// know the cleanup-by-RPC path was unavailable. Idempotent — multiple
// Disconnect calls are safe; the second one just observes the entry gone
// from the map.
func (h *Hub) Disconnect(clusterID string) bool { return h.disconnectImpl(clusterID) }

// Drain gracefully closes every connected agent in parallel. Called from
// the server's Shutdown path before httpServer.Shutdown so agents
// reconnect to a surviving replica immediately, instead of waiting out
// the 20s WS-ping timeout that the previous shutdown path produced.
//
// Snapshots the agent set under the lock, then closes each agent OUTSIDE
// the lock in parallel. Closing serially under the lock would multiply
// close latency by N. Each close cancels the agent's context and shuts
// its stream manager — the agent's read loop observes the close
// immediately and re-dials, hitting the Service load balancer which
// routes to a healthy sibling pod.
//
// Returns the number of agents that were drained.
func (h *Hub) Drain() int {
	snapshot := h.agents.DrainAll()
	if len(snapshot) == 0 {
		return 0
	}
	observability.WithEvent(h.log, "hub_drain_started").Info("draining all agent connections",
		slog.Int("count", len(snapshot)),
	)
	for _, agent := range snapshot {
		go func(a *AgentConnection) {
			a.cancel()
			a.Streams.CloseAll()
		}(agent)
	}
	// Wipe our locator entries so siblings stop forwarding to a pod
	// that's about to terminate. Done synchronously so the entries are
	// gone before the HTTP server's grace period elapses.
	h.mu.RLock()
	loc := h.locator
	h.mu.RUnlock()
	if loc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		loc.Drain(ctx)
	}
	return len(snapshot)
}

// disconnectImpl is the unexported worker for the public Disconnect.
// Kept separate so Drain doesn't recurse into a re-lock.
func (h *Hub) disconnectImpl(clusterID string) bool {
	// Remove first so HandleWebSocket's removeAgent() at the bottom of
	// the read/write loop doesn't have to race with us.
	agent := h.agents.Delete(clusterID)
	if agent == nil {
		return false
	}
	observability.WithEvent(h.log, "agent_forced_disconnect").Info("forcibly disconnecting agent",
		slog.String("cluster_id", clusterID),
		slog.String("session_id", agent.SessionID),
	)
	agent.cancel()
	agent.Streams.CloseAll()
	// Clear the locator directory entry. On the forced-disconnect path we
	// already ran agents.Delete above, so HandleWebSocket's teardown will see
	// removeAgent()==false and skip its own (now owner-gated) loc.Delete — so
	// this path must clear the entry itself or a decommissioned/force-dropped
	// cluster keeps a stale redis directory row pointing here until its TTL,
	// making siblings reverse-proxy to a pod whose agent is already gone.
	h.mu.RLock()
	loc := h.locator
	h.mu.RUnlock()
	if loc != nil {
		loc.Delete(context.Background(), clusterID)
	}
	return true
}

// SendDecommission sends a MsgDecommission to the agent and waits for
// MsgDecommissionAck. The returned `connected` flag is true if an agent was
// registered for the cluster at the moment of send — the cluster decommission
// reconciler uses this to distinguish "agent unreachable, skip with warning"
// from "agent reachable but bombed". `wait` bounds how long we wait for the
// ACK; on timeout we return the connected flag but a nil ack and a
// context.DeadlineExceeded error.
func (h *Hub) SendDecommission(ctx context.Context, clusterID string, payload protocol.DecommissionPayload, wait time.Duration) (*protocol.DecommissionAckPayload, bool, error) {
	agent := h.agents.Get(clusterID)
	if agent == nil {
		// Multi-replica HA: the decommission task is drained off the shared
		// 'tunnel' asynq queue by ANY pod, but the agent's WS is live on
		// exactly one pod. If the locator says a SIBLING pod owns the WS,
		// return a retryable "cluster agent not connected" error so the
		// existing isAgentNotConnectedErr (cluster_template_apply.go) matches
		// and asynq re-queues the WHOLE phase onto the owning pod, where the
		// local Get succeeds and the ACK is in-process. We deliberately do
		// NOT proxy the stateful MsgDecommission stream+ACK across pods: the
		// only cross-pod forwarders handle plain HTTP / WS-upgrade, not a
		// per-step ACK round-trip. SINGLE-REPLICA is unaffected — the locator
		// is nil (or points at self) so we fall through to skip-with-audit.
		if loc := h.Locator(); loc != nil {
			if addr, lerr := loc.Lookup(ctx, clusterID); lerr == nil && addr != "" && addr != loc.Address() {
				return nil, false, fmt.Errorf("cluster agent not connected to this pod (owner=%s)", addr)
			}
		}
		// Locator nil / no entry / entry points at us but the local map lost
		// it → agent genuinely gone → keep skip-with-audit (legacy behavior).
		return nil, false, nil
	}
	streamID := uuid.NewString()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		return nil, true, fmt.Errorf("create decommission stream: %w", err)
	}
	defer agent.Streams.CloseStream(streamID)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, true, fmt.Errorf("marshal decommission payload: %w", err)
	}
	msg := &protocol.Message{
		Type:      protocol.MsgDecommission,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}
	if err := h.SendToAgent(clusterID, msg); err != nil {
		return nil, true, fmt.Errorf("send decommission: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	select {
	case data := <-stream.DataCh:
		var ack protocol.DecommissionAckPayload
		if err := json.Unmarshal(data, &ack); err != nil {
			return nil, true, fmt.Errorf("decode decommission ack: %w", err)
		}
		return &ack, true, nil
	case <-stream.DoneCh:
		return nil, true, fmt.Errorf("decommission stream closed before ack")
	case <-waitCtx.Done():
		return nil, true, waitCtx.Err()
	}
}

// BroadcastToAll sends a message to all connected agents. Snapshots
// the agent set across all shards (briefly locking each in turn) then
// sends OUTSIDE the locks — at 500 agents the previous serial-under-
// lock implementation made BroadcastToAll latency O(N * send-latency)
// and held the hub mutex the whole time.
func (h *Hub) BroadcastToAll(msg *protocol.Message) {
	agents := h.agents.Snapshot()
	for _, agent := range agents {
		select {
		case agent.sendCh <- msg:
		default:
			h.log.Warn("broadcast: send buffer full, skipping",
				slog.String("cluster_id", agent.ClusterID),
			)
		}
	}
}

// ConnectedClusters returns a list of connected cluster IDs.
func (h *Hub) ConnectedClusters() []string {
	return h.agents.ConnectedIDs()
}

func (h *Hub) persistConnect(ctx context.Context, agent *AgentConnection) {
	if h.validator == nil {
		return
	}
	clusterID, err := uuid.Parse(agent.ClusterID)
	if err != nil {
		h.log.Warn("failed to persist agent connection: invalid cluster id",
			slog.String("cluster_id", agent.ClusterID),
			slog.String("error", err.Error()),
		)
		return
	}
	if err := h.validator.DisconnectActiveConnectionsByCluster(ctx, clusterID); err != nil {
		h.log.Warn("failed to disconnect existing agent sessions",
			slog.String("cluster_id", agent.ClusterID),
			slog.String("error", err.Error()),
		)
	}
	row, err := h.validator.CreateAgentConnection(ctx, sqlc.CreateAgentConnectionParams{
		ClusterID:    clusterID,
		AgentID:      agent.AgentID,
		SessionID:    agent.SessionID,
		Status:       "connected",
		ChannelName:  "",
		PodName:      "",
		NodeName:     "",
		AgentVersion: agent.AgentVersion,
	})
	if err != nil {
		h.log.Warn("failed to persist agent connection",
			slog.String("cluster_id", agent.ClusterID),
			slog.String("session_id", agent.SessionID),
			slog.String("error", err.Error()),
		)
		return
	}
	agent.DBID = row.ID
}

func (h *Hub) persistDisconnect(ctx context.Context, agent *AgentConnection) {
	if h.validator == nil || agent.DBID == uuid.Nil {
		return
	}
	disconnectedAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	if err := h.validator.UpdateAgentConnectionStatus(ctx, sqlc.UpdateAgentConnectionStatusParams{
		ID:             agent.DBID,
		Status:         "disconnected",
		DisconnectedAt: disconnectedAt,
	}); err != nil {
		h.log.Warn("failed to persist agent disconnect",
			slog.String("cluster_id", agent.ClusterID),
			slog.String("session_id", agent.SessionID),
			slog.String("error", err.Error()),
		)
	}
}

func (h *Hub) persistPing(agent *AgentConnection) {
	if h.validator == nil || agent.DBID == uuid.Nil {
		return
	}
	if err := h.validator.UpdateAgentConnectionPing(context.Background(), agent.DBID); err != nil {
		h.log.Warn("failed to persist agent ping",
			slog.String("cluster_id", agent.ClusterID),
			slog.String("session_id", agent.SessionID),
			slog.String("error", err.Error()),
		)
	}
}

func (h *Hub) validateAndMaybeRotateToken(ctx context.Context, clusterID uuid.UUID, payload protocol.ConnectPayload) (string, string, error) {
	// SEC-R01: shared A3 gate with tunnel2 (adoption + durable hash + cluster match).
	res, err := connectauth.Validate(ctx, h.validator, clusterID, payload.Token)
	if err != nil {
		return "", "", err
	}

	if res.Kind == connectauth.KindRegistration {
		if err := h.validator.MarkRegistrationTokenUsed(ctx, res.RegistrationToken.ID); err != nil {
			return "", "", fmt.Errorf("failed to mark token used")
		}
		durable, err := h.ensureClusterAgentToken(ctx, clusterID)
		if err != nil {
			return "", "", fmt.Errorf("failed to issue agent token")
		}
		return connectauth.KindRegistration, durable, nil
	}

	agentToken := res.AgentToken

	// A3: stamp adoption on the first CONNECT that presents a valid durable
	// token (proof the agent persisted it). Best-effort/non-fatal like the
	// adjacent Touch; the query's WHERE adopted_at IS NULL makes it
	// idempotent across reconnects and rotation/grace sub-paths. agentToken.ID
	// is row-stable across A2 rotation, so stamping pre-rotation is correct.
	if err := h.validator.MarkClusterAgentTokenAdopted(ctx, agentToken.ID); err != nil {
		h.log.Warn("failed to mark cluster agent token adopted",
			slog.String("cluster_id", payload.ClusterID), slog.String("error", err.Error()))
	}

	// Rotation grace. When a rotation is pending, the very next CONNECT
	// (presenting the still-valid current token) mints a fresh durable
	// token, demotes the old hash to previous_token_hash so it keeps
	// validating until the agent adopts the new one, and delivers the
	// fresh token in the ACK. The agent never holds an invalid token.
	if agentToken.RotationPendingAt.Valid {
		presentedHash := auth.HashOpaqueToken(payload.Token)
		// Only rotate when the agent presented the CURRENT token. If it
		// presented the previous (already-rotated-out) token we must not
		// rotate again — that would strand the in-flight new token.
		if agentToken.TokenHash == presentedHash {
			if fresh, err := h.rotateClusterAgentToken(ctx, clusterID, agentToken); err == nil {
				return connectauth.KindAgent, fresh, nil
			} else {
				h.log.Warn("failed to rotate cluster agent token; continuing with current token",
					slog.String("cluster_id", payload.ClusterID),
					slog.String("error", err.Error()),
				)
			}
		}
	}

	// First CONNECT with the NEW current token retires the old hash
	// (revoke the previous token now that the agent has adopted the new
	// one). Skip when the agent presented the previous token — that's the
	// grace path and clearing here would lock it out.
	if agentToken.PreviousTokenHash.Valid && agentToken.PreviousTokenHash.String != "" {
		presentedHash := auth.HashOpaqueToken(payload.Token)
		if agentToken.TokenHash == presentedHash {
			if err := h.validator.ClearPreviousClusterAgentTokenHash(ctx, agentToken.ID); err != nil {
				h.log.Warn("failed to clear previous agent token hash",
					slog.String("cluster_id", payload.ClusterID),
					slog.String("error", err.Error()),
				)
			}
		}
	}

	if err := h.validator.TouchClusterAgentToken(ctx, agentToken.ID); err != nil {
		h.log.Warn("failed to touch cluster agent token",
			slog.String("cluster_id", payload.ClusterID),
			slog.String("error", err.Error()),
		)
	}
	return connectauth.KindAgent, payload.Token, nil
}

// rotateClusterAgentToken mints a fresh durable token and performs the grace
// rotation (old hash -> previous_token_hash, fresh -> token/token_hash,
// stamp last_rotated_at, clear rotation_pending_at). Returns the plaintext
// fresh token for delivery in the CONNECT_ACK.
func (h *Hub) rotateClusterAgentToken(ctx context.Context, clusterID uuid.UUID, current sqlc.ClusterAgentToken) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.URLEncoding.EncodeToString(b)
	row, err := h.validator.RotateClusterAgentToken(ctx, sqlc.RotateClusterAgentTokenParams{
		ID:        current.ID,
		Token:     "",
		TokenHash: auth.HashOpaqueToken(token),
	})
	if err != nil {
		return "", err
	}
	h.recordAgentTokenRotationCompleted(ctx, clusterID, row)
	return token, nil
}

func (h *Hub) ensureClusterAgentToken(ctx context.Context, clusterID uuid.UUID) (string, error) {
	// NOTE (A3 residual): this early-return returns an EXISTING plaintext durable
	// (token != "") without re-minting, so it does NOT reset adopted_at — for a
	// legacy plaintext durable a re-import token stays replayable within its TTL
	// (the gate anchor never advances). Modern durables are hash-only (token ==
	// ""), so they fall through to the upsert below, which resets adopted_at = NULL
	// and re-closes the temporal gate. There are 0 plaintext durables in current
	// deployments; if legacy ones reappear, reset adopted_at on this path too.
	if tok, err := h.validator.GetClusterAgentTokenByClusterID(ctx, clusterID); err == nil && tok.Token != "" {
		if err := h.validator.TouchClusterAgentToken(ctx, tok.ID); err != nil {
			h.log.Warn("failed to touch existing cluster agent token",
				slog.String("cluster_id", clusterID.String()),
				slog.String("error", err.Error()),
			)
		}
		return tok.Token, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.URLEncoding.EncodeToString(b)
	row, err := h.validator.UpsertClusterAgentToken(ctx, sqlc.UpsertClusterAgentTokenParams{
		ClusterID: clusterID,
		TokenHash: auth.HashOpaqueToken(token),
	})
	if err != nil {
		return "", err
	}
	h.recordAgentTokenRotated(ctx, clusterID, row)
	if row.Token != "" {
		return row.Token, nil
	}
	return token, nil
}

// recordAgentConnected audits a successful tunnel CONNECT (A4 / M6). Fail-open:
// audit.Record logs+swallows write errors and never blocks, and a Querier
// type-assert miss simply skips the record — a legitimate connection is never
// dropped or delayed by auditing. tokenKind is the validator verdict
// ("registration" or "agent") mapped to the durable/registration wire label.
func (h *Hub) recordAgentConnected(ctx context.Context, payload protocol.ConnectPayload, ipAddr *netip.Addr, r *http.Request, tokenKind, sessionID string, reconnect bool) {
	q, ok := h.validator.(audit.Querier)
	if !ok {
		return
	}
	audit.Record(ctx, q, audit.Event{
		Source:          "tunnel",
		ActorAuthMethod: "agent_token",
		Action:          actionAgentConnected,
		ResourceType:    "cluster",
		ResourceID:      payload.ClusterID,
		ResourceName:    payload.ClusterID,
		StatusCode:      200,
		IPAddress:       ipAddr,
		UserAgent:       userAgentOf(r),
		Detail: map[string]any{
			"cluster_id":    payload.ClusterID,
			"source_ip":     ipKeyOf(ipAddr),
			"agent_id":      payload.AgentID,
			"agent_version": payload.AgentVersion,
			"token_kind":    connectTokenKindLabel(tokenKind),
			"session_id":    sessionID,
			"reconnect":     reconnect,
		},
	})
}

// recordAgentAuthFailed audits a rejected tunnel CONNECT (A4 / M6). Same
// fail-open contract as recordAgentConnected. reason is the validation error
// string (for a token failure) or "timestamp_skew" (for the L13 replay path).
// Token plaintext is NEVER placed in Detail.
func (h *Hub) recordAgentAuthFailed(ctx context.Context, payload protocol.ConnectPayload, ipAddr *netip.Addr, r *http.Request, tokenKind, reason string) {
	q, ok := h.validator.(audit.Querier)
	if !ok {
		return
	}
	audit.Record(ctx, q, audit.Event{
		Source:          "tunnel",
		ActorAuthMethod: "agent_token",
		Action:          actionAgentAuthFailed,
		ResourceType:    "cluster",
		ResourceID:      payload.ClusterID,
		StatusCode:      403,
		IPAddress:       ipAddr,
		UserAgent:       userAgentOf(r),
		Detail: map[string]any{
			"cluster_id":    payload.ClusterID,
			"source_ip":     ipKeyOf(ipAddr),
			"agent_version": payload.AgentVersion,
			"token_kind":    tokenKind,
			"reason":        reason,
		},
	})
}

// connectTokenKindLabel maps the validator's internal verdict to the audited
// wire label: "registration" (registration-token exchange) -> "registration",
// "agent" (durable agent token) -> "durable".
func connectTokenKindLabel(kind string) string {
	return connectauth.TokenKindLabel(kind)
}

func ipKeyOf(addr *netip.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

func userAgentOf(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.UserAgent()
}

func (h *Hub) recordAgentTokenRotated(ctx context.Context, clusterID uuid.UUID, row sqlc.ClusterAgentToken) {
	q, ok := h.validator.(audit.Querier)
	if !ok {
		return
	}
	hashPrefix := row.TokenHash
	if len(hashPrefix) > 12 {
		hashPrefix = hashPrefix[:12]
	}
	audit.Record(ctx, q, audit.Event{
		Source:          "tunnel",
		ActorAuthMethod: "registration_token",
		Action:          "agent.token.rotated",
		ResourceType:    "cluster",
		ResourceID:      clusterID.String(),
		Detail: map[string]any{
			"cluster_id":               clusterID.String(),
			"cluster_token_id":         row.ID.String(),
			"previous_credential_type": "registration_token",
			"new_credential_type":      "durable_agent_token",
			"token_hash_prefix":        hashPrefix,
			"hash_algorithm":           "sha256",
			"plaintext_stored":         row.Token != "",
			"source":                   "registration_token_exchange",
		},
	})
}

// recordAgentTokenRotationCompleted audits a grace rotation finishing on the
// tunnel: a pending rotation was driven to completion on the agent's CONNECT
// and a fresh durable token was delivered in the ACK.
func (h *Hub) recordAgentTokenRotationCompleted(ctx context.Context, clusterID uuid.UUID, row sqlc.ClusterAgentToken) {
	q, ok := h.validator.(audit.Querier)
	if !ok {
		return
	}
	hashPrefix := row.TokenHash
	if len(hashPrefix) > 12 {
		hashPrefix = hashPrefix[:12]
	}
	audit.Record(ctx, q, audit.Event{
		Source:          "tunnel",
		ActorAuthMethod: "agent_token",
		Action:          "agent.token.rotation.confirmed",
		ResourceType:    "cluster",
		ResourceID:      clusterID.String(),
		Detail: map[string]any{
			"cluster_id":        clusterID.String(),
			"cluster_token_id":  row.ID.String(),
			"token_hash_prefix": hashPrefix,
			"hash_algorithm":    "sha256",
			"grace_active":      row.PreviousTokenHash.Valid && row.PreviousTokenHash.String != "",
			"source":            "rotation_grace",
		},
	})
}
