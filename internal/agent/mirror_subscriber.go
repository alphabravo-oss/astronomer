// Sprint 069 — CRD-mirror v2 agent-side subscriber.
//
// MirrorSubscriber is the agent-side counterpart to the server's
// internal/crd ingester (ingest_v2.go). It runs a SharedInformerFactory
// for the five sprint-069 mirrored GVKs and emits one
// protocol.MsgMirrorEvent per Add/Update/Delete callback.
//
// Decoupled from the existing StateSubscriber because:
//   - The wire format differs (MirrorEvent carries the full object body,
//     StateUpdate carries metadata only).
//   - The RBAC surface differs (IngressClass / GatewayClass are
//     cluster-scoped; NetworkPolicy / ResourceQuota / LimitRange are
//     all namespaces). The agent ServiceAccount needs distinct
//     RoleBindings for the five GVKs.
//   - GatewayClass watches need a dynamic client (gateway-api types are
//     not on the typed clientset), whereas the other four use the typed
//     informers.
//
// Failure mode: any single GVK whose RBAC is missing fails to sync and
// is logged once; the process keeps running with whatever GVKs did
// sync. The server-side prune sweep handles the resulting "missing
// rows" case by treating it as "this cluster doesn't have any" — the
// UI just renders an empty tab.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/internal/crd"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// gatewayClassGVR is the GroupVersionResource for the v1 GatewayClass
// CRD shipped by sigs.k8s.io/gateway-api. We use a string-shaped
// constant rather than importing the typed package so the agent builds
// in clusters where gateway-api is not installed (a missing CRD is
// indistinguishable from RBAC denial at watch time — both log + skip).
var gatewayClassGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gatewayclasses",
}

// vulnerabilityReportGVR is the upstream trivy-operator CRD shipped by
// aquasecurity.github.io. Same shape rationale as gatewayClassGVR: we
// string-pin the GVR so the agent builds and runs in clusters where
// trivy-operator is not (yet) installed — the dynamic informer logs +
// skips when the CRD is missing instead of crashing the subscriber.
var vulnerabilityReportGVR = schema.GroupVersionResource{
	Group:    "aquasecurity.github.io",
	Version:  "v1alpha1",
	Resource: "vulnerabilityreports",
}

// mirrorResyncPeriod is how often the SharedInformerFactory replays
// every object as an Add. Resyncs serve two purposes:
//  1. Drift detection — anything the agent missed gets re-emitted.
//  2. last_seen_at refresh — the server-side prune (1h cutoff) only
//     drops rows that haven't been touched in an hour; the resync
//     keeps living rows fresh.
//
// 10 minutes balances those against the server-side ingest pressure.
const mirrorResyncPeriod = 10 * time.Minute

// MirrorSender is the narrow tunnel-send interface the subscriber
// needs. Matches the shape used by StateSubscriber so the agent's
// existing tunnel client can satisfy both.
type MirrorSender interface {
	Send(msg *protocol.Message) error
}

// MirrorConnectionWatcher is the narrow tunnel-state surface the
// reconnect-replay goroutine polls. The agent's TunnelClient satisfies
// it via IsConnected.
type MirrorConnectionWatcher interface {
	IsConnected() bool
}

// MirrorSubscriber owns the five informers and forwards events as
// MsgMirrorEvent messages over the tunnel.
type MirrorSubscriber struct {
	client    kubernetes.Interface
	dyn       dynamic.Interface
	sender    MirrorSender
	log       *slog.Logger
	ready     atomic.Bool
	readyCh   chan struct{}
	once      sync.Once
	startedAt time.Time

	// stores holds the live informer caches so the reconnect-replay
	// goroutine can iterate them on every tunnel reconnect. Keyed by
	// kind (crd.KindIngressClass / KindVulnerabilityReport / …).
	// Populated as each informer's cache syncs.
	storeMu sync.RWMutex
	stores  map[string]informerStoreEntry

	// conn is the connection watcher whose IsConnected reading we
	// poll for reconnect detection. nil-safe: when unwired (tests /
	// older callers), we skip the replay goroutine.
	conn MirrorConnectionWatcher
}

// informerStoreEntry pairs an informer store with the kind label that
// dispatch uses. Lets replayAll fan out to dispatchUnstructured /
// dispatchTyped without re-deriving the kind per item.
type informerStoreEntry struct {
	kind    string
	store   cache.Store
	dynamic bool // true → items are *unstructured.Unstructured already
}

