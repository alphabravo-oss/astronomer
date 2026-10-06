package observation

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type testClock struct{ n atomic.Int64 }

func newTestClock() *testClock               { c := &testClock{}; c.n.Store(time.Now().UnixNano()); return c }
func (c *testClock) now() time.Time          { return time.Unix(0, c.n.Load()) }
func (c *testClock) advance(d time.Duration) { c.n.Add(int64(d)) }
func fixtureTracker(t *testing.T, opts Options) (*Tracker, cache.Store, *corev1.Pod) {
	t.Helper()
	tr := NewTracker(Pods, opts)
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	tr.Bind(store, func() bool { return true })
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "one", ResourceVersion: "1"}}
	applyList(t, tr, store, []corev1.Pod{*pod}, "1")
	return tr, store, pod
}
func applyList(t *testing.T, tr *Tracker, store cache.Store, items []corev1.Pod, rv string) {
	t.Helper()
	_, err := tr.List(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (runtime.Object, error) {
		return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: rv}, Items: items}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := make([]any, len(items))
	for i := range items {
		objects[i] = items[i].DeepCopy()
	}
	if err := store.Replace(objects, rv); err != nil {
		t.Fatal(err)
	}
}
func openWatch(t *testing.T, tr *Tracker, ctx context.Context) (*watch.RaceFreeFakeWatcher, watch.Interface) {
	t.Helper()
	raw := watch.NewRaceFreeFake()
	wrapped, err := tr.Watch(ctx, metav1.ListOptions{}, func(_ context.Context, o metav1.ListOptions) (watch.Interface, error) {
		if !o.AllowWatchBookmarks {
			t.Error("bookmarks not requested")
		}
		return raw, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wrapped.Stop)
	return raw, wrapped
}
func receive(t *testing.T, w watch.Interface) watch.Event {
	t.Helper()
	select {
	case event, ok := <-w.ResultChan():
		if !ok {
			t.Fatal("watch closed")
		}
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("watch event blocked")
		return watch.Event{}
	}
}

func TestWatchMutationBarrierBookmarkAndDelete(t *testing.T) {
	clock := newTestClock()
	tr, store, pod := fixtureTracker(t, Options{Now: clock.now})
	raw, w := openWatch(t, tr, context.Background())
	initial := tr.Snapshot().Observation.ObservedAt
	clock.advance(time.Second)
	changed := pod.DeepCopy()
	changed.ResourceVersion = "2"
	changed.Labels = map[string]string{"new": "value"}
	raw.Modify(changed)
	receive(t, w)
	if got := tr.Snapshot(); got.Observation.State == protocol.ObservationCurrent {
		t.Fatal("unapplied update fresh")
	}
	if err := store.Update(changed); err != nil {
		t.Fatal(err)
	}
	got := tr.Snapshot()
	if got.Observation.State != protocol.ObservationCurrent || !got.Observation.ObservedAt.After(*initial) {
		t.Fatal("applied update not current")
	}
	clock.advance(time.Second)
	raw.Action(watch.Bookmark, &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "3"}})
	receive(t, w)
	if got := tr.Snapshot(); got.ResourceVersion != "3" || !got.Observation.ObservedAt.Equal(clock.now()) {
		t.Fatal("bookmark did not verify quiet watch")
	}
	raw.Delete(changed)
	receive(t, w)
	if tr.Snapshot().Observation.State == protocol.ObservationCurrent {
		t.Fatal("unapplied deletion fresh")
	}
	if err := store.Delete(changed); err != nil {
		t.Fatal(err)
	}
	if got := tr.Snapshot(); got.Observation.State != protocol.ObservationCurrent || len(got.Pods) != 0 {
		t.Fatal("delete not applied")
	}
}

