// Package events implements an in-memory pub/sub bus used to push lifecycle
// events to long-lived API consumers (SSE / WebSocket subscribers).
//
// The bus is intentionally minimal: events are best-effort and slow consumers
// drop frames rather than block the producer.
package events

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
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
)

// Event is a single push payload. Data is opaque JSON-marshalable per Type.
type Event struct {
	ID   uint64    `json:"id"`
	Type Type      `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data,omitempty"`
}

// Bus is a multi-subscriber, lossy-on-slow-consumer in-memory pub/sub.
type Bus struct {
	mu      sync.RWMutex
	subs    map[*subscription]struct{}
	nextID  atomic.Uint64
}

type subscription struct {
	ch chan Event
}

// NewBus constructs a Bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[*subscription]struct{})}
}

// Publish broadcasts e to every active subscriber. Slow subscribers (channel
// full) drop the event rather than block the publisher.
func (b *Bus) Publish(t Type, data any) {
	if b == nil {
		return
	}
	e := Event{
		ID:   b.nextID.Add(1),
		Type: t,
		Time: time.Now().UTC(),
		Data: data,
	}
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
