// Package events implements a pub/sub bus used to push lifecycle events to
// long-lived API consumers (SSE / WebSocket subscribers) and local taps
// (webhooks, SIEM). The bus is lossy on slow consumers.
//
// Multi-replica: when a Redis client is attached via AttachRedis, Publish
// also fans out to other server pods. Remote replicas re-inject with
// Event.Remote=true so webhook/SIEM taps (which must not double-deliver)
// skip them; SSE consumers still receive them.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/redis/go-redis/v9"
)

// Type is the event-type discriminator used by clients to filter / dispatch.
type Type string

const (
	// TypeClusterConnected fires when an agent successfully completes the
	// CONNECT_ACK handshake on the tunnel.
	TypeClusterConnected Type = "cluster.connected"

	// TypeClusterDisconnected fires when the tunnel closes for any reason.
	TypeClusterDisconnected Type = "cluster.disconnected"

	// TypeClusterHeartbeat fires when an agent heartbeat updates last_heartbeat.
	TypeClusterHeartbeat Type = "cluster.heartbeat"

	// TypeClusterStatusChanged fires when a cluster's status column transitions
	// (active <-> disconnected <-> error).
	TypeClusterStatusChanged Type = "cluster.status_changed"

	// TypeClusterMetrics fires periodically with a cluster's CPU / memory /
	// pod-count snapshot. Emitted by the metrics publisher every ~10s for
	// each active cluster.
	TypeClusterMetrics Type = "cluster.metrics"

	// TypeClusterCreated fires when a cluster row is inserted.
	TypeClusterCreated Type = "cluster.created"

	// TypeClusterUpdated fires when a cluster row is mutated via the API.
	TypeClusterUpdated Type = "cluster.updated"

	// TypeClusterDeleted fires when a cluster row is removed.
	TypeClusterDeleted Type = "cluster.deleted"

	// TypeAgentReconnecting fires when the tunnel server observes an agent
	// attempting to (re)connect — currently emitted alongside cluster.connected
	// when the cluster row was previously in a disconnected state.
	TypeAgentReconnecting Type = "agent.reconnecting"

	// TypeAgentFailed fires when an agent connection attempt is rejected (bad
	// token, mismatched cluster id) or terminates with a transport error.
	TypeAgentFailed Type = "agent.failed"

	// TypeClusterK8sChanged is a coarse-grained "something on this cluster
	// changed" signal. The agent-side informer fan-out is a follow-up; until
	// then we publish this on agent-driven mutations the server can observe
	// (cluster CRUD, agent reconnects). UI subscribers treat it as an
	// invalidation hint for the resource list of a given cluster.
	TypeClusterK8sChanged Type = "cluster.k8s_changed"

	// TypeClusterRegistrationStep is fired by the wizard backend every
	// time a cluster_registration_steps row is written or updated. The
	// SSE endpoint (/api/v1/clusters/{id}/registration/events/) filters
	// the event stream by cluster_id and forwards matching events to
	// the wizard frontend / Provisioning tab. Sprint 22 (migration 078).
	TypeClusterRegistrationStep Type = "cluster.registration.step"

	// TypeClusterRegistrationPhase is fired alongside step events
	// whenever the cluster's registration_phase column transitions.
	// Carries the new phase so subscribers can route the wizard URL
	// without re-fetching status.
	TypeClusterRegistrationPhase Type = "cluster.registration.phase"
)

// DefaultRedisChannel is the pub/sub channel for cross-pod event fan-out.
const DefaultRedisChannel = "astronomer:events:v1"

const (
	// DefaultRedisRelayQueueCapacity absorbs ordinary event bursts without
	// allowing a Redis outage to grow process memory without bound.
	DefaultRedisRelayQueueCapacity = 1024
	// MaxRedisRelayQueueCapacity is the hard operator-configurable ceiling.
	MaxRedisRelayQueueCapacity = 65536

	redisRelayPublishTimeout  = 2 * time.Second
	redisRelayShutdownTimeout = 500 * time.Millisecond
	redisRelayRetryBackoff    = 50 * time.Millisecond
	redisRelayMaxAttempts     = 2
	redisRelayLogInterval     = 30 * time.Second
)