// NewMirrorSubscriber constructs a MirrorSubscriber. dyn is the dynamic
// client used for the GatewayClass watch; pass nil to skip that GVK
// (e.g. when gateway-api isn't installed in the cluster). The four
// typed informers run regardless.
func NewMirrorSubscriber(client kubernetes.Interface, dyn dynamic.Interface, sender MirrorSender, log *slog.Logger) *MirrorSubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &MirrorSubscriber{
		client:    client,
		dyn:       dyn,
		sender:    sender,
		log:       log,
		readyCh:   make(chan struct{}),
		startedAt: time.Now(),
		stores:    make(map[string]informerStoreEntry),
	}
}

// SetConnectionWatcher wires the tunnel connection watcher so the
// reconnect-replay loop can detect false→true transitions and re-emit
// every cached mirror item to the (newly-reconnected) management
// plane. Optional; nil = no replay goroutine.
func (s *MirrorSubscriber) SetConnectionWatcher(c MirrorConnectionWatcher) {
	if s == nil {
		return
	}
	s.conn = c
}

// WaitReady blocks until the informer caches have synced, or until ctx
// is cancelled. Returns true on sync, false on cancel.
func (s *MirrorSubscriber) WaitReady(ctx context.Context) bool {
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

// Run blocks until ctx is cancelled. Sets up the typed informer set
// (always present in any modern k8s), launches a per-GVR retry loop
// for each dynamic CRD (gateway-api, trivy-operator) that retries on
// missing-CRD until success, and starts the reconnect-replay loop.
func (s *MirrorSubscriber) Run(ctx context.Context) {
	if s.client == nil {
		s.log.Warn("mirror subscriber: nil clientset, skipping")
		return
	}

	factory := informers.NewSharedInformerFactory(s.client, mirrorResyncPeriod)

	// IngressClass — networking.k8s.io/v1, cluster-scoped.
	ic := factory.Networking().V1().IngressClasses().Informer()
	s.attachIngressClass(ic)
	s.recordStore(crd.KindIngressClass, ic.GetStore(), false)

	// NetworkPolicy — networking.k8s.io/v1, namespace-scoped (all namespaces).
	np := factory.Networking().V1().NetworkPolicies().Informer()
	s.attachNetworkPolicy(np)
	s.recordStore(crd.KindNetworkPolicy, np.GetStore(), false)

	// ResourceQuota — core/v1.
	rq := factory.Core().V1().ResourceQuotas().Informer()
	s.attachResourceQuota(rq)
	s.recordStore(crd.KindResourceQuota, rq.GetStore(), false)

	// LimitRange — core/v1.
	lr := factory.Core().V1().LimitRanges().Informer()
	s.attachLimitRange(lr)
	s.recordStore(crd.KindLimitRange, lr.GetStore(), false)

	stopCh := make(chan struct{})
	defer close(stopCh)
	factory.Start(stopCh)

	// Dynamic informers run in their own goroutines so a missing CRD
	// for one GVR (e.g. gateway-api uninstalled) doesn't strand the
	// other. Each goroutine retries every 60s until the CRD is
	// installed, then attaches the informer and blocks until ctx done.
	if s.dyn != nil {
		go s.runDynamicGVR(ctx, gatewayClassGVR, crd.KindGatewayClass, stopCh, attachKind{
			add: func(obj any) { s.dispatchUnstructured(protocol.MirrorOpAdded, crd.KindGatewayClass, obj) },
			upd: func(_, n any) { s.dispatchUnstructured(protocol.MirrorOpModified, crd.KindGatewayClass, n) },
			del: func(obj any) { s.dispatchDeleteUnstructured(crd.KindGatewayClass, obj) },
		})
		go s.runDynamicGVR(ctx, vulnerabilityReportGVR, crd.KindVulnerabilityReport, stopCh, attachKind{
			add: func(obj any) { s.dispatchUnstructured(protocol.MirrorOpAdded, crd.KindVulnerabilityReport, obj) },
			upd: func(_, n any) { s.dispatchUnstructured(protocol.MirrorOpModified, crd.KindVulnerabilityReport, n) },
			del: func(obj any) { s.dispatchDeleteUnstructured(crd.KindVulnerabilityReport, obj) },
		})
	}

	// Bounded wait for the typed factory only. The dynamic GVRs are
	// off in their own loops and may take minutes to settle (operator
	// hasn't installed trivy yet); we don't gate "subscriber ready" on
	// them or events from the typed kinds would be delayed unfairly.
	syncStop := make(chan struct{})
	go func() {
		select {
		case <-stopCh:
		case <-time.After(30 * time.Second):
		}
		close(syncStop)
	}()
	synced := factory.WaitForCacheSync(syncStop)
	for typ, ok := range synced {
		if !ok {
			s.log.Warn("mirror subscriber: typed cache failed to sync (RBAC missing?)", "type", fmt.Sprintf("%T", typ))
		}
	}

	s.ready.Store(true)
	s.once.Do(func() { close(s.readyCh) })
	s.log.Info("mirror subscriber started", "resync", mirrorResyncPeriod)

	// Reconnect replay: on every tunnel false→true transition,
	// re-emit every cached item so the management plane doesn't lose
	// rows when the WS drops mid-bootstrap. Optional; only runs when
	// the caller wired a connection watcher.
	if s.conn != nil {
		go s.runReconnectReplay(ctx)
	}

	<-ctx.Done()
}

// attachKind groups the three informer callbacks for a kind so the
// per-GVR loop can register them once after the CRD becomes available
// (or replace them if a future iteration of the retry rebuilds the
// informer). Defined as a struct so adding a new kind doesn't grow
// the signature of runDynamicGVR.
type attachKind struct {
	add func(obj any)
	upd func(oldObj, newObj any)
	del func(obj any)
}

// runDynamicGVR loops trying to bring up a dynamic informer for the
// given GVR. Returns only when ctx is cancelled.
//
// The CRD may be installed on the cluster AFTER the agent starts (the
// platform-baseline auto-attach installs trivy-operator post-agent-
// boot in the common case). client-go's SharedInformerFactory has no
// built-in retry for that — a missed initial list fails forever — so
// this function owns the retry loop: build a tiny single-GVR factory,
// wait briefly for sync, on success block until ctx, on failure sleep
// and try again.
func (s *MirrorSubscriber) runDynamicGVR(ctx context.Context, gvr schema.GroupVersionResource, kind string, parentStop <-chan struct{}, hooks attachKind) {
	backoff := 30 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-parentStop:
			return
		default:
		}

		attempt := dynamicinformer.NewDynamicSharedInformerFactory(s.dyn, mirrorResyncPeriod)
		inf := attempt.ForResource(gvr).Informer()
		_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    hooks.add,
			UpdateFunc: hooks.upd,
			DeleteFunc: hooks.del,
		})

		innerStop := make(chan struct{})
		// iterDone scopes the stop-watcher goroutine to THIS iteration. The
		// watcher owns innerStop's close exclusively — it fires on ctx /
		// parentStop (real shutdown) OR on iterDone (this attempt failed and
		// the loop is about to retry). This is the ONLY place innerStop is
		// closed, so a failed attempt can't leak a blocked watcher goroutine
		// (previously one per ~30s retry for an absent CRD) and shutdown can't
		// double-close innerStop (previously `panic: close of closed channel`
		// when parentStop woke every leaked watcher after line 341 had already
		// closed that iteration's innerStop).
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

		// 20s bounded wait per attempt — long enough for a healthy
		// apiserver, short enough that an unbacked GVR retries
		// promptly. We don't share the 30s typed-sync timeout because
		// that one already elapsed by the time we get here.
		syncStop := make(chan struct{})
		go func() {
			select {
			case <-innerStop:
			case <-time.After(20 * time.Second):
			}
			close(syncStop)
		}()
		syncMap := attempt.WaitForCacheSync(syncStop)
		ok := false
		for _, v := range syncMap {
			ok = v
			break
		}
		if ok {
			s.log.Info("mirror subscriber: dynamic GVR online", "gvr", gvr.String(), "kind", kind)
			s.recordStore(kind, inf.GetStore(), true)
			// Block until shutdown — the informer is now running. The watcher
			// goroutine closes innerStop when ctx/parentStop fire; we never
			// close iterDone on the success path so it keeps running as the
			// long-lived informer's stop signal.
			<-innerStop
			return
		}

		// Attempt failed: end this iteration. Closing iterDone makes the
		// watcher goroutine close innerStop exactly once and exit — no leaked
		// goroutine, no double-close of innerStop.
		close(iterDone)
		s.log.Info("mirror subscriber: dynamic GVR not yet available, will retry",
			"gvr", gvr.String(), "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-parentStop:
			return
		case <-time.After(backoff):
		}
	}
}

