package delivery

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const maxObservedAssignments = protocol.MaxDeliveryAssignments

// AssignmentObjectObservation distinguishes a synchronized missing object from
// an unreadable source. Noncurrent objects must never be normalized as healthy.
type AssignmentObjectObservation struct {
	Object      *unstructured.Unstructured
	Observation protocol.DeliveryObservation
}

type AssignmentObservation struct {
	Assignment         AcceptedAssignment
	Source, Reconciler AssignmentObjectObservation
}

// AssignmentObservationSource is deliberately independent of mutation execution.
// Consumers batch once per status pass, preserve checkpoint fences, and use Wake
// for status work only. Legacy direct GET selection belongs at composition.
type AssignmentObservationSource interface {
	ReplaceAssignments([]AcceptedAssignment) error
	SnapshotAssignments() map[string]AssignmentObservation
	Wake() <-chan struct{}
	ConsumeDirty() []string
}

type AssignmentCacheOptions struct {
	Observation observation.Options
	// MaxAssignments bounds retained reverse-index and pending work. Zero uses
	// the fixed maximum; callers may choose a smaller limit, never a larger one.
	MaxAssignments int
}

type assignmentObjectKey struct {
	GVR             schema.GroupVersionResource
	Namespace, Name string
}
type assignmentRefs struct {
	assignment         AcceptedAssignment
	source, reconciler assignmentObjectKey
}
type assignmentKindStore struct {
	tracker  *observation.Tracker
	informer cache.SharedIndexInformer
}

// AssignmentCache shares five ownership-filtered Flux informers for all projects.
// It uses the caller's existing credentials, with no RBAC expansion or fallback.
// Run belongs to the agent lifecycle, not to a management tunnel connection.
type AssignmentCache struct {
	mu          sync.Mutex
	started     bool
	limit       int
	assignments map[string]assignmentRefs
	reverse     map[assignmentObjectKey]map[string]struct{}
	dirty       map[string]struct{}
	wake        chan struct{}
	kinds       map[schema.GroupVersionResource]assignmentKindStore
}

var assignmentGVKs = []schema.GroupVersionKind{gitRepositoryGVK, ociRepositoryGVK, helmRepositoryGVK, kustomizationGVK, helmReleaseGVK}

type assignmentListWatch struct{ *cache.ListWatch }

func (*assignmentListWatch) IsWatchListSemanticsUnSupported() bool { return true }

func NewAssignmentCache(client dynamic.Interface, opts AssignmentCacheOptions) (*AssignmentCache, error) {
	if client == nil {
		return nil, errors.New("assignment observation dynamic client is required")
	}
	if opts.MaxAssignments == 0 {
		opts.MaxAssignments = maxObservedAssignments
	}
	if opts.MaxAssignments < 1 || opts.MaxAssignments > maxObservedAssignments {
		return nil, errors.New("invalid assignment observation limit")
	}
	c := &AssignmentCache{limit: opts.MaxAssignments, assignments: map[string]assignmentRefs{}, reverse: map[assignmentObjectKey]map[string]struct{}{}, dirty: map[string]struct{}{}, wake: make(chan struct{}, 1), kinds: map[schema.GroupVersionResource]assignmentKindStore{}}
	for _, gvk := range assignmentGVKs {
		gvr := deliveryResources[gvk].resource
		resource := client.Resource(gvr).Namespace(metav1.NamespaceAll)
		options := opts.Observation
		recordRequest, recordEvent := options.RecordRequest, options.RecordWatchEvent
		options.RecordRequest = func(kind observation.Kind, verb, outcome string) {
			if recordRequest != nil {
				recordRequest(kind, verb, outcome)
			}
			c.markKindDirty(gvr)
		}
		options.RecordWatchEvent = func(kind observation.Kind, outcome string) {
			if recordEvent != nil {
				recordEvent(kind, outcome)
			}
			c.markKindDirty(gvr)
		}
		tracker := observation.NewTracker(observation.Kind(gvk.Kind), options)
		lw := &assignmentListWatch{&cache.ListWatch{
			ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
				o.LabelSelector = ManagedByLabel + "=" + ManagedByValue
				return tracker.List(ctx, o, func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) { return resource.List(ctx, o) })
			},
			WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
				o.LabelSelector = ManagedByLabel + "=" + ManagedByValue
				return tracker.Watch(ctx, o, resource.Watch)
			},
		}}
		informer := cache.NewSharedIndexInformer(lw, &unstructured.Unstructured{}, 0, cache.Indexers{})
		if err := informer.SetTransform(projectAssignmentObject); err != nil {
			return nil, err
		}
		changed := func(raw any) {
			if tombstone, ok := raw.(cache.DeletedFinalStateUnknown); ok {
				raw = tombstone.Obj
			}
			if object, ok := raw.(*unstructured.Unstructured); ok {
				c.markObjectDirty(assignmentObjectKey{gvr, object.GetNamespace(), object.GetName()})
			}
		}
		if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: func(raw any) { changed(raw) }, UpdateFunc: func(_, raw any) { changed(raw) }, DeleteFunc: changed}); err != nil {
			return nil, err
		}
		tracker.Bind(informer.GetStore(), informer.HasSynced)
		c.kinds[gvr] = assignmentKindStore{tracker, informer}
	}
	return c, nil
}

