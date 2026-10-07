package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type assignmentCacheFixture struct {
	client      *fake.FakeDynamicClient
	cache       *AssignmentCache
	assignments []AcceptedAssignment
	objects     []*unstructured.Unstructured
	clock       atomic.Int64
	mu          sync.Mutex
	watches     map[schema.GroupVersionResource]*watch.RaceFreeFakeWatcher
	cancel      context.CancelFunc
	done        chan error
}

func newAssignmentCacheFixture(t *testing.T, n int) *assignmentCacheFixture {
	t.Helper()
	f := &assignmentCacheFixture{watches: map[schema.GroupVersionResource]*watch.RaceFreeFakeWatcher{}}
	f.clock.Store(time.Now().UnixNano())
	objects := []runtime.Object{}
	for i := 0; i < n; i++ {
		assignment := gitAssignment()
		assignment.DeploymentID = fmt.Sprintf("11111111-1111-4111-8111-%012d", i)
		source, reconciler := observedObjects(assignment)
		source.SetResourceVersion("1")
		reconciler.SetResourceVersion("1")
		source.Object["spec"] = map[string]any{"url": "https://credential@example.test", "secretRef": map[string]any{"name": "do-not-retain"}}
		source.Object["status"].(map[string]any)["artifact"].(map[string]any)["url"] = "https://sensitive.example.test"
		f.objects = append(f.objects, source, reconciler)
		objects = append(objects, source, reconciler)
		f.assignments = append(f.assignments, AcceptAssignment(assignment, Materialization{ControlNamespace: source.GetNamespace(), TargetNamespace: "workload", Objects: []*unstructured.Unstructured{source, reconciler}}))
	}
	kinds := map[schema.GroupVersionResource]string{}
	for _, gvk := range assignmentGVKs {
		kinds[deliveryResources[gvk].resource] = gvk.Kind + "List"
	}
	f.client = fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, objects...)
	f.client.PrependWatchReactor("*", func(action ktesting.Action) (bool, watch.Interface, error) {
		raw := watch.NewRaceFreeFake()
		f.mu.Lock()
		f.watches[action.GetResource()] = raw
		f.mu.Unlock()
		return true, raw, nil
	})
	var err error
	f.cache, err = NewAssignmentCache(f.client, AssignmentCacheOptions{Observation: observation.Options{Now: func() time.Time { return time.Unix(0, f.clock.Load()) }}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.cache.ReplaceAssignments(f.assignments); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *assignmentCacheFixture) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan error, 1)
	go func() { f.done <- f.cache.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-f.done:
		case <-time.After(5 * time.Second):
			t.Error("assignment cache shutdown blocked")
		}
	})
}
func eventuallyAssignment(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("assignment observation did not converge")
}
func (f *assignmentCacheFixture) current() bool {
	snapshots := f.cache.SnapshotAssignments()
	if len(snapshots) != len(f.assignments) {
		return false
	}
	for _, snapshot := range snapshots {
		if snapshot.Source.Observation.State != protocol.ObservationCurrent || snapshot.Reconciler.Observation.State != protocol.ObservationCurrent {
			return false
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.watches) == 5
}
func (f *assignmentCacheFixture) watcher(gvr schema.GroupVersionResource) *watch.RaceFreeFakeWatcher {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.watches[gvr]
}

func TestAssignmentCacheSteadyReadsNoGETs(t *testing.T) {
	for _, n := range []int{1, 10, 100} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := newAssignmentCacheFixture(t, n)
			executor, _ := NewExecutor(f.client)
			for _, accepted := range f.assignments {
				for _, identity := range accepted.Objects {
					if _, err := executor.Get(context.Background(), identity); err != nil {
						t.Fatal(err)
					}
				}
			}
			gets := 0
			for _, action := range f.client.Actions() {
				if action.GetVerb() == "get" {
					gets++
				}
			}
			if gets != 2*n {
				t.Fatalf("direct baseline GETs=%d want%d", gets, 2*n)
			}
			f.client.ClearActions()
			f.start(t)
			eventuallyAssignment(t, f.current)
			for i := 0; i < 10; i++ {
				if !f.current() {
					t.Fatal("lost initial synchronization")
				}
			}
			counts := map[string]int{}
			for _, action := range f.client.Actions() {
				counts[action.GetVerb()]++
				if action.GetNamespace() != "" {
					t.Fatal("unexpected per-namespace watcher")
				}
				switch a := action.(type) {
				case ktesting.ListAction:
					if a.GetListRestrictions().Labels.String() != ManagedByLabel+"="+ManagedByValue {
						t.Fatal("unscoped list")
					}
				case ktesting.WatchAction:
					if a.GetWatchRestrictions().Labels.String() != ManagedByLabel+"="+ManagedByValue {
						t.Fatal("unscoped watch")
					}
				}
			}
			if counts["get"] != 0 || counts["list"] != 5 || counts["watch"] != 5 {
				t.Fatalf("cached counts=%v", counts)
			}
		})
	}
}

