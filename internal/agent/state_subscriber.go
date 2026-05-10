package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Default tunables for the agent-side informer fan-out. Exposed as package
// vars (not consts) so tests can override without changing public API.
var (
	// stateSubscriberResyncPeriod is the SharedInformerFactory resync interval.
	// Long resyncs avoid hammering the apiserver while still catching anything
	// the informer missed during a partial reconnect.
	stateSubscriberResyncPeriod = 30 * time.Minute

	// stateSubscriberMinInterval is the per-key minimum gap between two emits.
	// Configured to 1s to absorb the burst when a Deployment scales (hundreds
	// of Pod updates per second arrive at the informer); each key still gets
	// at least one update per second.
	stateSubscriberMinInterval = 1 * time.Second

	// stateSubscriberEvictAfter is how long an unused key is kept in the
	// rate-limiter map. Anything older is purged to keep the map bounded for
	// long-running agents that touch many resources.
	stateSubscriberEvictAfter = 60 * time.Second

	// stateSubscriberEvictEvery is the eviction goroutine's tick interval.
	stateSubscriberEvictEvery = 1 * time.Minute

	// stateSubscriberEventCutoff filters out historical Events on initial list.
	// Without this, a fresh agent would emit thousands of stale Events on
	// startup; we only forward Events that are newer than this window.
	stateSubscriberEventCutoff = 5 * time.Minute
)

// stateSender is the minimal slice of TunnelClient the subscriber needs.
// Defining the interface here (instead of taking *TunnelClient directly)
// keeps the subscriber unit-testable without spinning up a tunnel.
type stateSender interface {
	Send(msg *protocol.Message) error
}

// stateRateLimiter collapses bursty informer events to at most one emit per
// stateSubscriberMinInterval per key.
//
// Data structure: a map keyed by `kind|namespace|name` with the timestamp of
// the last accepted emit. A separate eviction goroutine prunes entries older
// than stateSubscriberEvictAfter every stateSubscriberEvictEvery so the map
// can't grow unbounded if a cluster churns through many short-lived objects.
type stateRateLimiter struct {
	mu          sync.Mutex
	last        map[string]time.Time
	minInterval time.Duration
	evictAfter  time.Duration
	now         func() time.Time
}

func newStateRateLimiter(minInterval, evictAfter time.Duration) *stateRateLimiter {
	return &stateRateLimiter{
		last:        make(map[string]time.Time),
		minInterval: minInterval,
		evictAfter:  evictAfter,
		now:         time.Now,
	}
}

// Allow returns true if a new emit for the given key is permitted. It
// atomically updates the last-emit timestamp on accept so concurrent
// goroutines can't both pass the gate.
func (r *stateRateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if prev, ok := r.last[key]; ok && now.Sub(prev) < r.minInterval {
		return false
	}
	r.last[key] = now
	return true
}

// evictOlderThan drops any key whose last-emit timestamp is older than the
// given cutoff. Called by the eviction goroutine.
func (r *stateRateLimiter) evictOlderThan(cutoff time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped := 0
	for k, t := range r.last {
		if t.Before(cutoff) {
			delete(r.last, k)
			dropped++
		}
	}
	return dropped
}

// size returns the current number of tracked keys (for tests / metrics).
func (r *stateRateLimiter) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.last)
}

// StateSubscriber starts a SharedInformerFactory and forwards add/update/
// delete events as protocol.MsgStateUpdate messages over the tunnel. It is
// deliberately decoupled from the rest of the agent: if the ServiceAccount
// lacks list/watch on a resource, the corresponding informer fails to sync
// and is logged but never crashes the process.
type StateSubscriber struct {
	client  kubernetes.Interface
	sender  stateSender
	log     *slog.Logger
	limiter *stateRateLimiter
	ready   atomic.Bool
	readyCh chan struct{}
	once    sync.Once

	// startedAt is captured before the informer factory starts so the Events
	// filter can drop pre-existing items (resyncs include them as Adds).
	startedAt time.Time
}

// NewStateSubscriber constructs a StateSubscriber. The sender must be a live
// tunnel client; the subscriber will never block on it (Send returns an error
// if the channel is full and the message is dropped).
func NewStateSubscriber(client kubernetes.Interface, sender stateSender, log *slog.Logger) *StateSubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &StateSubscriber{
		client:    client,
		sender:    sender,
		log:       log,
		limiter:   newStateRateLimiter(stateSubscriberMinInterval, stateSubscriberEvictAfter),
		readyCh:   make(chan struct{}),
		startedAt: time.Now(),
	}
}

// WaitReady blocks until the informer caches have synced and the subscriber is
// ready to emit live updates, or until ctx is cancelled.
func (s *StateSubscriber) WaitReady(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if s.ready.Load() {
		return true
	}
	select {
	case <-s.readyCh:
		return true
	case <-ctx.Done():
		return false
	}
}

