package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Default tunables for the agent-side informer fan-out. Exposed as package
// Tunables stored as atomic.Int64 (nanoseconds) so tests can mutate
// them without racing against the Run goroutine's reads. Use the
// matching get/set helpers below; never read or write the variables
// directly. Pre-existing test infrastructure race documented in
// a cleanup pass.
var (
	// stateSubscriberResyncPeriod is the SharedInformerFactory resync
	// interval. Long resyncs avoid hammering the apiserver while still
	// catching anything the informer missed during a partial reconnect.
	stateSubscriberResyncPeriod atomic.Int64

	// stateSubscriberMinInterval is the per-key minimum gap between
	// two emits. Configured to 1s to absorb the burst when a
	// Deployment scales (hundreds of Pod updates per second arrive at
	// the informer); each key still gets at least one update per
	// second.
	stateSubscriberMinInterval atomic.Int64

	// stateSubscriberEvictAfter is how long an unused key is kept in
	// the rate-limiter map. Anything older is purged to keep the map
	// bounded for long-running agents that touch many resources.
	stateSubscriberEvictAfter atomic.Int64

	// stateSubscriberEvictEvery is the eviction goroutine's tick
	// interval.
	stateSubscriberEvictEvery atomic.Int64

	// stateSubscriberEventCutoff filters out historical Events on
	// initial list. Without this, a fresh agent would emit thousands
	// of stale Events on startup; we only forward Events that are
	// newer than this window.
	stateSubscriberEventCutoff atomic.Int64

	// stateSubscriberCRDRetry is the interval between attempts to bring
	// up an informer for a discover-if-present CRD (Velero, Argo,
	// Gatekeeper, Trivy). The CRD may be installed after the agent
	// starts, so absence is retried forever rather than treated as
	// fatal (P4.6).
	stateSubscriberCRDRetry atomic.Int64

	// stateSubscriberCRDSyncTimeout bounds each per-attempt cache-sync
	// wait for a CRD informer. Long enough for a healthy apiserver;
	// short enough that an unbacked GVR retries promptly.
	stateSubscriberCRDSyncTimeout atomic.Int64
)

func init() {
	stateSubscriberResyncPeriod.Store(int64(30 * time.Minute))
	stateSubscriberMinInterval.Store(int64(1 * time.Second))
	stateSubscriberEvictAfter.Store(int64(60 * time.Second))
	stateSubscriberEvictEvery.Store(int64(1 * time.Minute))
	stateSubscriberEventCutoff.Store(int64(5 * time.Minute))
	stateSubscriberCRDRetry.Store(int64(60 * time.Second))
	stateSubscriberCRDSyncTimeout.Store(int64(20 * time.Second))
}

// getStateSubscriberMinInterval and friends provide race-free reads
// against tunables that tests are allowed to mutate at runtime. Reading
// the atomic.Int64 directly via .Load() is what we do, but the helpers
// give a single Duration value so call sites don't repeat the cast.
func getStateSubscriberResyncPeriod() time.Duration {
	return time.Duration(stateSubscriberResyncPeriod.Load())
}
func getStateSubscriberMinInterval() time.Duration {
	return time.Duration(stateSubscriberMinInterval.Load())
}
func getStateSubscriberEvictAfter() time.Duration {
	return time.Duration(stateSubscriberEvictAfter.Load())
}
func getStateSubscriberEvictEvery() time.Duration {
	return time.Duration(stateSubscriberEvictEvery.Load())
}
func getStateSubscriberEventCutoff() time.Duration {
	return time.Duration(stateSubscriberEventCutoff.Load())
}
func getStateSubscriberCRDRetry() time.Duration {
	return time.Duration(stateSubscriberCRDRetry.Load())
}
func getStateSubscriberCRDSyncTimeout() time.Duration {
	return time.Duration(stateSubscriberCRDSyncTimeout.Load())
}

// metadataKind pairs the wire labels a StateUpdate carries with the GVR
// its metadata informer watches (P4.6 informer expansion).
type metadataKind struct {
	kind       string
	apiGroup   string
	apiVersion string
	gvr        schema.GroupVersionResource
}