func TestAssignmentCacheProjectionMutationDeleteAndFreshness(t *testing.T) {
	f := newAssignmentCacheFixture(t, 1)
	f.start(t)
	eventuallyAssignment(t, f.current)
	id := f.assignments[0].DeploymentID
	first := f.cache.SnapshotAssignments()[id]
	payload, _ := json.Marshal(first.Source.Object)
	if strings.Contains(string(payload), "do-not-retain") || strings.Contains(string(payload), "sensitive.example") || strings.Contains(string(payload), "credential@example") || strings.Contains(string(payload), "token=abcdef") || strings.Contains(string(payload), `"spec"`) {
		t.Fatalf("projection retained sensitive fields: %s", payload)
	}
	status, err := NormalizeAcceptedObservation(AcceptedObservation{Assignment: first.Assignment, Source: first.Source.Object, Reconciler: first.Reconciler.Object, ObservedAt: *first.Source.Observation.ObservedAt})
	if err != nil || status.Phase != "ready" || status.Inventory.Entries != 2 {
		t.Fatalf("projection broke normalization: %#v %v", status, err)
	}
	first.Source.Object.SetLabels(nil)
	first.Assignment.Objects[0].Name = "changed"
	*first.Source.Observation.ObservedAt = time.Time{}
	fresh := f.cache.SnapshotAssignments()[id]
	if fresh.Source.Object.GetLabels()[ManagedByLabel] != ManagedByValue || fresh.Assignment.Objects[0].Name == "changed" || fresh.Source.Observation.ObservedAt.IsZero() {
		t.Fatal("snapshot mutation corrupted source")
	}
	f.cache.ConsumeDirty()
	f.clock.Add(int64(time.Second))
	source := f.objects[0].DeepCopy()
	source.SetResourceVersion("2")
	_ = unstructured.SetNestedField(source.Object, "new-revision", "status", "artifact", "revision")
	gvr := deliveryResources[gitRepositoryGVK].resource
	f.watcher(gvr).Modify(source)
	eventuallyAssignment(t, func() bool {
		s := f.cache.SnapshotAssignments()[id]
		if s.Source.Object == nil {
			return false
		}
		revision, _, _ := unstructured.NestedString(s.Source.Object.Object, "status", "artifact", "revision")
		return revision == "new-revision" && s.Source.Observation.State == protocol.ObservationCurrent
	})
	eventuallyAssignment(t, func() bool { return len(f.cache.ConsumeDirty()) == 1 })
	beforeDelete := f.cache.SnapshotAssignments()[id].Source.Observation.ObservedAt
	f.clock.Add(int64(time.Second))
	source.SetResourceVersion("3")
	f.watcher(gvr).Delete(source)
	eventuallyAssignment(t, func() bool {
		s := f.cache.SnapshotAssignments()[id].Source
		return s.Object == nil && s.Observation.State == protocol.ObservationAbsent && s.Observation.ObservedAt.After(*beforeDelete)
	})
	at := *f.cache.SnapshotAssignments()[id].Source.Observation.ObservedAt
	f.clock.Add(int64(time.Minute))
	if got := f.cache.SnapshotAssignments()[id].Source.Observation.ObservedAt; !got.Equal(at) {
		t.Fatal("lookup restamped absence")
	}
	f.clock.Add(int64(5 * time.Minute))
	if got := f.cache.SnapshotAssignments()[id].Source.Observation.State; got != protocol.ObservationStale {
		t.Fatalf("expired cache state=%s", got)
	}
}

func TestAssignmentCacheSubscriptionBoundsAndShutdown(t *testing.T) {
	f := newAssignmentCacheFixture(t, 2)
	if err := f.cache.ReplaceAssignments(append(f.assignments, f.assignments[0])); err == nil {
		t.Fatal("duplicate accepted")
	}
	bad := f.assignments[0]
	bad.ControlNamespace = "outside"
	if err := f.cache.ReplaceAssignments([]AcceptedAssignment{bad}); err == nil {
		t.Fatal("foreign namespace accepted")
	}
	if len(f.cache.SnapshotAssignments()) != 2 {
		t.Fatal("invalid replacement mutated registry")
	}
	f.cache.limit = 1
	if err := f.cache.ReplaceAssignments(f.assignments); err == nil {
		t.Fatal("assignment bound bypassed")
	}
	f.cache.limit = 2
	for i := 0; i < 1000; i++ {
		f.cache.markKindDirty(deliveryResources[gitRepositoryGVK].resource)
	}
	if len(f.cache.wake) != 1 || len(f.cache.dirty) != 2 {
		t.Fatal("unbounded dirty queue")
	}
	if err := f.cache.ReplaceAssignments(f.assignments[:1]); err != nil {
		t.Fatal(err)
	}
	dirty := f.cache.ConsumeDirty()
	if len(dirty) != 1 || dirty[0] != f.assignments[0].DeploymentID || len(f.cache.reverse) != 2 {
		t.Fatalf("subscription eviction failed: %v", dirty)
	}
	if err := f.cache.ReplaceAssignments(nil); err != nil {
		t.Fatal(err)
	}
	if len(f.cache.ConsumeDirty()) != 0 || len(f.cache.reverse) != 0 || len(f.cache.wake) != 0 {
		t.Fatal("empty registry retained work")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.cache.Run(ctx); err != context.Canceled {
		t.Fatalf("canceled start=%v", err)
	}
	if len(f.client.Actions()) != 0 {
		t.Fatal("canceled startup contacted API")
	}
	if err := f.cache.Run(context.Background()); err == nil {
		t.Fatal("terminal source restarted")
	}
}