// Run is single-use. Cancellation joins all fixed informers and fences retained
// snapshots. Informer retries handle absent CRDs, revoked permissions and 410s.
func (c *AssignmentCache) Run(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return errors.New("assignment cache already started")
	}
	c.started = true
	c.mu.Unlock()
	var workers sync.WaitGroup
	if ctx.Err() == nil {
		for _, store := range c.kinds {
			workers.Add(1)
			go func() { defer workers.Done(); store.informer.RunWithContext(ctx) }()
		}
	}
	workers.Wait()
	for gvr, store := range c.kinds {
		store.tracker.Disconnect()
		c.markKindDirty(gvr)
	}
	return ctx.Err()
}

func (c *AssignmentCache) Wake() <-chan struct{} { return c.wake }
func (c *AssignmentCache) signalLocked() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
func (c *AssignmentCache) markObjectDirty(key assignmentObjectKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.reverse[key] {
		c.dirty[id] = struct{}{}
	}
	if len(c.dirty) > 0 {
		c.signalLocked()
	}
}
func (c *AssignmentCache) markKindDirty(gvr schema.GroupVersionResource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, ids := range c.reverse {
		if key.GVR == gvr {
			for id := range ids {
				c.dirty[id] = struct{}{}
			}
		}
	}
	if len(c.dirty) > 0 {
		c.signalLocked()
	}
}
func (c *AssignmentCache) ConsumeDirty() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, 0, len(c.dirty))
	for id := range c.dirty {
		ids = append(ids, id)
	}
	clear(c.dirty)
	select {
	case <-c.wake:
	default:
	}
	sort.Strings(ids)
	return ids
}