// metadataInformerKinds is the P4.6 expansion set: built-in kinds watched
// metadata-only (same MsgStateUpdate shape, no bodies, no secret surface).
// Always present on any supported apiserver, so they share the main
// metadata factory; RBAC-denied informers fail to sync and are logged
// without crashing the subscriber (bounded sync wait in Run).
var metadataInformerKinds = []metadataKind{
	{"Namespace", "", "v1", schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}},
	{"Job", "batch", "v1", schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}},
	{"CronJob", "batch", "v1", schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}},
	{"Ingress", "networking.k8s.io", "v1", schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}},
	{"NetworkPolicy", "networking.k8s.io", "v1", schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}},
	{"PersistentVolume", "", "v1", schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}},
	{"PersistentVolumeClaim", "", "v1", schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}},
	{"StorageClass", "storage.k8s.io", "v1", schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}},
	{"HorizontalPodAutoscaler", "autoscaling", "v2", schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}},
	{"ServiceAccount", "", "v1", schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}},
	{"Role", "rbac.authorization.k8s.io", "v1", schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}},
	{"RoleBinding", "rbac.authorization.k8s.io", "v1", schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}},
	{"ClusterRole", "rbac.authorization.k8s.io", "v1", schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}},
	{"ClusterRoleBinding", "rbac.authorization.k8s.io", "v1", schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}},
	{"ResourceQuota", "", "v1", schema.GroupVersionResource{Version: "v1", Resource: "resourcequotas"}},
}

// crdInformerKinds are the discover-if-present CRDs. Each gets its own
// retry loop (mirroring MirrorSubscriber.runDynamicGVR) because the CRD
// may be installed after the agent starts and a missing GVR must never
// stall the main factory's readiness.
var crdInformerKinds = []metadataKind{
	{"Backup", "velero.io", "v1", schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backups"}},
	{"Restore", "velero.io", "v1", schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "restores"}},
	{"Schedule", "velero.io", "v1", schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "schedules"}},
	{"Application", "argoproj.io", "v1alpha1", schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}},
	{"ApplicationSet", "argoproj.io", "v1alpha1", schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applicationsets"}},
	{"VulnerabilityReport", "aquasecurity.github.io", "v1alpha1", schema.GroupVersionResource{Group: "aquasecurity.github.io", Version: "v1alpha1", Resource: "vulnerabilityreports"}},
}

// gatekeeperConstraintsGV is the group/version whose resources are
// discovered dynamically: every ConstraintTemplate creates a new CRD in
// this group, so the resource list can't be pinned at compile time.
const gatekeeperConstraintsGV = "constraints.gatekeeper.sh/v1beta1"

// helm secret filtering (P4.6/P4.9 installed charts): only Helm release
// storage secrets are forwarded by the dedicated metadata informer —
// non-Helm secret metadata never rides this path.
const (
	helmSecretType       = "helm.sh/release.v1"
	helmSecretNamePrefix = "sh.helm.release.v1."
)

// stateSender is the minimal slice of TunnelClient the subscriber needs.
// Defining the interface here (instead of taking *TunnelClient directly)
// keeps the subscriber unit-testable without spinning up a tunnel.
type stateSender interface {
	Send(msg *protocol.Message) error
}

// StateConnectionWatcher is the narrow tunnel-state surface the reconnect-
// replay goroutine polls. The agent's TunnelClient satisfies it via
// IsConnected. Mirrors MirrorConnectionWatcher so the two subscribers share the
// same proven replay shape. (L12 / C3.)
type StateConnectionWatcher interface {
	IsConnected() bool
}

