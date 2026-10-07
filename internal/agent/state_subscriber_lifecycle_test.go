package agent

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestStateSubscriberCanceledBeforeStartDoesNotList(t *testing.T) {
	client := fake.NewClientset()
	subscriber := NewStateSubscriber(client, &recordingSender{}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { subscriber.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not return for pre-canceled context")
	}
	if len(client.Actions()) != 0 {
		t.Fatalf("canceled subscriber contacted API: %v", client.Actions())
	}
	if subscriber.ready.Load() {
		t.Fatal("canceled subscriber advertised readiness")
	}
}

func TestStateSubscriberCancelDuringDeniedTypedSync(t *testing.T) {
	client := fake.NewClientset()
	denied := make(chan struct{}, 1)
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		select {
		case denied <- struct{}{}:
		default:
		}
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", nil)
	})
	subscriber := NewStateSubscriber(client, &recordingSender{}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { subscriber.Run(ctx); close(done) }()
	select {
	case <-denied:
	case <-time.After(time.Second):
		t.Fatal("denied LIST was not attempted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("typed cache sync ignored cancellation")
	}
	if subscriber.ready.Load() {
		t.Fatal("canceled bootstrap advertised readiness")
	}
	if _, ok := subscriber.InventoryPodCount(); ok {
		t.Fatal("denied pod cache reported authoritative inventory")
	}
}

func TestStateSubscriberPartialTypedBootstrapAndWatchCleanup(t *testing.T) {
	// Repeated boot/cancel cycles exercise watch ownership and canceled bootstrap
	// timers, without relying on noisy process-wide goroutine counts.
	for attempt := 0; attempt < 5; attempt++ {
		client := fake.NewClientset()
		client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", nil)
		})
		var mu sync.Mutex
		var watches []*trackedLifecycleWatch
		client.PrependWatchReactor("*", func(action k8stesting.Action) (bool, watch.Interface, error) {
			upstream, err := client.Tracker().Watch(action.GetResource(), action.GetNamespace())
			if err != nil {
				return true, nil, err
			}
			tracked := &trackedLifecycleWatch{Interface: upstream, stopped: make(chan struct{})}
			mu.Lock()
			watches = append(watches, tracked)
			mu.Unlock()
			return true, tracked, nil
		})
		sender := &recordingSender{}
		subscriber := NewStateSubscriber(client, sender, slog.New(slog.DiscardHandler))
		subscriber.syncTimeout = 200 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { subscriber.Run(ctx); close(done) }()
		func() {
			defer cancel()
			readyCtx, readyCancel := context.WithTimeout(ctx, 2*time.Second)
			defer readyCancel()
			if !subscriber.WaitReady(readyCtx) {
				t.Fatal("one denied typed kind blocked bounded bootstrap")
			}
			if _, ok := subscriber.InventoryPodCount(); ok {
				t.Fatal("unsynced pod cache advertised available inventory")
			}
			// Readiness is intentionally partial. Wait on actual healthy-kind
			// sync predicates rather than assuming they finish within the short
			// test bootstrap timeout on a loaded CI runner.
			if err := wait.PollUntilContextTimeout(ctx, 5*time.Millisecond, 2*time.Second, true, func(context.Context) (bool, error) {
				_, nodes := subscriber.syncedStore("Node")
				_, services := subscriber.syncedStore("Service")
				return nodes && services, nil
			}); err != nil {
				t.Fatal("healthy kinds failed to sync after partial bootstrap")
			}
			if _, err := client.CoreV1().Services("default").Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "after-bootstrap"}}, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			if waitForStateUpdate(t, sender, "Service", "after-bootstrap", time.Second) == nil {
				t.Fatal("healthy typed kind did not publish after partial bootstrap")
			}
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("subscriber failed to join informer shutdown")
		}
		mu.Lock()
		stopped := append([]*trackedLifecycleWatch(nil), watches...)
		mu.Unlock()
		if len(stopped) == 0 {
			t.Fatal("test did not start any healthy watches")
		}
		for _, watcher := range stopped {
			select {
			case <-watcher.stopped:
			default:
				t.Fatal("Run returned while an owned informer watch remained open")
			}
		}
	}
}

type trackedLifecycleWatch struct {
	watch.Interface
	once    sync.Once
	stopped chan struct{}
}

func (w *trackedLifecycleWatch) Stop() {
	w.once.Do(func() { w.Interface.Stop(); close(w.stopped) })
}

func TestStateSubscriberBoundsTypedAndMetadataBootstrapTogether(t *testing.T) {
	client := fake.NewClientset()
	deny := func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(action.GetResource().GroupResource(), "", nil)
	}
	client.PrependReactor("list", "pods", deny)
	metadata := metadatafake.NewSimpleMetadataClient(metadatafake.NewTestScheme())
	metadata.PrependReactor("list", "namespaces", deny)
	subscriber := NewStateSubscriber(client, &recordingSender{}, slog.New(slog.DiscardHandler))
	subscriber.syncTimeout = 100 * time.Millisecond
	subscriber.SetMetadataClient(metadata)
	subscriber.crdAvailable = func(metadataKind) bool { return false }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { subscriber.Run(ctx); close(done) }()
	readyCtx, readyCancel := context.WithTimeout(ctx, time.Second)
	defer readyCancel()
	if !subscriber.WaitReady(readyCtx) {
		t.Fatal("denied metadata kind added an unbounded second sync window")
	}
	for _, kind := range []string{"Pod", "Namespace"} {
		if _, ok := subscriber.syncedStore(kind); ok {
			t.Fatalf("denied %s cache advertised availability", kind)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscriber failed to stop typed and metadata factories")
	}
}
