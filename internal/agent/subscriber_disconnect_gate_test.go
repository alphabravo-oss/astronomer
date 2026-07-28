package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// fakeMirrorWatcher is a toggleable MirrorConnectionWatcher. Starts
// disconnected, mirroring fakeStateWatcher.
type fakeMirrorWatcher struct{ connected atomic.Bool }

func (f *fakeMirrorWatcher) IsConnected() bool { return f.connected.Load() }

// saturateDataQueue fills a disconnected client's data queue, which is the state
// an outage actually leaves behind: no writeLoop, nothing draining, 256 slots
// full.
func saturateDataQueue(t *testing.T, tc *TunnelClient) {
	t.Helper()
	for len(tc.sendCh) < sendQueueSize {
		if err := tc.Send(&protocol.Message{Type: protocol.MsgK8sStreamFrame, StreamID: "wedge"}); err != nil {
			t.Fatalf("filling the data queue at depth %d: %v", len(tc.sendCh), err)
		}
	}
}

// TestInformerEventsDoNotBlockWhileDisconnected is the regression for
// backpressure applied without a connectivity gate.
//
// emit binds its send to runCtx — the PROCESS context, not the connection — so
// while the tunnel is down every informer callback used to wait the FULL
// sendQueueWait before dropping. Measured before the gate: three events took
// 6.003s, one 2s wait each. Each of the ~24 informers has its own client-go
// processorListener whose pendingNotifications is an unbounded RingGrowing, so
// during an outage the whole cluster's event stream accumulates in RAM behind
// those waits against a 512Mi limit — the OOM the in-flight bound exists to
// prevent, reintroduced on the producer side. And the work is waste: replayAll
// re-emits everything from cache on the next false->true edge.
func TestInformerEventsDoNotBlockWhileDisconnected(t *testing.T) {
	const events = 5

	tc := NewTunnelClient(testConfig(), testLogger())
	saturateDataQueue(t, tc)

	watcher := &fakeStateWatcher{} // disconnected
	subscriber := NewStateSubscriber(nil, tc, slog.New(slog.NewTextHandler(testWriter{t}, nil)))
	subscriber.SetConnectionWatcher(watcher)

	start := time.Now()
	for i := 0; i < events; i++ {
		subscriber.dispatch(protocol.StateUpdateOpModified, "Pod", "", "v1", &metav1.ObjectMeta{
			Name:            fmt.Sprintf("pod-%d", i),
			Namespace:       "default",
			ResourceVersion: "1",
		})
	}
	if elapsed := time.Since(start); elapsed >= sendQueueWait {
		t.Fatalf("%d informer events took %v while disconnected, want well under one %v queue wait; every informer goroutine is parked and client-go's unbounded notification ring grows behind them",
			events, elapsed, sendQueueWait)
	}
}

// TestMirrorEventsDoNotBlockWhileDisconnected is the same property on the
// mirror path, which matters more there: mirror frames carry FULL object bodies,
// so what accumulates behind a parked callback is whole Kubernetes objects.
func TestMirrorEventsDoNotBlockWhileDisconnected(t *testing.T) {
	const events = 5

	tc := NewTunnelClient(testConfig(), testLogger())
	saturateDataQueue(t, tc)

	watcher := &fakeMirrorWatcher{} // disconnected
	subscriber := NewMirrorSubscriber(nil, nil, tc, slog.New(slog.NewTextHandler(testWriter{t}, nil)))
	subscriber.SetConnectionWatcher(watcher)
	subscriber.ready.Store(true)

	start := time.Now()
	for i := 0; i < events; i++ {
		u := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ResourceQuota",
			"metadata":   map[string]any{"name": fmt.Sprintf("q-%d", i), "namespace": "default"},
		}}
		subscriber.sendEvent(protocol.MirrorOpAdded, "ResourceQuota", u)
		subscriber.dispatchDeleteUnstructured("ResourceQuota", u)
	}
	if elapsed := time.Since(start); elapsed >= sendQueueWait {
		t.Fatalf("%d mirror events took %v while disconnected, want well under one %v queue wait", events, elapsed, sendQueueWait)
	}
}

// TestReconnectReplayIsCompleteAfterADisconnectedGap is the other half of the
// contract: dropping live events while disconnected is only safe because the
// reconnect replay re-derives the whole cache. The gate must therefore apply to
// the informer-callback path ONLY — replayAll still blocks on backpressure and
// still delivers every object.
func TestReconnectReplayIsCompleteAfterADisconnectedGap(t *testing.T) {
	const objects = 200

	defer func(prev int) { replayRatePerSecond = prev }(replayRatePerSecond)
	replayRatePerSecond = 20000

	tc := NewTunnelClient(testConfig(), testLogger())
	watcher := &fakeStateWatcher{} // disconnected
	subscriber := NewStateSubscriber(nil, tc, slog.New(slog.NewTextHandler(testWriter{t}, nil)))
	subscriber.SetConnectionWatcher(watcher)

	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for i := 0; i < objects; i++ {
		if err := store.Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("pod-%d", i),
			Namespace:       "default",
			ResourceVersion: "1",
		}}); err != nil {
			t.Fatalf("seed store: %v", err)
		}
		// A live event for the same object while disconnected: dropped.
		subscriber.dispatch(protocol.StateUpdateOpModified, "Pod", "", "v1", &metav1.ObjectMeta{
			Name:            fmt.Sprintf("pod-%d", i),
			Namespace:       "default",
			ResourceVersion: "1",
		})
	}
	subscriber.recordStore("Pod", "", "v1", store, alwaysSynced)

	if queued := len(tc.sendCh); queued != 0 {
		t.Fatalf("%d frames were queued while disconnected; nothing drains them and the reconnect replays anyway", queued)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var received atomic.Int64
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-tc.sendCh:
				if msg.Type == protocol.MsgStateUpdate {
					received.Add(1)
				}
			}
		}
	}()

	tc.setConnected(true)
	watcher.setConnected(true)
	subscriber.replayAll(ctx)

	deadline := time.Now().Add(10 * time.Second)
	for received.Load() < objects && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := received.Load(); got != objects {
		t.Fatalf("replayed %d of %d cached objects after the reconnect; dropping live events is only safe if the replay is complete", got, objects)
	}

	cancel()
	<-drained
}