// stateStoreEntry pairs an informer's cache.Store with the GVK labels the
// dispatch path needs so replayAll can re-emit every cached object without
// re-deriving them per item.
type stateStoreEntry struct {
	kind       string
	apiGroup   string
	apiVersion string
	store      cache.Store
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

	// meta is the metadata-only client for the P4.6 informer expansion
	// (built-in kinds beyond the typed set, Helm release Secrets, and
	// discover-if-present CRDs). Optional; nil keeps the pre-P4.6
	// typed-informer-only behavior.
	meta metadata.Interface

	// startedAt is captured before the informer factory starts so the Events
	// filter can drop pre-existing items (resyncs include them as Adds).
	startedAt time.Time

	// watchSecrets gates the Secret informer. The viewer/namespace-* profiles
	// deliberately lack secret RBAC, so starting the informer there just
	// error-loops on Forbidden. Defaults false so a caller omission cannot
	// silently add a sensitive informer; SetWatchSecrets enables it only for an
	// explicitly compatible normalized profile.
	watchSecrets bool

	// stores holds the durable-kind informer caches so the reconnect-replay
	// goroutine can re-emit them on a tunnel false→true transition (L12 / C3).
	// Keyed by kind; populated by attach() as each informer is registered. The
	// Events informer is deliberately EXCLUDED (replaying historical Events
	// would flood — exactly what eventIsRecent guards against).
	storeMu sync.RWMutex
	stores  map[string]stateStoreEntry

	// conn is the connection watcher whose IsConnected reading the replay loop
	// polls. nil-safe: when unwired (tests / older callers) the replay goroutine
	// is never started, so behavior is exactly as before.
	conn StateConnectionWatcher
}

// NewStateSubscriber constructs a StateSubscriber. The sender must be a live
// tunnel client; the subscriber will never block on it (Send returns an error
// if the channel is full and the message is dropped).
func NewStateSubscriber(client kubernetes.Interface, sender stateSender, log *slog.Logger) *StateSubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &StateSubscriber{
		client:       client,
		sender:       sender,
		log:          log,
		limiter:      newStateRateLimiter(getStateSubscriberMinInterval(), getStateSubscriberEvictAfter()),
		readyCh:      make(chan struct{}),
		startedAt:    time.Now(),
		watchSecrets: false,
		stores:       make(map[string]stateStoreEntry),
	}
}

// SetConnectionWatcher wires the tunnel connection watcher so the reconnect-
// replay loop can detect false→true transitions and re-emit every cached
// informer object to the (newly-reconnected) management plane. Optional; nil =
// no replay goroutine (the legacy behavior). (L12 / C3.)
func (s *StateSubscriber) SetConnectionWatcher(c StateConnectionWatcher) {
	if s != nil {
		s.conn = c
	}
}

// SetMetadataClient wires the metadata-only client used by the expanded
// informer set (P4.6). Call before Run. Optional; nil skips the expansion
// and keeps the typed informer set only.
func (s *StateSubscriber) SetMetadataClient(mc metadata.Interface) {
	if s != nil {
		s.meta = mc
	}
}

