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

// `<resource>.changed` types published via PublishChanged (see publish.go for
// the envelope contract). Cluster-scoped types MUST carry `cluster_id` in
// their payload — the SSE stream drops events without it fail-closed for
// restricted users (SEC-R07). `admin_queue.changed` and `siem_forwarder.changed`
// are deliberately unscoped: the fail-closed drop makes them superuser-only.
const (
	// TypeBackupChanged fires when a backup, restore, or schedule row is
	// written (payload `kind` field: backup|restore|schedule).
	TypeBackupChanged Type = "backup.changed"

	// TypeFleetOperationChanged fires per fleet-operation target with the
	// target's cluster_id.
	TypeFleetOperationChanged Type = "fleet_operation.changed"

	// TypeLoggingOperationChanged fires when a logging stack operation row
	// is written.
	TypeLoggingOperationChanged Type = "logging_operation.changed"

	// TypeToolOperationChanged fires when a tool operation row is written.
	TypeToolOperationChanged Type = "tool_operation.changed"

	// TypeCISScanChanged fires when a CIS benchmark scan row is written.
	TypeCISScanChanged Type = "cis_scan.changed"

	// TypeImageScanChanged fires when an image vulnerability scan row is
	// written.
	TypeImageScanChanged Type = "image_scan.changed"

	// TypeArgoCDChanged fires on own ArgoCD writes and at the end of each
	// server-side reconcile pass (payload `scope` field).
	TypeArgoCDChanged Type = "argocd.changed"

	// TypeAdminQueueChanged fires when the admin task queue mutates.
	// Unscoped (no cluster_id): superuser-only by fail-closed drop.
	TypeAdminQueueChanged Type = "admin_queue.changed"

	// TypeSIEMForwarderChanged fires when a SIEM forwarder row is written.
	// Unscoped (no cluster_id): superuser-only by fail-closed drop.
	TypeSIEMForwarderChanged Type = "siem_forwarder.changed"

	// TypeAgentFleetChanged fires when an agent fleet row is written.
	TypeAgentFleetChanged Type = "agent_fleet.changed"

	// TypeTemplateBindingChanged fires when a template binding row is
	// written.
	TypeTemplateBindingChanged Type = "template_binding.changed"

	// TypeRegistryChanged fires when a registry row is written.
	TypeRegistryChanged Type = "registry.changed"

	// TypeSnapshotChanged fires when a snapshot row is written.
	TypeSnapshotChanged Type = "snapshot.changed"
)

// DefaultRedisChannel is the pub/sub channel for cross-pod event fan-out.
const DefaultRedisChannel = "astronomer:events:v1"

// Event is a single push payload. Data is opaque JSON-marshalable per Type.
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

	rdb     redis.UniversalClient
	channel string
	origin  string
	log     *slog.Logger
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
func (b *Bus) AttachRedis(rdb redis.UniversalClient, channel string, log *slog.Logger) {
	if b == nil || rdb == nil {
		return
	}
	if channel == "" {
		channel = DefaultRedisChannel
	}
	if log != nil {
		b.log = log
	}
	b.rdb = rdb
	b.channel = channel
}

// StartRedisRelay subscribes to the Redis channel and re-injects remote events
// into the local bus with Remote=true. Blocks until ctx is cancelled.
func (b *Bus) StartRedisRelay(ctx context.Context) {
	if b == nil || b.rdb == nil {
		return
	}
	ch := b.channel
	if ch == "" {
		ch = DefaultRedisChannel
	}
	pubsub := b.rdb.Subscribe(ctx, ch)
	defer func() { _ = pubsub.Close() }()

	msgCh := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
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
				if b.log != nil {
					b.log.Debug("events redis: bad payload", "error", err)
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
	b.publishRedis(e)
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

func (b *Bus) publishRedis(e Event) {
	if b.rdb == nil {
		return
	}
	ch := b.channel
	if ch == "" {
		ch = DefaultRedisChannel
	}
	var dataRaw json.RawMessage
	if e.Data != nil {
		raw, err := json.Marshal(e.Data)
		if err != nil {
			return
		}
		dataRaw = raw
	}
	payload, err := json.Marshal(redisWire{
		ID:     e.ID,
		Type:   e.Type,
		Time:   e.Time,
		Data:   dataRaw,
		Origin: e.Origin,
	})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.rdb.Publish(ctx, ch, payload).Err(); err != nil && b.log != nil {
		b.log.Debug("events redis publish failed", "error", err)
	}
}

// Subscribe returns a channel that receives events until ctx is cancelled.
// Buffer is sized to absorb informer/publisher bursts (T6: 64→256 for the
// P4.5 domain-publisher expansion); further bursts are dropped and counted
// via RecordDroppedEvent (alertable; D7 threshold 0.1%/24h drives the
// post-merge coalescing follow-up).
func (b *Bus) Subscribe(ctx context.Context) <-chan Event {
	s := &subscription{ch: make(chan Event, 256)}
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