// ReplaceAssignments atomically replaces subscriptions, including dirty/index
// eviction for removed assignments. Rejected sets leave the old set untouched.
func (c *AssignmentCache) ReplaceAssignments(assignments []AcceptedAssignment) error {
	if len(assignments) > c.limit {
		return errors.New("assignment observation limit exceeded")
	}
	next := make(map[string]assignmentRefs, len(assignments))
	reverse := make(map[assignmentObjectKey]map[string]struct{}, 2*len(assignments))
	for _, accepted := range assignments {
		refs, err := assignmentObservationRefs(accepted)
		if err != nil {
			return err
		}
		id := accepted.DeploymentID
		if _, exists := next[id]; exists {
			return fmt.Errorf("duplicate accepted assignment")
		}
		next[id] = refs
		for _, key := range []assignmentObjectKey{refs.source, refs.reconciler} {
			if reverse[key] == nil {
				reverse[key] = map[string]struct{}{}
			}
			reverse[key][id] = struct{}{}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.dirty {
		if _, exists := next[id]; !exists {
			delete(c.dirty, id)
		}
	}
	for id, refs := range next {
		if old, exists := c.assignments[id]; !exists || !reflect.DeepEqual(old, refs) {
			c.dirty[id] = struct{}{}
		}
	}
	c.assignments = next
	c.reverse = reverse
	select {
	case <-c.wake:
	default:
	}
	if len(c.dirty) > 0 {
		c.signalLocked()
	}
	return nil
}

func assignmentObservationRefs(accepted AcceptedAssignment) (assignmentRefs, error) {
	if err := validateAcceptedAssignment(accepted); err != nil {
		return assignmentRefs{}, err
	}
	names := Names(accepted.ProjectID, accepted.DeploymentID)
	if accepted.ControlNamespace != names.ControlNamespace {
		return assignmentRefs{}, errors.New("assignment control namespace mismatch")
	}
	refs := assignmentRefs{assignment: accepted}
	refs.assignment.Objects = append([]ObjectIdentity(nil), accepted.Objects...)
	for _, identity := range accepted.Objects {
		source := identity.Kind == sourceKind(accepted.SourceKind)
		reconciler := identity.Kind == reconcilerKind(accepted.RendererKind)
		if !source && !reconciler {
			continue
		}
		gvr, _, err := resourceForIdentity(identity)
		if err != nil {
			return assignmentRefs{}, err
		}
		if identity.Namespace != names.ControlNamespace {
			return assignmentRefs{}, errors.New("assignment observation namespace mismatch")
		}
		key := assignmentObjectKey{gvr, identity.Namespace, identity.Name}
		if source {
			if identity.Name != names.Source || refs.source.Name != "" {
				return assignmentRefs{}, errors.New("invalid source observation identity")
			}
			refs.source = key
		}
		if reconciler {
			if identity.Name != names.Base || refs.reconciler.Name != "" {
				return assignmentRefs{}, errors.New("invalid reconciler observation identity")
			}
			refs.reconciler = key
		}
	}
	if refs.source.Name == "" || refs.reconciler.Name == "" {
		return assignmentRefs{}, errors.New("accepted assignment lacks Flux observation identities")
	}
	return refs, nil
}

// SnapshotAssignments does five store reads per batch, never one scan per
// assignment. Copies isolate callers from stores and subscription metadata.
func (c *AssignmentCache) SnapshotAssignments() map[string]AssignmentObservation {
	c.mu.Lock()
	assignments := make(map[string]assignmentRefs, len(c.assignments))
	for id, refs := range c.assignments {
		assignments[id] = refs
	}
	c.mu.Unlock()
	snapshots := make(map[schema.GroupVersionResource]observation.Snapshot, len(c.kinds))
	objects := map[assignmentObjectKey]*unstructured.Unstructured{}
	for gvr, kind := range c.kinds {
		snapshot := kind.tracker.Snapshot()
		snapshots[gvr] = snapshot
		for _, object := range snapshot.Objects {
			objects[assignmentObjectKey{gvr, object.GetNamespace(), object.GetName()}] = object
		}
	}
	lookup := func(key assignmentObjectKey) AssignmentObjectObservation {
		state := snapshots[key.GVR].Observation
		if state.ObservedAt != nil {
			at := *state.ObservedAt
			state.ObservedAt = &at
		}
		object := objects[key]
		if state.State == protocol.ObservationCurrent && object == nil {
			state.State = protocol.ObservationAbsent
		}
		if object != nil {
			object = object.DeepCopy()
		}
		return AssignmentObjectObservation{object, state}
	}
	result := make(map[string]AssignmentObservation, len(assignments))
	for id, refs := range assignments {
		accepted := refs.assignment
		accepted.Objects = append([]ObjectIdentity(nil), accepted.Objects...)
		result[id] = AssignmentObservation{accepted, lookup(refs.source), lookup(refs.reconciler)}
	}
	return result
}