// SetWatchSecrets enables/disables the Secret informer. Call before Run with
// the result of ProfileAllowsSecrets so read-only, custom, and unknown
// profiles don't error-loop or silently widen into a sensitive secrets watch.
func (s *StateSubscriber) SetWatchSecrets(enabled bool) {
	if s != nil {
		s.watchSecrets = enabled
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

	factory := informers.NewSharedInformerFactory(s.client, getStateSubscriberResyncPeriod())

	// Register per-resource handlers. The handler funcs share the same
	// dispatch logic; only the Kind / API group differ.
	s.registerCore(factory)
	s.registerApps(factory)
	s.registerEvents(factory)
	s.log.Debug("state subscriber: handlers registered, starting factory")

	stopCh := make(chan struct{})
	defer close(stopCh)

	factory.Start(stopCh)

	// P4.6 informer expansion: metadata-only informers for the built-in
	// kinds beyond the typed set, a Helm-release-filtered Secret informer,
	// per-CRD retry loops (tolerate absence), and gatekeeper-constraint
	// discovery. All reuse the shared dispatch path (per-key 1/s limiter)
	// and, where recorded, the reconnect replay.
	var metaFactory metadatainformer.SharedInformerFactory
	if s.meta != nil {
		metaFactory = metadatainformer.NewSharedInformerFactory(s.meta, getStateSubscriberResyncPeriod())
		for _, k := range metadataInformerKinds {
			s.attach(metaFactory.ForResource(k.gvr).Informer(), k.kind, k.apiGroup, k.apiVersion)
		}
		metaFactory.Start(stopCh)
		s.startHelmSecretInformer(stopCh)
		for _, k := range crdInformerKinds {
			go s.runCRDInformer(ctx, k, stopCh)
		}
		go s.runGatekeeperConstraints(ctx, stopCh)
	}

	// Wait for the initial list to populate; if some informers fail to sync
	// (typically RBAC), log and continue. We do NOT abort: a partial subscriber
	// is still better than polling-only.
	synced := factory.WaitForCacheSync(stopCh)
	for typ, ok := range synced {
		if !ok {
			s.log.Warn("state subscriber: cache failed to sync (RBAC?)", "type", fmt.Sprintf("%T", typ))
		}
	}
	if metaFactory != nil {
		// Bounded wait: a single RBAC-denied metadata informer must not
		// stall readiness forever (that would suppress every typed kind's
		// events too — the ready gate is subscriber-wide).
		syncStop := make(chan struct{})
		go func() {
			select {
			case <-stopCh:
			case <-time.After(30 * time.Second):
			}
			close(syncStop)
		}()
		msynced := metaFactory.WaitForCacheSync(syncStop)
		for typ, ok := range msynced {
			if !ok {
				s.log.Warn("state subscriber: metadata cache failed to sync (RBAC?)", "type", fmt.Sprintf("%v", typ))
			}
		}
	}
	s.ready.Store(true)
	s.once.Do(func() { close(s.readyCh) })
	s.log.Info("state subscriber started", "resync_period", getStateSubscriberResyncPeriod().String())

	// Reconnect replay (L12 / C3): on every tunnel false→true transition,
	// re-emit the durable informer caches so a UI relying on pushed state isn't
	// stale until the next 30m resync. Started only when a connection watcher is
	// wired AND after caches have synced (ready=true), so the stores are
	// populated. No-op when unwired.
	if s.conn != nil {
		go s.runReconnectReplay(ctx)
	}

	// Eviction goroutine.
	evictTicker := time.NewTicker(getStateSubscriberEvictEvery())
	defer evictTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-evictTicker.C:
			cutoff := time.Now().Add(-getStateSubscriberEvictAfter())
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
	if s.watchSecrets {
		s.attach(core.Secrets().Informer(), "Secret", "", "v1")
	} else {
		s.log.Debug("secret informer disabled for read-only profile")
	}
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
	// Record the store so reconnect-replay can re-emit this kind's cache.
	s.recordStore(kind, apiGroup, apiVersion, inf.GetStore())
	_, err := inf.AddEventHandler(s.handlers(kind, apiGroup, apiVersion))
	if err != nil {
		s.log.Warn("state subscriber: failed to register handler", "kind", kind, "error", err)
	}
}

// handlers builds the shared Add/Update/Delete handler set for a kind so
// the typed factory, the metadata factory, and the CRD retry loops all
// funnel through the same ready-gate + dispatch path.
func (s *StateSubscriber) handlers(kind, apiGroup, apiVersion string) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
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
	}
}