// recordStore captures an informer's cache.Store so reconnect-replay
// can iterate it later. Idempotent; replaces any prior entry for the
// same kind so a CRD-retry-rebuild swaps the store cleanly.
func (s *MirrorSubscriber) recordStore(kind string, store cache.Store, dynamic bool) {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	s.stores[kind] = informerStoreEntry{kind: kind, store: store, dynamic: dynamic}
}

// runReconnectReplay polls the tunnel state every 2s. On every
// false→true transition (including the initial connect since we start
// at "previous=false"), it walks every recorded informer store and
// dispatches Add for every cached item. This makes the agent
// idempotent across mid-bootstrap WS drops — the management plane
// re-receives the full mirror inventory after each reconnect rather
// than waiting for the next 10-minute resync.
func (s *MirrorSubscriber) runReconnectReplay(ctx context.Context) {
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

// replayAll dispatches an Add for every cached item across every
// recorded store. Cheap: each Add hits dispatchTyped or
// dispatchUnstructured which marshals + queues onto the bounded send
// channel; backpressure is the same as a regular informer Add storm.
func (s *MirrorSubscriber) replayAll() {
	s.storeMu.RLock()
	entries := make([]informerStoreEntry, 0, len(s.stores))
	for _, e := range s.stores {
		entries = append(entries, e)
	}
	s.storeMu.RUnlock()

	total := 0
	for _, e := range entries {
		items := e.store.List()
		for _, obj := range items {
			if e.dynamic {
				s.dispatchUnstructured(protocol.MirrorOpAdded, e.kind, obj)
			} else {
				s.dispatchTyped(protocol.MirrorOpAdded, e.kind, obj)
			}
		}
		total += len(items)
	}
	if total > 0 {
		s.log.Info("mirror subscriber: replayed cached items after reconnect", "items", total)
	}
}

// ---------------------------------------------------------------------
// Per-GVK handler attachment
// ---------------------------------------------------------------------

func (s *MirrorSubscriber) attachIngressClass(inf cache.SharedIndexInformer) {
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s.dispatchTyped(protocol.MirrorOpAdded, crd.KindIngressClass, obj)
		},
		UpdateFunc: func(_, newObj any) {
			s.dispatchTyped(protocol.MirrorOpModified, crd.KindIngressClass, newObj)
		},
		DeleteFunc: func(obj any) {
			s.dispatchDelete(crd.KindIngressClass, obj)
		},
	})
}