func TestRelistRequiresAppliedReplacementDespiteHasSynced(t *testing.T) {
	tr, store, _ := fixtureTracker(t, Options{})
	raw, w := openWatch(t, tr, context.Background())
	before := tr.Snapshot()
	raw.Error(&metav1.Status{Reason: metav1.StatusReasonExpired, Code: 410})
	receive(t, w)
	w.Stop()
	if got := tr.Snapshot(); got.Observation.State != protocol.ObservationStale || !got.Observation.ObservedAt.Equal(*before.Observation.ObservedAt) {
		t.Fatal("expired source lost stale snapshot")
	}
	_, err := tr.List(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (runtime.Object, error) {
		return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "7"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, next := openWatch(t, tr, context.Background())
	defer next.Stop()
	if tr.Snapshot().Observation.State == protocol.ObservationCurrent {
		t.Fatal("initial HasSynced hid unapplied replacement")
	}
	if err := store.Replace(nil, "7"); err != nil {
		t.Fatal(err)
	}
	if tr.Snapshot().Observation.State != protocol.ObservationCurrent {
		t.Fatal("applied relist not current")
	}
}

func TestRolloverDoesNotRenewAndQuietWatchEventuallyRepairs(t *testing.T) {
	clock := newTestClock()
	tr, _, _ := fixtureTracker(t, Options{Now: clock.now, WatchTimeout: 20 * time.Millisecond, MaxAge: 2 * time.Minute})
	_, w := openWatch(t, tr, context.Background())
	first := tr.Snapshot()
	select {
	case <-w.ResultChan():
	case <-time.After(5 * time.Second):
		t.Fatal("watch deadline unbounded")
	}
	w.Stop()
	clock.advance(time.Second)
	_, next := openWatch(t, tr, context.Background())
	defer next.Stop()
	got := tr.Snapshot()
	if got.Observation.State != protocol.ObservationCurrent || !got.Observation.ObservedAt.Equal(*first.Observation.ObservedAt) {
		t.Fatal("normal rollover flapped or refreshed time")
	}
	clock.advance(3 * time.Minute)
	if tr.Snapshot().Observation.State != protocol.ObservationStale {
		t.Fatal("quiet watch never expires")
	}
	next.Stop()
	calls := 0
	_, err := tr.Watch(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (watch.Interface, error) {
		calls++
		return watch.NewRaceFreeFake(), nil
	})
	if !apierrors.IsResourceExpired(err) || calls != 0 {
		t.Fatalf("quiet watch did not request relist: calls=%d err=%v", calls, err)
	}
}

func TestCanceledWatchJoinsOwnedForwarder(t *testing.T) {
	for range 25 {
		tr, _, _ := fixtureTracker(t, Options{})
		ctx, cancel := context.WithCancel(context.Background())
		raw, w := openWatch(t, tr, ctx)
		_ = tr.Snapshot()
		cancel()
		w.Stop()
		if !raw.IsStopped() {
			t.Fatal("upstream watch leaked")
		}
		if _, open := <-w.ResultChan(); open {
			t.Fatal("forwarder not joined")
		}
		if tr.Snapshot().Observation.State != protocol.ObservationDisconnected {
			t.Fatal("canceled source still current")
		}
	}
}

func TestPaginatedListDoesNotExposePartialOrDuplicateRows(t *testing.T) {
	tr := NewTracker(Pods, Options{})
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	tr.Bind(store, func() bool { return true })
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "one", ResourceVersion: "1"}}
	for i := range 2 {
		opts := metav1.ListOptions{}
		next := "page2"
		if i == 1 {
			opts.Continue = "page2"
			next = ""
		}
		_, err := tr.List(context.Background(), opts, func(context.Context, metav1.ListOptions) (runtime.Object, error) {
			return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "1", Continue: next}, Items: []corev1.Pod{pod}}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && tr.Snapshot().Observation.State == protocol.ObservationCurrent {
			t.Fatal("partial page current")
		}
	}
	if err := store.Add(&pod); err != nil {
		t.Fatal(err)
	}
	_, w := openWatch(t, tr, context.Background())
	defer w.Stop()
	if got := tr.Snapshot(); got.Observation.State != protocol.ObservationCurrent || len(got.Pods) != 1 {
		t.Fatal("duplicate page identity inflated snapshot")
	}
}

func TestServerEOFGraceAndResumedProgressWithoutRelist(t *testing.T) {
	clock := newTestClock()
	tr, store, pod := fixtureTracker(t, Options{Now: clock.now})
	raw, w := openWatch(t, tr, context.Background())
	first := tr.Snapshot()
	raw.Stop()
	select {
	case <-w.ResultChan():
	case <-time.After(5 * time.Second):
		t.Fatal("EOF not forwarded")
	}
	w.Stop()
	clock.advance(time.Second)
	if got := tr.Snapshot(); got.Observation.State != protocol.ObservationCurrent || !got.Observation.ObservedAt.Equal(*first.Observation.ObservedAt) {
		t.Fatal("server timeout EOF must preserve old time within grace")
	}
	nextRaw, next := openWatch(t, tr, context.Background())
	defer next.Stop()
	clock.advance(6 * time.Second)
	if tr.Snapshot().Observation.State != protocol.ObservationDisconnected {
		t.Fatal("watch-open renewed grace without progress")
	}
	changed := pod.DeepCopy()
	changed.ResourceVersion = "2"
	nextRaw.Modify(changed)
	receive(t, next)
	if tr.Snapshot().Observation.State == protocol.ObservationCurrent {
		t.Fatal("resumed event current before application")
	}
	if err := store.Update(changed); err != nil {
		t.Fatal(err)
	}
	if got := tr.Snapshot(); got.Observation.State != protocol.ObservationCurrent || !got.Observation.ObservedAt.Equal(clock.now()) {
		t.Fatal("resumed progress did not recover disconnected source")
	}
}