// startHelmSecretInformer runs a metadata informer over Secrets filtered to
// Helm release storage only: server-side by the `type` field selector, plus
// a defensive agent-side name-prefix check — non-Helm secret metadata is
// never forwarded on this path. Gated on watchSecrets exactly like the
// typed Secret informer so read-only profiles don't error-loop on
// Forbidden. When the typed Secret informer also runs, the per-key rate
// limiter collapses the duplicate emits (same `Secret|ns|name` key). Not
// recorded for reconnect replay — the typed Secret store already covers
// these objects.
func (s *StateSubscriber) startHelmSecretInformer(stopCh <-chan struct{}) {
	if !s.watchSecrets {
		s.log.Debug("helm secret metadata informer disabled for read-only profile")
		return
	}
	secretsGVR := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	inf := metadatainformer.NewFilteredMetadataInformer(
		s.meta, secretsGVR, metav1.NamespaceAll, getStateSubscriberResyncPeriod(),
		cache.Indexers{}, func(o *metav1.ListOptions) { o.FieldSelector = "type=" + helmSecretType },
	).Informer()
	base := s.handlers("Secret", "", "v1")
	onlyHelm := func(next func(any)) func(any) {
		return func(obj any) {
			if meta, ok := metaFromObj(obj); ok && strings.HasPrefix(meta.GetName(), helmSecretNamePrefix) {
				next(obj)
			}
		}
	}
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    onlyHelm(base.AddFunc),
		UpdateFunc: func(oldObj, newObj any) { onlyHelm(func(o any) { base.UpdateFunc(oldObj, o) })(newObj) },
		DeleteFunc: onlyHelm(base.DeleteFunc),
	})
	if err != nil {
		s.log.Warn("state subscriber: failed to register helm secret handler", "error", err)
		return
	}
	go inf.Run(stopCh)
}

// runCRDInformer loops trying to bring up a metadata informer for a
// discover-if-present CRD. The CRD may be installed after the agent starts
// (Velero/Argo/Trivy commonly arrive via the platform baseline post-boot),
// so each failed bounded sync attempt sleeps and retries instead of giving
// up. Mirrors MirrorSubscriber.runDynamicGVR's stop-channel discipline.
func (s *StateSubscriber) runCRDInformer(ctx context.Context, k metadataKind, parentStop <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-parentStop:
			return
		default:
		}

		attempt := metadatainformer.NewSharedInformerFactory(s.meta, getStateSubscriberResyncPeriod())
		inf := attempt.ForResource(k.gvr).Informer()
		_, _ = inf.AddEventHandler(s.handlers(k.kind, k.apiGroup, k.apiVersion))

		// iterDone scopes the stop-watcher goroutine to THIS iteration; the
		// watcher owns innerStop's close exclusively so a failed attempt
		// can't leak a blocked goroutine or double-close on shutdown.
		innerStop := make(chan struct{})
		iterDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
			case <-parentStop:
			case <-iterDone:
			}
			close(innerStop)
		}()
		attempt.Start(innerStop)

		syncStop := make(chan struct{})
		go func() {
			select {
			case <-innerStop:
			case <-time.After(getStateSubscriberCRDSyncTimeout()):
			}
			close(syncStop)
		}()
		ok := false
		for _, v := range attempt.WaitForCacheSync(syncStop) {
			ok = v
			break
		}
		if ok {
			s.log.Info("state subscriber: CRD informer online", "gvr", k.gvr.String(), "kind", k.kind)
			s.recordStore(k.kind, k.apiGroup, k.apiVersion, inf.GetStore())
			// Block until shutdown — the informer is running; the watcher
			// goroutine closes innerStop when ctx/parentStop fire.
			<-innerStop
			return
		}

		close(iterDone)
		s.log.Debug("state subscriber: CRD not yet available, will retry",
			"gvr", k.gvr.String(), "retry_in", getStateSubscriberCRDRetry().String())
		select {
		case <-ctx.Done():
			return
		case <-parentStop:
			return
		case <-time.After(getStateSubscriberCRDRetry()):
		}
	}
}