func (s *MirrorSubscriber) attachNetworkPolicy(inf cache.SharedIndexInformer) {
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s.dispatchTyped(protocol.MirrorOpAdded, crd.KindNetworkPolicy, obj)
		},
		UpdateFunc: func(_, newObj any) {
			s.dispatchTyped(protocol.MirrorOpModified, crd.KindNetworkPolicy, newObj)
		},
		DeleteFunc: func(obj any) {
			s.dispatchDelete(crd.KindNetworkPolicy, obj)
		},
	})
}

func (s *MirrorSubscriber) attachResourceQuota(inf cache.SharedIndexInformer) {
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s.dispatchTyped(protocol.MirrorOpAdded, crd.KindResourceQuota, obj)
		},
		UpdateFunc: func(_, newObj any) {
			s.dispatchTyped(protocol.MirrorOpModified, crd.KindResourceQuota, newObj)
		},
		DeleteFunc: func(obj any) {
			s.dispatchDelete(crd.KindResourceQuota, obj)
		},
	})
}

func (s *MirrorSubscriber) attachLimitRange(inf cache.SharedIndexInformer) {
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s.dispatchTyped(protocol.MirrorOpAdded, crd.KindLimitRange, obj)
		},
		UpdateFunc: func(_, newObj any) {
			s.dispatchTyped(protocol.MirrorOpModified, crd.KindLimitRange, newObj)
		},
		DeleteFunc: func(obj any) {
			s.dispatchDelete(crd.KindLimitRange, obj)
		},
	})
}

// attachGatewayClass / attachVulnerabilityReport are gone: dynamic
// GVR handlers are now attached inline in runDynamicGVR (see Run)
// because each retry-rebuild needs to bind a fresh informer instance.

// ---------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------

// dispatchTyped converts a typed informer object to unstructured, marshals
// it, and queues a MirrorEvent. We never block on the send channel; on
// overflow we drop and let the next resync catch up.
//
// Bootstrap behavior: the previous gate here dropped all events before
// ready=true to avoid a "thundering reset on every reconnect", but
// reconnects don't restart this goroutine — only an agent process
// restart does. Suppressing bootstrap adds meant that a stable cluster
// (no churn) never sent any mirror events to the server, leaving the
// image_vulnerability_reports table permanently empty even when trivy
// was producing reports. We now emit on bootstrap too.
func (s *MirrorSubscriber) dispatchTyped(op protocol.MirrorEventOp, kind string, obj any) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		s.log.Warn("mirror subscriber: convert to unstructured failed", "kind", kind, "error", err)
		return
	}
	u := &unstructured.Unstructured{Object: m}
	s.sendEvent(op, kind, u)
}