// Event is a single push payload. Data is opaque JSON-marshalable per Type.
// Publishers must treat Data and any referenced maps/slices as immutable after
// Publish returns: Redis serialization is intentionally owned by the
// asynchronous relay worker so Publish never pays serialization/Redis latency.
type Event struct {
	ID   uint64    `json:"id"`
	Type Type      `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data,omitempty"`
	// Remote is true when the event was injected from another server pod via
	// Redis. Webhook/SIEM taps MUST skip Remote events to avoid double delivery;
	// SSE consumers should deliver them.
	Remote bool `json:"remote,omitempty"`
	// Origin identifies the publishing pod (best-effort; used for echo suppress).
	Origin string `json:"origin,omitempty"`
}

// Bus is a multi-subscriber, lossy-on-slow-consumer pub/sub with optional
// Redis fan-out for multi-replica SSE.
type Bus struct {
	mu     sync.RWMutex
	subs   map[*subscription]struct{}
	nextID atomic.Uint64

	relayMu    sync.RWMutex
	rdb        redis.UniversalClient
	channel    string
	origin     string
	log        *slog.Logger
	relayQueue chan Event
	relayDone  chan struct{}

	relayStarted     atomic.Bool
	relayAttached    atomic.Bool
	relayAccepting   atomic.Bool
	relayHealthy     atomic.Bool
	relayEnqueued    atomic.Uint64
	relayPublished   atomic.Uint64
	relayDropped     atomic.Uint64
	relayLastSuccess atomic.Int64
	nextFailureLogAt atomic.Int64
}

type subscription struct {
	ch chan Event
}

// redisWire is the payload published to Redis (stable JSON).
type redisWire struct {
	ID     uint64          `json:"id"`
	Type   Type            `json:"type"`
	Time   time.Time       `json:"time"`
	Data   json.RawMessage `json:"data,omitempty"`
	Origin string          `json:"origin"`
}

type redisRelayConfig struct {
	queueCapacity int
}

// RedisRelayOption configures the bounded Redis fan-out worker.
type RedisRelayOption func(*redisRelayConfig)

// WithRedisRelayQueueCapacity configures the bounded outbound queue. Values at
// or below zero use the safe default; values above the hard maximum are
// clamped so an environment typo cannot create unbounded memory pressure.
func WithRedisRelayQueueCapacity(capacity int) RedisRelayOption {
	return func(cfg *redisRelayConfig) {
		cfg.queueCapacity = capacity
	}
}

// RedisRelayStatus is a point-in-time operational view of the bounded relay.
// Prometheus exposes the same health and throughput signals for alerting.
type RedisRelayStatus struct {
	Running     bool
	Healthy     bool
	QueueDepth  int
	Capacity    int
	Enqueued    uint64
	Published   uint64
	Dropped     uint64
	LastSuccess time.Time
}

// NewBus constructs a Bus.
func NewBus() *Bus {
	origin, _ := os.Hostname()
	if origin == "" {
		origin = "unknown"
	}
	return &Bus{
		subs:   make(map[*subscription]struct{}),
		origin: origin,
		log:    slog.Default(),
	}
}