// runGatekeeperConstraints discovers the resources under
// constraints.gatekeeper.sh (one CRD per ConstraintTemplate — the set is
// dynamic, so it can't be pinned like crdInformerKinds) and starts a
// metadata informer per discovered resource, normalized to the stable wire
// kind "Constraint" so the dashboard has one routing row. Re-discovers on
// every retry tick to pick up templates installed after agent start.
// Constraint stores are NOT recorded for reconnect replay: entries are
// keyed by kind and multiple constraint resources share "Constraint"; the
// frontend's reconnect bulk invalidation covers the gap.
func (s *StateSubscriber) runGatekeeperConstraints(ctx context.Context, parentStop <-chan struct{}) {
	started := make(map[string]bool)
	for {
		if rl, err := s.client.Discovery().ServerResourcesForGroupVersion(gatekeeperConstraintsGV); err == nil && rl != nil {
			for _, r := range rl.APIResources {
				if started[r.Name] || strings.Contains(r.Name, "/") {
					continue // already watching, or a subresource
				}
				gvr := schema.GroupVersionResource{Group: "constraints.gatekeeper.sh", Version: "v1beta1", Resource: r.Name}
				factory := metadatainformer.NewSharedInformerFactory(s.meta, getStateSubscriberResyncPeriod())
				_, _ = factory.ForResource(gvr).Informer().AddEventHandler(s.handlers("Constraint", gvr.Group, gvr.Version))
				factory.Start(parentStop)
				started[r.Name] = true
				s.log.Info("state subscriber: gatekeeper constraint informer online", "resource", r.Name)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-parentStop:
			return
		case <-time.After(getStateSubscriberCRDRetry()):
		}
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
	s.emit(op, kind, apiGroup, apiVersion, meta, key)
}

// emit marshals + sends one StateUpdate. Split out of dispatch so the
// reconnect-replay path can re-emit cached objects WITHOUT going through the
// per-key rate limiter (a one-shot bounded resync should never be collapsed by
// the limiter that's there to absorb live event storms).
func (s *StateSubscriber) emit(op protocol.StateUpdateOp, kind, apiGroup, apiVersion string, meta metav1.Object, key string) {
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

// recordStore captures an informer's cache.Store so reconnect-replay can
// iterate it later. Idempotent; replaces any prior entry for the same kind.
func (s *StateSubscriber) recordStore(kind, apiGroup, apiVersion string, store cache.Store) {
	if store == nil {
		return
	}
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	s.stores[kind] = stateStoreEntry{kind: kind, apiGroup: apiGroup, apiVersion: apiVersion, store: store}
}

// runReconnectReplay polls the tunnel state every 2s. On every false→true
// transition (including the initial connect, since prev starts false) it
// re-emits every cached informer object as a Modified update. This makes the
// pushed-state stream resilient to mid-stream WS drops without waiting for the
// next 30m informer resync. Mirrors MirrorSubscriber.runReconnectReplay.
func (s *StateSubscriber) runReconnectReplay(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	prev := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := s.conn.IsConnected()
		if now && !prev {
			s.replayAll()
		}
		prev = now
	}
}

// replayAll re-emits every cached object across every recorded store as a
// StateUpdateOpModified (the "object currently exists / resync" op, matching
// the informer's own 30m resync which redelivers cached objects as updates).
// Bounded: it lists only the in-memory informer caches (no apiserver hit) and
// bypasses the rate limiter, so a reconnect emits at most one snapshot per
// up-edge over the same bounded send channel.
func (s *StateSubscriber) replayAll() {
	s.storeMu.RLock()
	entries := make([]stateStoreEntry, 0, len(s.stores))
	for _, e := range s.stores {
		entries = append(entries, e)
	}
	s.storeMu.RUnlock()

	total := 0
	for _, e := range entries {
		for _, obj := range e.store.List() {
			meta, ok := metaFromObj(obj)
			if !ok {
				continue
			}
			key := fmt.Sprintf("%s|%s|%s", e.kind, meta.GetNamespace(), meta.GetName())
			s.emit(protocol.StateUpdateOpModified, e.kind, e.apiGroup, e.apiVersion, meta, key)
			total++
		}
	}
	if total > 0 {
		s.log.Info("state subscriber: replayed cached objects after reconnect", "objects", total)
	}
}

// eventIsRecent decides whether a corev1/eventsv1 Event is fresh enough to
// forward. The informer replays old events as Adds during initial list /
// resync; without this filter we'd flood on every reconnect.
func (s *StateSubscriber) eventIsRecent(obj any) bool {
	cutoff := time.Now().Add(-getStateSubscriberEventCutoff())
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