// Run blocks until ctx is cancelled. It builds the informer factory, wires
// per-resource handlers, and starts the eviction goroutine. RBAC failures
// during initial sync are logged at WARN; the agent continues to run.
func (s *StateSubscriber) Run(ctx context.Context) {
	if s.client == nil {
		s.log.Warn("state subscriber: nil clientset, skipping live updates")
		return
	}

	factory := informers.NewSharedInformerFactory(s.client, stateSubscriberResyncPeriod)

	// Register per-resource handlers. The handler funcs share the same
	// dispatch logic; only the Kind / API group differ.
	s.registerCore(factory)
	s.registerApps(factory)
	s.registerEvents(factory)
	s.log.Debug("state subscriber: handlers registered, starting factory")

	stopCh := make(chan struct{})
	defer close(stopCh)

	factory.Start(stopCh)

	// Wait for the initial list to populate; if some informers fail to sync
	// (typically RBAC), log and continue. We do NOT abort: a partial subscriber
	// is still better than polling-only.
	synced := factory.WaitForCacheSync(stopCh)
	for typ, ok := range synced {
		if !ok {
			s.log.Warn("state subscriber: cache failed to sync (RBAC?)", "type", fmt.Sprintf("%T", typ))
		}
	}
	s.ready.Store(true)
	s.once.Do(func() { close(s.readyCh) })
	s.log.Info("state subscriber started", "resync_period", stateSubscriberResyncPeriod.String())

	// Eviction goroutine.
	evictTicker := time.NewTicker(stateSubscriberEvictEvery)
	defer evictTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-evictTicker.C:
			cutoff := time.Now().Add(-stateSubscriberEvictAfter)
			if dropped := s.limiter.evictOlderThan(cutoff); dropped > 0 {
				s.log.Debug("state subscriber: evicted rate-limiter keys", "dropped", dropped)
			}
		}
	}
}

// registerCore wires informers for core/v1 resources. ConfigMaps and Secrets
// fan out their *metadata only* — never `data` — so a leaked secret value
// can't ride the tunnel.
func (s *StateSubscriber) registerCore(factory informers.SharedInformerFactory) {
	core := factory.Core().V1()

	s.attach(core.Pods().Informer(), "Pod", "", "v1")
	s.attach(core.Services().Informer(), "Service", "", "v1")
	s.attach(core.Nodes().Informer(), "Node", "", "v1")
	s.attach(core.ConfigMaps().Informer(), "ConfigMap", "", "v1")
	s.attach(core.Secrets().Informer(), "Secret", "", "v1")
}

// registerApps wires informers for apps/v1 resources.
func (s *StateSubscriber) registerApps(factory informers.SharedInformerFactory) {
	apps := factory.Apps().V1()

	s.attach(apps.Deployments().Informer(), "Deployment", "apps", "v1")
	s.attach(apps.ReplicaSets().Informer(), "ReplicaSet", "apps", "v1")
	s.attach(apps.StatefulSets().Informer(), "StatefulSet", "apps", "v1")
	s.attach(apps.DaemonSets().Informer(), "DaemonSet", "apps", "v1")
}

// registerEvents wires the events informer with a freshness filter.
func (s *StateSubscriber) registerEvents(factory informers.SharedInformerFactory) {
	ev := factory.Events().V1().Events().Informer()
	_, _ = ev.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			meta, ok := metaFromObj(obj)
			if !ok {
				return
			}
			if !s.ready.Load() {
				return
			}
			// Drop Events older than the cutoff so we don't flood on startup.
			if !s.eventIsRecent(obj) {
				return
			}
			s.dispatch(protocol.StateUpdateOpAdded, "Event", "events.k8s.io", "v1", meta)
		},
		UpdateFunc: func(_, newObj any) {
			meta, ok := metaFromObj(newObj)
			if !ok {
				return
			}
			if !s.ready.Load() {
				return
			}
			if !s.eventIsRecent(newObj) {
				return
			}
			s.dispatch(protocol.StateUpdateOpModified, "Event", "events.k8s.io", "v1", meta)
		},
		DeleteFunc: func(obj any) {
			meta, ok := metaFromObj(obj)
			if !ok {
				return
			}
			if !s.ready.Load() {
				return
			}
			s.dispatch(protocol.StateUpdateOpDeleted, "Event", "events.k8s.io", "v1", meta)
		},
	})
}