// AttachRedis enables cross-pod fan-out. Safe to call once at startup.
// rdb may be a *redis.Client or other UniversalClient.
func (b *Bus) AttachRedis(rdb redis.UniversalClient, channel string, log *slog.Logger, opts ...RedisRelayOption) {
	if b == nil || rdb == nil {
		return
	}
	cfg := redisRelayConfig{queueCapacity: DefaultRedisRelayQueueCapacity}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	requestedCapacity := cfg.queueCapacity
	if cfg.queueCapacity <= 0 {
		cfg.queueCapacity = DefaultRedisRelayQueueCapacity
	}
	if cfg.queueCapacity > MaxRedisRelayQueueCapacity {
		cfg.queueCapacity = MaxRedisRelayQueueCapacity
	}
	if channel == "" {
		channel = DefaultRedisChannel
	}
	b.relayMu.Lock()
	defer b.relayMu.Unlock()
	if log != nil {
		b.log = log
	}
	if requestedCapacity > MaxRedisRelayQueueCapacity && b.log != nil {
		b.log.Warn("events redis relay queue capacity exceeds hard maximum; clamping",
			"requested_capacity", requestedCapacity,
			"effective_capacity", cfg.queueCapacity)
	}
	b.rdb = rdb
	b.channel = channel
	b.relayQueue = make(chan Event, cfg.queueCapacity)
	b.relayAttached.Store(true)
	b.relayAccepting.Store(true)
	observability.SetEventRelayQueue(0, cfg.queueCapacity)
	observability.SetEventRelayHealth(false)
}

// StartRedisRelay subscribes to the Redis channel and re-injects remote events
// into the local bus with Remote=true. Blocks until ctx is cancelled.
func (b *Bus) StartRedisRelay(ctx context.Context) {
	if b == nil {
		return
	}
	b.relayMu.RLock()
	rdb := b.rdb
	ch := b.channel
	b.relayMu.RUnlock()
	if rdb == nil {
		return
	}
	if ch == "" {
		ch = DefaultRedisChannel
	}
	relayCtx, cancel := context.WithCancel(ctx)
	done := b.startRedisPublisher(relayCtx)
	defer func() {
		cancel()
		<-done
	}()
	pubsub := rdb.Subscribe(relayCtx, ch)
	defer func() { _ = pubsub.Close() }()

	msgCh := pubsub.Channel()
	for {
		select {
		case <-relayCtx.Done():
			return
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			if msg == nil || msg.Payload == "" {
				continue
			}
			var wire redisWire
			if err := json.Unmarshal([]byte(msg.Payload), &wire); err != nil {
				if log := b.logger(); log != nil {
					log.Debug("events redis: bad payload", "error", err)
				}
				continue
			}
			if wire.Origin != "" && wire.Origin == b.origin {
				continue // echo of our own publish
			}
			var data any
			if len(wire.Data) > 0 {
				data = json.RawMessage(wire.Data)
			}
			e := Event{
				ID:     wire.ID,
				Type:   wire.Type,
				Time:   wire.Time,
				Data:   data,
				Remote: true,
				Origin: wire.Origin,
			}
			if e.ID == 0 {
				e.ID = b.nextID.Add(1)
			}
			if e.Time.IsZero() {
				e.Time = time.Now().UTC()
			}
			b.broadcastLocal(e)
		}
	}
}

// Publish broadcasts e to every active local subscriber and, when Redis is
// attached, to sibling pods. Local taps see Remote=false; remote pods inject
// with Remote=true.
func (b *Bus) Publish(t Type, data any) {
	if b == nil {
		return
	}
	e := Event{
		ID:     b.nextID.Add(1),
		Type:   t,
		Time:   time.Now().UTC(),
		Data:   data,
		Origin: b.origin,
	}
	b.broadcastLocal(e)
	b.enqueueRedis(e)
}

// PublishRemote injects an already-built remote event for tests (Remote=true).
func (b *Bus) PublishRemote(t Type, data any) {
	if b == nil {
		return
	}
	e := Event{
		ID:     b.nextID.Add(1),
		Type:   t,
		Time:   time.Now().UTC(),
		Data:   data,
		Remote: true,
		Origin: "test-remote",
	}
	b.broadcastLocal(e)
}

func (b *Bus) broadcastLocal(e Event) {
	b.mu.RLock()
	for s := range b.subs {
		select {
		case s.ch <- e:
		default:
			observability.RecordDroppedEvent("events_bus", "slow_subscriber")
		}
	}
	b.mu.RUnlock()
}

