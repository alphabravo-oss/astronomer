package delivery

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	ktesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestAssignmentCacheFailureRecovery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		initial bool
		want    protocol.ObservationState
	}{
		{"missing-crd-then-installed", 404, true, protocol.ObservationUnavailable},
		{"initial-denied", 403, true, protocol.ObservationDenied},
		{"permission-revoked", 403, false, protocol.ObservationDenied},
		{"resource-version-expired", 410, false, protocol.ObservationStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAssignmentCacheFixture(t, 1)
			gvr := deliveryResources[gitRepositoryGVK].resource
			var code atomic.Int64
			if tc.initial {
				code.Store(int64(tc.code))
			}
			failure := func() error {
				switch code.Load() {
				case 403:
					return apierrors.NewForbidden(gvr.GroupResource(), "", nil)
				case 404:
					return apierrors.NewNotFound(gvr.GroupResource(), "")
				case 410:
					return apierrors.NewResourceExpired("compacted")
				default:
					return nil
				}
			}
			f.client.PrependReactor("list", gvr.Resource, func(ktesting.Action) (bool, runtime.Object, error) {
				if err := failure(); err != nil {
					return true, nil, err
				}
				return false, nil, nil
			})
			f.client.PrependWatchReactor(gvr.Resource, func(ktesting.Action) (bool, watch.Interface, error) {
				if err := failure(); err != nil {
					return true, nil, err
				}
				return false, nil, nil
			})
			f.start(t)
			id := f.assignments[0].DeploymentID
			if !tc.initial {
				eventuallyAssignment(t, f.current)
				code.Store(int64(tc.code))
				reason := metav1.StatusReasonForbidden
				if tc.code == 410 {
					reason = metav1.StatusReasonExpired
				}
				f.watcher(gvr).Error(&metav1.Status{Status: metav1.StatusFailure, Reason: reason, Code: int32(tc.code)})
			}
			eventuallyAssignment(t, func() bool { return f.cache.SnapshotAssignments()[id].Source.Observation.State == tc.want })
			snapshot := f.cache.SnapshotAssignments()[id]
			if snapshot.Reconciler.Observation.State != protocol.ObservationCurrent {
				eventuallyAssignment(t, func() bool {
					return f.cache.SnapshotAssignments()[id].Reconciler.Observation.State == protocol.ObservationCurrent
				})
			}
			if tc.initial && snapshot.Source.Observation.ObservedAt != nil {
				t.Fatal("never-synced source fabricated observation time")
			}
			if !tc.initial && snapshot.Source.Observation.ObservedAt == nil {
				t.Fatal("source lost verified time on failure")
			}
			// Existing CRD/permission may become usable without restarting the source.
			code.Store(0)
			eventuallyAssignment(t, f.current)
			if s := f.cache.SnapshotAssignments()[id].Source; s.Object == nil || s.Observation.ObservedAt == nil {
				t.Fatal("recovery lost object/time")
			}
			for _, a := range f.client.Actions() {
				if a.GetVerb() == "get" {
					t.Fatal("failure silently fell back to GET")
				}
			}
		})
	}
}

func TestAssignmentCacheWatchReconnectAndShutdown(t *testing.T) {
	f := newAssignmentCacheFixture(t, 1)
	f.start(t)
	eventuallyAssignment(t, f.current)
	id := f.assignments[0].DeploymentID
	gvr := deliveryResources[gitRepositoryGVK].resource
	before := f.cache.SnapshotAssignments()[id].Source.Observation.ObservedAt
	var unavailable atomic.Bool
	f.client.PrependWatchReactor(gvr.Resource, func(ktesting.Action) (bool, watch.Interface, error) {
		if unavailable.Load() {
			return true, nil, apierrors.NewServiceUnavailable("watch unavailable")
		}
		return false, nil, nil
	})
	unavailable.Store(true)
	old := f.watcher(gvr)
	old.Stop()
	// A failed reconnect must not turn retained data into a healthy snapshot.
	eventuallyAssignment(t, func() bool {
		return f.cache.SnapshotAssignments()[id].Source.Observation.State == protocol.ObservationUnavailable
	})
	snapshot := f.cache.SnapshotAssignments()[id].Source
	if !snapshot.Observation.ObservedAt.Equal(*before) {
		t.Fatal("watch reconnect restamped source")
	}
	f.clock.Add(int64(6 * time.Second))
	eventuallyAssignment(t, func() bool {
		return f.cache.SnapshotAssignments()[id].Source.Observation.State != protocol.ObservationCurrent
	})
	unavailable.Store(false)
	eventuallyAssignment(t, func() bool { return f.watcher(gvr) != old })
	source := f.objects[0].DeepCopy()
	source.SetResourceVersion("2")
	f.watcher(gvr).Modify(source)
	eventuallyAssignment(t, func() bool {
		return f.cache.SnapshotAssignments()[id].Source.Observation.State == protocol.ObservationCurrent
	})
	f.cancel()
	eventuallyAssignment(t, func() bool {
		for _, entry := range f.cache.SnapshotAssignments() {
			if entry.Source.Observation.State != protocol.ObservationDisconnected || entry.Reconciler.Observation.State != protocol.ObservationDisconnected {
				return false
			}
		}
		return true
	})
}

func TestAssignmentCacheConcurrentSubscriptionsAndReads(t *testing.T) {
	f := newAssignmentCacheFixture(t, 10)
	f.start(t)
	eventuallyAssignment(t, f.current)
	var workers sync.WaitGroup
	for i := 0; i < 3; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := 0; n < 100; n++ {
				for _, snapshot := range f.cache.SnapshotAssignments() {
					if snapshot.Source.Object != nil {
						snapshot.Source.Object.SetLabels(nil)
					}
				}
				f.cache.ConsumeDirty()
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		for n := 0; n < 100; n++ {
			if err := f.cache.ReplaceAssignments(f.assignments[:n%11]); err != nil {
				t.Error(err)
			}
		}
	}()
	workers.Wait()
	if err := f.cache.ReplaceAssignments(nil); err != nil {
		t.Fatal(err)
	}
	if len(f.cache.SnapshotAssignments()) != 0 || len(f.cache.ConsumeDirty()) != 0 {
		t.Fatal("removed assignments survived")
	}
	f.cancel()
}

func TestAssignmentCacheInvalidConstructor(t *testing.T) {
	if _, err := NewAssignmentCache(nil, AssignmentCacheOptions{}); err == nil {
		t.Fatal("nil client accepted")
	}
	f := newAssignmentCacheFixture(t, 0)
	for _, limit := range []int{-1, maxObservedAssignments + 1} {
		if _, err := NewAssignmentCache(f.client, AssignmentCacheOptions{MaxAssignments: limit}); err == nil {
			t.Fatal("invalid bound accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.cache.Run(ctx); err != context.Canceled {
		t.Fatal(err)
	}
}
