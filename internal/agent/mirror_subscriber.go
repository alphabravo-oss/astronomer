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

// mirrorResyncPeriod is how often the SharedInformerFactory replays
// every object as an Add. Resyncs serve two purposes:
//   1. Drift detection — anything the agent missed gets re-emitted.
//   2. last_seen_at refresh — the server-side prune (1h cutoff) only
//      drops rows that haven't been touched in an hour; the resync
//      keeps living rows fresh.
// 10 minutes balances those against the server-side ingest pressure.
const mirrorResyncPeriod = 10 * time.Minute

// MirrorSender is the narrow tunnel-send interface the subscriber
// needs. Matches the shape used by StateSubscriber so the agent's
// existing tunnel client can satisfy both.
type MirrorSender interface {
	Send(msg *protocol.Message) error
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
	}
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

// Run blocks until ctx is cancelled. Sets up the five informers, waits
// for initial cache sync, then returns once ctx fires.
func (s *MirrorSubscriber) Run(ctx context.Context) {
	if s.client == nil {
		s.log.Warn("mirror subscriber: nil clientset, skipping")
		return
	}

	factory := informers.NewSharedInformerFactory(s.client, mirrorResyncPeriod)

	// IngressClass — networking.k8s.io/v1, cluster-scoped.
	ic := factory.Networking().V1().IngressClasses().Informer()
	s.attachIngressClass(ic)

	// NetworkPolicy — networking.k8s.io/v1, namespace-scoped (all namespaces).
	np := factory.Networking().V1().NetworkPolicies().Informer()
	s.attachNetworkPolicy(np)

	// ResourceQuota — core/v1.
	rq := factory.Core().V1().ResourceQuotas().Informer()
	s.attachResourceQuota(rq)

	// LimitRange — core/v1.
	lr := factory.Core().V1().LimitRanges().Informer()
	s.attachLimitRange(lr)

	stopCh := make(chan struct{})
	defer close(stopCh)
	factory.Start(stopCh)

	// GatewayClass via dynamic factory (gateway-api may not be installed;
	// failure to sync is treated as "no GatewayClasses in this cluster").
	var dynFactory dynamicinformer.DynamicSharedInformerFactory
	if s.dyn != nil {
		dynFactory = dynamicinformer.NewDynamicSharedInformerFactory(s.dyn, mirrorResyncPeriod)
		gc := dynFactory.ForResource(gatewayClassGVR).Informer()
		s.attachGatewayClass(gc)
		dynFactory.Start(stopCh)
	}

	synced := factory.WaitForCacheSync(stopCh)
	for typ, ok := range synced {
		if !ok {
			s.log.Warn("mirror subscriber: typed cache failed to sync (RBAC or CRD missing?)", "type", fmt.Sprintf("%T", typ))
		}
	}
	if dynFactory != nil {
		dynSynced := dynFactory.WaitForCacheSync(stopCh)
		for gvr, ok := range dynSynced {
			if !ok {
				s.log.Warn("mirror subscriber: dynamic cache failed to sync", "gvr", gvr.String())
			}
		}
	}

	s.ready.Store(true)
	s.once.Do(func() { close(s.readyCh) })
	s.log.Info("mirror subscriber started", "resync", mirrorResyncPeriod)

	<-ctx.Done()
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

func (s *MirrorSubscriber) attachGatewayClass(inf cache.SharedIndexInformer) {
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s.dispatchUnstructured(protocol.MirrorOpAdded, crd.KindGatewayClass, obj)
		},
		UpdateFunc: func(_, newObj any) {
			s.dispatchUnstructured(protocol.MirrorOpModified, crd.KindGatewayClass, newObj)
		},
		DeleteFunc: func(obj any) {
			s.dispatchDeleteUnstructured(crd.KindGatewayClass, obj)
		},
	})
}

// ---------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------

// dispatchTyped converts a typed informer object to unstructured, marshals
// it, and queues a MirrorEvent. We never block on the send channel; on
// overflow we drop and let the next resync catch up.
func (s *MirrorSubscriber) dispatchTyped(op protocol.MirrorEventOp, kind string, obj any) {
	if !s.ready.Load() {
		// Suppress bootstrap adds; the WaitReady gate prevents a thundering
		// reset on every reconnect.
		return
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		s.log.Warn("mirror subscriber: convert to unstructured failed", "kind", kind, "error", err)
		return
	}
	u := &unstructured.Unstructured{Object: m}
	s.sendEvent(op, kind, u)
}

// dispatchUnstructured is the variant for the dynamic-informer GatewayClass
// path — the informer already hands back *unstructured.Unstructured.
func (s *MirrorSubscriber) dispatchUnstructured(op protocol.MirrorEventOp, kind string, obj any) {
	if !s.ready.Load() {
		return
	}
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