func (b *Bus) enqueueRedis(e Event) {
	if !b.relayAccepting.Load() {
		if b.relayAttached.Load() {
			b.recordRelayDrop("relay_not_running")
		}
		return
	}
	b.relayMu.RLock()
	queue := b.relayQueue
	b.relayMu.RUnlock()
	if queue == nil {
		return
	}
	select {
	case queue <- e:
		b.relayEnqueued.Add(1)
		observability.RecordEventRelayResult(observability.EventRelayResultEnqueued)
		observability.SetEventRelayQueue(len(queue), cap(queue))
	default:
		b.recordRelayDrop("queue_full")
	}
}

func (b *Bus) startRedisPublisher(ctx context.Context) <-chan struct{} {
	b.relayMu.Lock()
	if b.relayDone != nil {
		done := b.relayDone
		b.relayMu.Unlock()
		return done
	}
	done := make(chan struct{})
	b.relayDone = done
	queue := b.relayQueue
	rdb := b.rdb
	channel := b.channel
	if channel == "" {
		channel = DefaultRedisChannel
	}
	b.relayMu.Unlock()
	if queue == nil || rdb == nil {
		close(done)
		return done
	}

	b.relayStarted.Store(true)
	b.relayAccepting.Store(true)
	b.relayHealthy.Store(true)
	observability.SetEventRelayHealth(true)
	go b.runRedisPublisher(ctx, rdb, channel, queue, done)
	return done
}

func (b *Bus) runRedisPublisher(ctx context.Context, rdb redis.UniversalClient, channel string, queue <-chan Event, done chan<- struct{}) {
	defer close(done)
	defer func() {
		b.relayAccepting.Store(false)
		b.relayStarted.Store(false)
		b.relayHealthy.Store(false)
		observability.SetEventRelayHealth(false)
		observability.SetEventRelayQueue(len(queue), cap(queue))
	}()

	for {
		select {
		case <-ctx.Done():
			b.relayAccepting.Store(false)
			b.drainRedisQueue(rdb, channel, queue)
			return
		default:
		}
		select {
		case <-ctx.Done():
			b.relayAccepting.Store(false)
			b.drainRedisQueue(rdb, channel, queue)
			return
		case event := <-queue:
			observability.SetEventRelayQueue(len(queue), cap(queue))
			b.publishRedisEvent(ctx, rdb, channel, event)
		}
	}
}

func (b *Bus) drainRedisQueue(rdb redis.UniversalClient, channel string, queue <-chan Event) {
	drainCtx, cancel := context.WithTimeout(context.Background(), redisRelayShutdownTimeout)
	defer cancel()
	for {
		select {
		case event := <-queue:
			observability.SetEventRelayQueue(len(queue), cap(queue))
			b.publishRedisEvent(drainCtx, rdb, channel, event)
		case <-drainCtx.Done():
			b.dropQueuedRelayEvents(queue, "shutdown_timeout")
			return
		default:
			return
		}
	}
}

func (b *Bus) dropQueuedRelayEvents(queue <-chan Event, reason string) {
	for {
		select {
		case <-queue:
			b.recordRelayDrop(reason)
		default:
			observability.SetEventRelayQueue(0, cap(queue))
			return
		}
	}
}