// dispatchUnstructured is the variant for the dynamic-informer GatewayClass
// + VulnerabilityReport paths — the informer already hands back
// *unstructured.Unstructured. Bootstrap suppression dropped — see
// dispatchTyped for the same rationale.
func (s *MirrorSubscriber) dispatchUnstructured(op protocol.MirrorEventOp, kind string, obj any) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		s.log.Warn("mirror subscriber: dynamic informer returned non-unstructured", "kind", kind, "type", fmt.Sprintf("%T", obj))
		return
	}
	s.sendEvent(op, kind, u)
}

// sendEvent is the marshal+send tail. Both typed/unstructured paths
// funnel through here so the wire-format logic lives in one spot.
func (s *MirrorSubscriber) sendEvent(op protocol.MirrorEventOp, kind string, u *unstructured.Unstructured) {
	if u == nil {
		return
	}
	body, err := json.Marshal(u.Object)
	if err != nil {
		s.log.Warn("mirror subscriber: marshal object failed", "kind", kind, "error", err)
		return
	}
	payload := protocol.MirrorEventPayload{
		Op:        op,
		Kind:      kind,
		Namespace: u.GetNamespace(),
		Name:      u.GetName(),
		Object:    body,
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		s.log.Warn("mirror subscriber: marshal payload failed", "kind", kind, "error", err)
		return
	}
	msg := &protocol.Message{
		Type:      protocol.MsgMirrorEvent,
		Timestamp: time.Now().UTC(),
		Payload:   pb,
	}
	if err := s.sender.Send(msg); err != nil {
		s.log.Warn("mirror subscriber: send failed", "kind", kind, "error", err)
	}
}

// dispatchDelete handles the DeleteFunc callback path for typed
// informers. cache.DeletedFinalStateUnknown is a "tombstone" that
// wraps the last-known object — we unwrap before reading meta.
func (s *MirrorSubscriber) dispatchDelete(kind string, obj any) {
	if !s.ready.Load() {
		return
	}
	if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tomb.Obj
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		s.log.Warn("mirror subscriber: convert delete to unstructured failed", "kind", kind, "error", err)
		return
	}
	u := &unstructured.Unstructured{Object: m}
	payload := protocol.MirrorEventPayload{
		Op:        protocol.MirrorOpDeleted,
		Kind:      kind,
		Namespace: u.GetNamespace(),
		Name:      u.GetName(),
		// Object intentionally empty on delete — the server only needs
		// the natural key to route to the right DELETE statement.
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		s.log.Warn("mirror subscriber: marshal delete payload failed", "kind", kind, "error", err)
		return
	}
	msg := &protocol.Message{
		Type:      protocol.MsgMirrorEvent,
		Timestamp: time.Now().UTC(),
		Payload:   pb,
	}
	if err := s.sender.Send(msg); err != nil {
		s.log.Warn("mirror subscriber: send delete failed", "kind", kind, "error", err)
	}
}

// dispatchDeleteUnstructured is the dynamic-informer variant of
// dispatchDelete. cache.DeletedFinalStateUnknown still applies — the
// dynamic informer cache uses the same tombstone shape.
func (s *MirrorSubscriber) dispatchDeleteUnstructured(kind string, obj any) {
	if !s.ready.Load() {
		return
	}
	if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tomb.Obj
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		s.log.Warn("mirror subscriber: dynamic informer delete returned non-unstructured", "kind", kind, "type", fmt.Sprintf("%T", obj))
		return
	}
	payload := protocol.MirrorEventPayload{
		Op:        protocol.MirrorOpDeleted,
		Kind:      kind,
		Namespace: u.GetNamespace(),
		Name:      u.GetName(),
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		s.log.Warn("mirror subscriber: marshal delete payload failed", "kind", kind, "error", err)
		return
	}
	msg := &protocol.Message{
		Type:      protocol.MsgMirrorEvent,
		Timestamp: time.Now().UTC(),
		Payload:   pb,
	}
	if err := s.sender.Send(msg); err != nil {
		s.log.Warn("mirror subscriber: send delete failed", "kind", kind, "error", err)
	}
}

// Sanity assertion — keep at least one named typed-package reference so
// `go vet` doesn't flag networkingv1 / corev1 as unused on future
// refactors that move the type imports somewhere else.
var (
	_ = networkingv1.NetworkPolicy{}
	_ = corev1.ResourceQuota{}
)