// attach registers Add/Update/Delete handlers that build StateUpdatePayloads.
// All three handlers share a dispatch helper so the rate-limiter and serialize
// logic only lives in one place.
func (s *StateSubscriber) attach(inf cache.SharedIndexInformer, kind, apiGroup, apiVersion string) {
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if meta, ok := metaFromObj(obj); ok {
				if !s.ready.Load() {
					s.log.Debug("state subscriber: suppressing bootstrap add", "kind", kind, "namespace", meta.GetNamespace(), "name", meta.GetName())
					return
				}
				s.dispatch(protocol.StateUpdateOpAdded, kind, apiGroup, apiVersion, meta)
			}
		},
		UpdateFunc: func(_, newObj any) {
			if meta, ok := metaFromObj(newObj); ok {
				if !s.ready.Load() {
					return
				}
				s.dispatch(protocol.StateUpdateOpModified, kind, apiGroup, apiVersion, meta)
			}
		},
		DeleteFunc: func(obj any) {
			if meta, ok := metaFromObj(obj); ok {
				if !s.ready.Load() {
					return
				}
				s.dispatch(protocol.StateUpdateOpDeleted, kind, apiGroup, apiVersion, meta)
			}
		},
	})
	if err != nil {
		s.log.Warn("state subscriber: failed to register handler", "kind", kind, "error", err)
	}
}

// dispatch builds a payload, runs it through the rate limiter, and sends it.
// Per-event logging is at DEBUG level so the success path doesn't flood logs;
// failures (marshal/send) are at WARN so they surface during outage triage.
func (s *StateSubscriber) dispatch(op protocol.StateUpdateOp, kind, apiGroup, apiVersion string, meta metav1.Object) {
	key := fmt.Sprintf("%s|%s|%s", kind, meta.GetNamespace(), meta.GetName())
	s.log.Debug("state subscriber received event", "op", op, "kind", kind, "namespace", meta.GetNamespace(), "name", meta.GetName())
	agentStateUpdatesReceivedTotal.WithLabelValues(observability.MetricValues(kind)...).Inc()
	if !s.limiter.Allow(key) {
		agentStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("rate_limited", kind)...).Inc()
		s.log.Debug("state subscriber rate-limited", "key", key)
		return
	}
	payload := protocol.StateUpdatePayload{
		Op:              op,
		Kind:            kind,
		APIGroup:        apiGroup,
		APIVersion:      apiVersion,
		Namespace:       meta.GetNamespace(),
		Name:            meta.GetName(),
		ResourceVersion: meta.GetResourceVersion(),
		CoalesceKey:     key,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		agentStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("marshal_failed", kind)...).Inc()
		s.log.Warn("state subscriber: marshal failed", "kind", kind, "error", err)
		return
	}
	msg := &protocol.Message{
		Type:      protocol.MsgStateUpdate,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}
	if err := s.sender.Send(msg); err != nil {
		agentStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("send_failed", kind)...).Inc()
		// The send channel is bounded; on overflow we drop and let the next
		// emit win the race.
		s.log.Warn("state subscriber: send failed", "kind", kind, "error", err)
		return
	}
	agentStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("queued", kind)...).Inc()
	s.log.Debug("state subscriber sent", "kind", kind, "namespace", meta.GetNamespace(), "name", meta.GetName())
}

// eventIsRecent decides whether a corev1/eventsv1 Event is fresh enough to
// forward. The informer replays old events as Adds during initial list /
// resync; without this filter we'd flood on every reconnect.
func (s *StateSubscriber) eventIsRecent(obj any) bool {
	cutoff := time.Now().Add(-stateSubscriberEventCutoff)
	switch e := obj.(type) {
	case *eventsv1.Event:
		ts := e.EventTime.Time
		if ts.IsZero() {
			ts = e.CreationTimestamp.Time
		}
		return ts.After(cutoff)
	case *corev1.Event:
		ts := e.LastTimestamp.Time
		if ts.IsZero() {
			ts = e.CreationTimestamp.Time
		}
		return ts.After(cutoff)
	default:
		// Unknown event shape: be conservative and forward.
		return true
	}
}

// metaFromObj extracts metav1.Object from typed informer objects. Tombstones
// (DeletedFinalStateUnknown) are unwrapped so deletes still emit when the
// informer missed the actual Delete event.
func metaFromObj(obj any) (metav1.Object, bool) {
	if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tomb.Obj
	}
	switch v := obj.(type) {
	case metav1.Object:
		return v, true
	}
	// Fall through: Kubernetes types embed ObjectMeta but the typed informer
	// hands us the typed pointer, so the metav1.Object assertion above
	// usually wins. As a defensive fallback, try the well-known concrete
	// types we register.
	switch v := obj.(type) {
	case *corev1.Pod:
		return &v.ObjectMeta, true
	case *corev1.Service:
		return &v.ObjectMeta, true
	case *corev1.Node:
		return &v.ObjectMeta, true
	case *corev1.ConfigMap:
		return &v.ObjectMeta, true
	case *corev1.Secret:
		return &v.ObjectMeta, true
	case *corev1.Event:
		return &v.ObjectMeta, true
	case *appsv1.Deployment:
		return &v.ObjectMeta, true
	case *appsv1.ReplicaSet:
		return &v.ObjectMeta, true
	case *appsv1.StatefulSet:
		return &v.ObjectMeta, true
	case *appsv1.DaemonSet:
		return &v.ObjectMeta, true
	case *eventsv1.Event:
		return &v.ObjectMeta, true
	}
	return nil, false
}