func (b *Bus) publishRedisEvent(parent context.Context, rdb redis.UniversalClient, channel string, event Event) bool {
	var dataRaw json.RawMessage
	if event.Data != nil {
		raw, err := json.Marshal(event.Data)
		if err != nil {
			b.recordRelayDrop("serialization_failed")
			b.logRelayFailure("events redis serialization failed", err)
			return false
		}
		dataRaw = raw
	}
	payload, err := json.Marshal(redisWire{
		ID:     event.ID,
		Type:   event.Type,
		Time:   event.Time,
		Data:   dataRaw,
		Origin: event.Origin,
	})
	if err != nil {
		b.recordRelayDrop("serialization_failed")
		b.logRelayFailure("events redis serialization failed", err)
		return false
	}

	var publishErr error
	for attempt := 1; attempt <= redisRelayMaxAttempts; attempt++ {
		if err := parent.Err(); err != nil {
			publishErr = err
			break
		}
		attemptCtx, cancel := context.WithTimeout(parent, redisRelayPublishTimeout)
		started := time.Now()
		publishErr = rdb.Publish(attemptCtx, channel, payload).Err()
		observability.ObserveEventRelayPublish(started)
		cancel()
		if publishErr == nil {
			now := time.Now().UTC()
			b.relayPublished.Add(1)
			b.relayLastSuccess.Store(now.UnixNano())
			b.relayHealthy.Store(true)
			observability.RecordEventRelayResult(observability.EventRelayResultPublished)
			observability.SetEventRelayHealth(true)
			observability.SetEventRelayLastSuccess(now)
			return true
		}
		if attempt < redisRelayMaxAttempts {
			timer := time.NewTimer(redisRelayRetryBackoff)
			select {
			case <-parent.Done():
				if !timer.Stop() {
					<-timer.C
				}
				publishErr = parent.Err()
				attempt = redisRelayMaxAttempts
			case <-timer.C:
			}
		}
	}

	b.relayHealthy.Store(false)
	observability.SetEventRelayHealth(false)
	b.recordRelayDrop("publish_failed")
	if publishErr == nil {
		publishErr = errors.New("redis publish failed")
	}
	b.logRelayFailure("events redis publish failed", publishErr)
	return false
}

func (b *Bus) recordRelayDrop(reason string) {
	b.relayDropped.Add(1)
	observability.RecordEventRelayResult(observability.EventRelayResultDropped)
	observability.RecordDroppedEvent("events_redis_relay", reason)
}

func (b *Bus) logRelayFailure(message string, err error) {
	now := time.Now()
	nowUnix := now.UnixNano()
	for {
		next := b.nextFailureLogAt.Load()
		if next > nowUnix {
			return
		}
		if b.nextFailureLogAt.CompareAndSwap(next, now.Add(redisRelayLogInterval).UnixNano()) {
			if log := b.logger(); log != nil {
				log.Debug(message, "error", err, "log_suppression_window", redisRelayLogInterval)
			}
			return
		}
	}
}

func (b *Bus) logger() *slog.Logger {
	b.relayMu.RLock()
	defer b.relayMu.RUnlock()
	return b.log
}

// RelayStatus returns a lock-free status snapshot suitable for health/debug
// endpoints without exposing queue contents.
func (b *Bus) RelayStatus() RedisRelayStatus {
	if b == nil {
		return RedisRelayStatus{}
	}
	b.relayMu.RLock()
	queue := b.relayQueue
	b.relayMu.RUnlock()
	status := RedisRelayStatus{
		Running:   b.relayStarted.Load(),
		Healthy:   b.relayHealthy.Load(),
		Enqueued:  b.relayEnqueued.Load(),
		Published: b.relayPublished.Load(),
		Dropped:   b.relayDropped.Load(),
	}
	if queue != nil {
		status.QueueDepth = len(queue)
		status.Capacity = cap(queue)
	}
	if value := b.relayLastSuccess.Load(); value > 0 {
		status.LastSuccess = time.Unix(0, value).UTC()
	}
	return status
}

// Subscribe returns a channel that receives events until ctx is cancelled.
// Buffer is sized to absorb modest bursts; further bursts are dropped.
func (b *Bus) Subscribe(ctx context.Context) <-chan Event {
	s := &subscription{ch: make(chan Event, 64)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		delete(b.subs, s)
		b.mu.Unlock()
		close(s.ch)
	}()

	return s.ch
}

// MarshalJSON exists so callers can send events as bytes without re-encoding.
// Useful for SSE writers that need a stable JSON shape.
func (e Event) MarshalJSON() ([]byte, error) {
	type alias Event
	return json.Marshal(alias(e))
}