func TestSyntheticRepairDoesNotCountAsAPIRequest(t *testing.T) {
	clock := newTestClock()
	var requests, repairs atomic.Int32
	tr, _, _ := fixtureTracker(t, Options{Now: clock.now, RecordRequest: func(Kind, string, string) { requests.Add(1) }, RecordWatchEvent: func(_ Kind, outcome string) {
		if outcome == "repair" {
			repairs.Add(1)
		}
	}})
	_, w := openWatch(t, tr, context.Background())
	_ = tr.Snapshot()
	w.Stop()
	if requests.Load() != 2 {
		t.Fatalf("initial LIST+WATCH count=%d", requests.Load())
	}
	clock.advance(5 * time.Minute)
	_, err := tr.Watch(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (watch.Interface, error) {
		t.Fatal("synthetic repair invoked API")
		return nil, nil
	})
	if !apierrors.IsResourceExpired(err) || requests.Load() != 2 || repairs.Load() != 1 {
		t.Fatalf("repair mixed into API count: requests=%d repairs=%d err=%v", requests.Load(), repairs.Load(), err)
	}
}

func TestProactiveRepairRetainsOnlyFreshBoundedEvidence(t *testing.T) {
	for _, graceExpired := range []bool{false, true} {
		t.Run(fmt.Sprint(graceExpired), func(t *testing.T) {
			clock := newTestClock()
			tr, store, pod := fixtureTracker(t, Options{Now: clock.now})
			raw, w := openWatch(t, tr, context.Background())
			before := tr.Snapshot()
			clock.advance(150 * time.Second)
			raw.Stop()
			select {
			case <-w.ResultChan():
			case <-time.After(5 * time.Second):
				t.Fatal("EOF")
			}
			w.Stop()
			if graceExpired {
				clock.advance(6 * time.Second)
			}
			calls := 0
			_, err := tr.Watch(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (watch.Interface, error) { calls++; return nil, nil })
			if !apierrors.IsResourceExpired(err) || calls != 0 {
				t.Fatal("default policy must schedule repair at150s before240s expiry")
			}
			got := tr.Snapshot()
			expected := protocol.ObservationCurrent
			if graceExpired {
				expected = protocol.ObservationDisconnected
			}
			if got.Observation.State != expected || !got.Observation.ObservedAt.Equal(*before.Observation.ObservedAt) {
				t.Fatalf("planned repair revived/restamped old source: %s", got.Observation.State)
			}
			clock.advance(21 * time.Second)
			if tr.Snapshot().Observation.State == protocol.ObservationCurrent {
				t.Fatal("planned repair grace unbounded")
			}
			replacement := pod.DeepCopy()
			replacement.ResourceVersion = "2"
			_, err = tr.List(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (runtime.Object, error) {
				return &corev1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "2"}, Items: []corev1.Pod{*replacement}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, next := openWatch(t, tr, context.Background())
			defer next.Stop()
			if tr.Snapshot().Observation.State == protocol.ObservationCurrent {
				t.Fatal("unapplied proactive replacement current")
			}
			if err := store.Update(replacement); err != nil {
				t.Fatal(err)
			}
			if got := tr.Snapshot(); got.Observation.State != protocol.ObservationCurrent || !got.Observation.ObservedAt.Equal(clock.now()) {
				t.Fatal("verified repair not fresh")
			}
		})
	}
}

func TestLocalDeadlineExpiredGraceCannotReviveOnRepair(t *testing.T) {
	clock := newTestClock()
	tr, _, _ := fixtureTracker(t, Options{Now: clock.now})
	_, w := openWatch(t, tr, context.Background())
	_ = tr.Snapshot()
	w.Stop()
	// Model the owned75s deadline rather than clean server EOF; raw Current is
	// intentionally retained during its grace and must not be trusted afterward.
	tr.mu.Lock()
	tr.state = protocol.ObservationCurrent
	tr.mu.Unlock()
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	clock.advance(150 * time.Second)
	tr.closed(context.Background(), expired)
	clock.advance(6 * time.Second)
	if tr.Snapshot().Observation.State != protocol.ObservationDisconnected {
		t.Fatal("deadline grace never expired")
	}
	_, err := tr.Watch(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (watch.Interface, error) {
		t.Fatal("repair issued watch")
		return nil, nil
	})
	if !apierrors.IsResourceExpired(err) {
		t.Fatal(err)
	}
	if tr.Snapshot().Observation.State != protocol.ObservationDisconnected {
		t.Fatal("raw current state revived effectively disconnected snapshot")
	}
}
