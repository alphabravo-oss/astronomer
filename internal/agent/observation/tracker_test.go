package observation

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestSnapshotRequiresAppliedListAndNeverRefreshesOnRead(t *testing.T) {
	now := time.Now()
	tracker := NewTracker(Pods, Options{Now: func() time.Time { return now }, MaxAge: 2 * time.Minute})
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	tracker.Bind(store, func() bool { return true })
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "one", ResourceVersion: "1"}}
	list := func(context.Context, metav1.ListOptions) (runtime.Object, error) {
		return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "1"}, Items: []corev1.Pod{*pod}}, nil
	}
	if _, err := tracker.List(context.Background(), metav1.ListOptions{}, list); err != nil {
		t.Fatal(err)
	}
	raw := watch.NewRaceFreeFake()
	wrapped, err := tracker.Watch(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (watch.Interface, error) { return raw, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Stop()
	if got := tracker.Snapshot(); got.Observation.State == protocol.ObservationCurrent {
		t.Fatal("unapplied LIST certified current")
	}
	if err := store.Add(pod); err != nil {
		t.Fatal(err)
	}
	first := tracker.Snapshot()
	if first.Observation.State != protocol.ObservationCurrent {
		t.Fatalf("state=%s", first.Observation.State)
	}
	now = now.Add(time.Second)
	second := tracker.Snapshot()
	if !second.Observation.ObservedAt.Equal(*first.Observation.ObservedAt) {
		t.Fatal("read refreshed source time")
	}
	second.Pods[0].Name = "mutated"
	if tracker.Snapshot().Pods[0].Name != "one" {
		t.Fatal("snapshot mutated canonical store")
	}
	now = now.Add(3 * time.Minute)
	if got := tracker.Snapshot(); got.Observation.State != protocol.ObservationStale {
		t.Fatalf("quiet expired source=%s", got.Observation.State)
	}
}

func TestDeniedListDoesNotBecomeCurrentOnWatchOpen(t *testing.T) {
	tracker := NewTracker(Pods, Options{})
	tracker.Bind(cache.NewStore(cache.MetaNamespaceKeyFunc), func() bool { return true })
	_, _ = tracker.List(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (runtime.Object, error) {
		return nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", nil)
	})
	raw := watch.NewRaceFreeFake()
	wrapped, err := tracker.Watch(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (watch.Interface, error) { return raw, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Stop()
	if got := tracker.Snapshot(); got.Observation.State != protocol.ObservationDenied {
		t.Fatalf("watch-open hid denial: %s", got.Observation.State)
	}
}
