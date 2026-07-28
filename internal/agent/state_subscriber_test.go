package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// recordingSender captures every Send call. Drop-in replacement for the live
// TunnelClient in tests so we can assert the exact sequence of frames the
// subscriber emits.
type recordingSender struct {
	mu   sync.Mutex
	msgs []*protocol.Message
}

func (r *recordingSender) SendBlocking(_ context.Context, msg *protocol.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
	return nil
}

func (r *recordingSender) Snapshot() []*protocol.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*protocol.Message, len(r.msgs))
	copy(out, r.msgs)
	return out
}

type failingSender struct {
	err error
}

func (f failingSender) SendBlocking(context.Context, *protocol.Message) error {
	return f.err
}

func counterValue(t *testing.T, c interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("read counter metric: %v", err)
	}
	if m.Counter == nil || m.Counter.Value == nil {
		t.Fatal("expected counter metric value")
	}
	return m.Counter.GetValue()
}

func TestStateSubscriberSecretInformerRequiresCompatibleExplicitProfile(t *testing.T) {
	subscriber := NewStateSubscriber(fake.NewClientset(), &recordingSender{}, slog.Default())
	if subscriber.watchSecrets {
		t.Fatal("new subscriber must default secret watching off")
	}

	for _, profile := range []string{"", "   ", "unknown", "viewer", "namespace-viewer", "namespace-operator", "custom"} {
		subscriber.SetWatchSecrets(ProfileAllowsSecrets(profile))
		if subscriber.watchSecrets {
			t.Fatalf("profile %q unexpectedly enables the Secret informer", profile)
		}
	}
	for _, profile := range []string{"operator", "admin"} {
		subscriber.SetWatchSecrets(ProfileAllowsSecrets(profile))
		if !subscriber.watchSecrets {
			t.Fatalf("explicit compatible profile %q should enable the Secret informer", profile)
		}
	}
}

// TestStateRateLimiterCollapsesBurst verifies that a burst on the same key
// emits exactly one accept and the rest are dropped within the window.
func TestStateRateLimiterCollapsesBurst(t *testing.T) {
	r := newStateRateLimiter(1*time.Second, 60*time.Second)
	// Pin time to make the test deterministic.
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }

	if !r.Allow("Pod|default|web") {
		t.Fatal("first Allow should pass")
	}
	for i := 0; i < 5; i++ {
		if r.Allow("Pod|default|web") {
			t.Fatalf("Allow #%d on the same key within window should be dropped", i)
		}
	}

	// Advance past the window; next Allow should pass again.
	now = now.Add(2 * time.Second)
	if !r.Allow("Pod|default|web") {
		t.Fatal("Allow after window should pass")
	}
}

// TestStateRateLimiterIndependentKeys verifies different keys don't share
// budgets — a key collision would mean the dashboard misses unrelated updates.
func TestStateRateLimiterIndependentKeys(t *testing.T) {
	r := newStateRateLimiter(1*time.Second, 60*time.Second)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }

	keys := []string{
		"Pod|default|a",
		"Pod|default|b",
		"Pod|kube-system|a",
		"Service|default|a",
		"Deployment|default|a",
	}
	for _, k := range keys {
		if !r.Allow(k) {
			t.Fatalf("first Allow for distinct key %q should pass", k)
		}
	}
	if r.size() != len(keys) {
		t.Fatalf("expected %d tracked keys, got %d", len(keys), r.size())
	}
}

// TestStateRateLimiterEviction verifies the eviction sweep frees old entries.
func TestStateRateLimiterEviction(t *testing.T) {
	r := newStateRateLimiter(1*time.Second, 60*time.Second)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }

	r.Allow("Pod|default|a")
	r.Allow("Pod|default|b")

	if got := r.size(); got != 2 {
		t.Fatalf("expected 2 keys, got %d", got)
	}

	// Evict everything older than now: should drop both.
	dropped := r.evictOlderThan(now.Add(time.Second))
	if dropped != 2 {
		t.Fatalf("expected 2 evictions, got %d", dropped)
	}
	if r.size() != 0 {
		t.Fatalf("expected 0 keys after evict, got %d", r.size())
	}
}

// TestStateUpdatePayloadRoundTrip verifies the wire format encodes and
// decodes losslessly. A round-trip mismatch would silently break the
// dashboard's invalidation logic.
func TestStateUpdatePayloadRoundTrip(t *testing.T) {
	original := protocol.StateUpdatePayload{
		Op:              protocol.StateUpdateOpModified,
		Kind:            "Deployment",
		APIGroup:        "apps",
		APIVersion:      "v1",
		Namespace:       "production",
		Name:            "frontend",
		ResourceVersion: "12345",
		CoalesceKey:     "Deployment|production|frontend",
	}

	body, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded protocol.StateUpdatePayload
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded != original {
		t.Fatalf("round-trip mismatch:\noriginal=%+v\ndecoded=%+v", original, decoded)
	}
}

// TestStateUpdatePayloadOmitsEmpty verifies optional fields don't show up on
// the wire when empty — keeps the JSON small for high-frequency updates.
func TestStateUpdatePayloadOmitsEmpty(t *testing.T) {
	minimal := protocol.StateUpdatePayload{
		Op:   protocol.StateUpdateOpAdded,
		Kind: "Node",
		Name: "node-1",
	}
	body, err := json.Marshal(minimal)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)
	expected := `{"op":"added","kind":"Node","name":"node-1"}`
	if got != expected {
		t.Fatalf("wire format unexpected:\nwant %s\n got %s", expected, got)
	}
}

// TestStateSubscriberEmitsOnPodCreate is the end-to-end happy path: a fake
// clientset, a recording sender, and a Pod create. The subscriber should
// publish exactly one MsgStateUpdate for the new Pod within a short window.
func TestStateSubscriberEmitsOnPodCreate(t *testing.T) {
	agentStateUpdatesReceivedTotal.Reset()
	agentStateUpdatesHandledTotal.Reset()

	// Tighten the eviction tickers and lengthen the cutoff so the test can
	// finish quickly without flaking.
	defer setStateSubscriberTunables(50*time.Millisecond, 1*time.Second, 200*time.Millisecond, 24*time.Hour)()

	client := fake.NewClientset()
	sender := &recordingSender{}
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))

	subscriber := NewStateSubscriber(client, sender, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go subscriber.Run(ctx)

	readyCtx, readyCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readyCancel()
	if !subscriber.WaitReady(readyCtx) {
		t.Fatal("state subscriber did not become ready")
	}

	// Create a Pod. The fake clientset's tracker turns this into an Add event
	// that the informer broadcasts to the registered handler.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "echo",
			Namespace:       "default",
			ResourceVersion: "1",
		},
	}
	if _, err := client.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	// Poll for the emit; up to 2s (the informer's first list happens
	// asynchronously, then the watch picks up the create).
	deadline := time.Now().Add(2 * time.Second)
	var found *protocol.StateUpdatePayload
	for time.Now().Before(deadline) {
		for _, m := range sender.Snapshot() {
			if m.Type != protocol.MsgStateUpdate {
				continue
			}
			var p protocol.StateUpdatePayload
			if err := json.Unmarshal(m.Payload, &p); err != nil {
				continue
			}
			if p.Kind == "Pod" && p.Name == "echo" {
				found = &p
				break
			}
		}
		if found != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if found == nil {
		t.Fatalf("expected a STATE_UPDATE for Pod default/echo, got none. captured=%d", len(sender.Snapshot()))
	}
	if found.Op != protocol.StateUpdateOpAdded {
		t.Errorf("expected op=added, got %s", found.Op)
	}
	if found.Namespace != "default" {
		t.Errorf("expected namespace=default, got %s", found.Namespace)
	}
	if got := counterValue(t, agentStateUpdatesReceivedTotal.WithLabelValues(observability.MetricValues("Pod")...)); got != 1 {
		t.Errorf("expected received_total{kind=Pod}=1, got %v", got)
	}
	if got := counterValue(t, agentStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("queued", "Pod")...)); got != 1 {
		t.Errorf("expected handled_total{outcome=queued,kind=Pod}=1, got %v", got)
	}
}

func TestStateSubscriberSuppressesBootstrapReplay(t *testing.T) {
	agentStateUpdatesReceivedTotal.Reset()
	agentStateUpdatesHandledTotal.Reset()

	defer setStateSubscriberTunables(50*time.Millisecond, 1*time.Second, 200*time.Millisecond, 24*time.Hour)()

	client := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "existing",
			Namespace:       "default",
			ResourceVersion: "1",
		},
	})
	sender := &recordingSender{}
	subscriber := NewStateSubscriber(client, sender, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.Run(ctx)

	readyCtx, readyCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readyCancel()
	if !subscriber.WaitReady(readyCtx) {
		t.Fatal("state subscriber did not become ready")
	}

	for _, m := range sender.Snapshot() {
		if m.Type != protocol.MsgStateUpdate {
			continue
		}
		var p protocol.StateUpdatePayload
		if err := json.Unmarshal(m.Payload, &p); err != nil {
			continue
		}
		if p.Kind == "Pod" && p.Name == "existing" {
			t.Fatalf("unexpected bootstrap STATE_UPDATE for pre-existing object: %+v", p)
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "fresh",
			Namespace:       "default",
			ResourceVersion: "2",
		},
	}
	if _, err := client.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range sender.Snapshot() {
			if m.Type != protocol.MsgStateUpdate {
				continue
			}
			var p protocol.StateUpdatePayload
			if err := json.Unmarshal(m.Payload, &p); err != nil {
				continue
			}
			if p.Kind == "Pod" && p.Name == "fresh" && p.Op == protocol.StateUpdateOpAdded {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("expected a post-sync STATE_UPDATE for Pod default/fresh, got none. captured=%d", len(sender.Snapshot()))
}

func TestStateSubscriberDispatchRateLimitedMetric(t *testing.T) {
	agentStateUpdatesReceivedTotal.Reset()
	agentStateUpdatesHandledTotal.Reset()

	subscriber := NewStateSubscriber(nil, &recordingSender{}, slog.Default())
	meta := &metav1.ObjectMeta{Name: "echo", Namespace: "default", ResourceVersion: "1"}

	subscriber.dispatch(protocol.StateUpdateOpAdded, "Pod", "", "v1", meta)
	subscriber.dispatch(protocol.StateUpdateOpModified, "Pod", "", "v1", meta)

	if got := counterValue(t, agentStateUpdatesReceivedTotal.WithLabelValues(observability.MetricValues("Pod")...)); got != 2 {
		t.Fatalf("expected received_total{kind=Pod}=2, got %v", got)
	}
	if got := counterValue(t, agentStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("queued", "Pod")...)); got != 1 {
		t.Fatalf("expected handled_total{outcome=queued,kind=Pod}=1, got %v", got)
	}
	if got := counterValue(t, agentStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("rate_limited", "Pod")...)); got != 1 {
		t.Fatalf("expected handled_total{outcome=rate_limited,kind=Pod}=1, got %v", got)
	}
}

func TestStateSubscriberDispatchSendFailedMetric(t *testing.T) {
	agentStateUpdatesReceivedTotal.Reset()
	agentStateUpdatesHandledTotal.Reset()

	subscriber := NewStateSubscriber(nil, failingSender{err: context.DeadlineExceeded}, slog.Default())
	meta := &metav1.ObjectMeta{Name: "echo", Namespace: "default", ResourceVersion: "1"}

	subscriber.dispatch(protocol.StateUpdateOpAdded, "Pod", "", "v1", meta)

	if got := counterValue(t, agentStateUpdatesReceivedTotal.WithLabelValues(observability.MetricValues("Pod")...)); got != 1 {
		t.Fatalf("expected received_total{kind=Pod}=1, got %v", got)
	}
	if got := counterValue(t, agentStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("send_failed", "Pod")...)); got != 1 {
		t.Fatalf("expected handled_total{outcome=send_failed,kind=Pod}=1, got %v", got)
	}
}

// fakeStateWatcher is a toggleable StateConnectionWatcher for the L12 replay
// tests. Starts disconnected; flip with setConnected.
type fakeStateWatcher struct{ connected atomic.Bool }

func (f *fakeStateWatcher) IsConnected() bool   { return f.connected.Load() }
func (f *fakeStateWatcher) setConnected(v bool) { f.connected.Store(v) }

// countModifiedPods scans the captured frames for replayed (Modified) Pod
// updates — only replayAll emits Modified for an otherwise-static cache.
func countModifiedPods(t *testing.T, msgs []*protocol.Message, name string) int {
	t.Helper()
	n := 0
	for _, m := range msgs {
		if m.Type != protocol.MsgStateUpdate {
			continue
		}
		var p protocol.StateUpdatePayload
		if err := json.Unmarshal(m.Payload, &p); err != nil {
			continue
		}
		if p.Kind == "Pod" && p.Name == name && p.Op == protocol.StateUpdateOpModified {
			n++
		}
	}
	return n
}

// TestStateSubscriberReplaysOnReconnect (matrix d): with a connection watcher
// wired, a false→true transition re-emits the cached informer contents as
// Modified updates — the L12 defense-in-depth resync.
func TestStateSubscriberReplaysOnReconnect(t *testing.T) {
	defer setStateSubscriberTunables(1*time.Millisecond, 1*time.Second, 200*time.Millisecond, 24*time.Hour)()

	client := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "cached", Namespace: "default", ResourceVersion: "1"},
	})
	sender := &recordingSender{}
	watcher := &fakeStateWatcher{} // starts disconnected

	subscriber := NewStateSubscriber(client, sender, slog.New(slog.NewTextHandler(testWriter{t}, nil)))
	subscriber.SetConnectionWatcher(watcher)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.Run(ctx)

	readyCtx, readyCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readyCancel()
	if !subscriber.WaitReady(readyCtx) {
		t.Fatal("state subscriber did not become ready")
	}

	// The initial-list Add for the cached Pod is bootstrap-suppressed, so no
	// Modified replay should exist yet.
	if got := countModifiedPods(t, sender.Snapshot(), "cached"); got != 0 {
		t.Fatalf("expected 0 replayed Modified before reconnect, got %d", got)
	}

	// Simulate a reconnect: false→true. The 2s replay ticker then fires once.
	watcher.setConnected(true)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countModifiedPods(t, sender.Snapshot(), "cached") >= 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if got := countModifiedPods(t, sender.Snapshot(), "cached"); got < 1 {
		t.Fatalf("expected the cached Pod to be replayed (Modified) after reconnect, got %d", got)
	}
}

// TestStateSubscriberReplayNoOpWhenUnwired (matrix d): with NO connection
// watcher wired, the replay goroutine never starts, so no Modified resync is
// ever emitted — legacy behavior is preserved exactly.
func TestStateSubscriberReplayNoOpWhenUnwired(t *testing.T) {
	defer setStateSubscriberTunables(1*time.Millisecond, 1*time.Second, 200*time.Millisecond, 24*time.Hour)()

	client := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "cached", Namespace: "default", ResourceVersion: "1"},
	})
	sender := &recordingSender{}

	// Deliberately do NOT call SetConnectionWatcher.
	subscriber := NewStateSubscriber(client, sender, slog.New(slog.NewTextHandler(testWriter{t}, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.Run(ctx)

	readyCtx, readyCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readyCancel()
	if !subscriber.WaitReady(readyCtx) {
		t.Fatal("state subscriber did not become ready")
	}

	// Wait out the same window the wired case used to fire its replay; with no
	// watcher, no Modified resync must ever appear.
	time.Sleep(3 * time.Second)
	if got := countModifiedPods(t, sender.Snapshot(), "cached"); got != 0 {
		t.Fatalf("expected 0 replayed Modified when unwired, got %d", got)
	}
}

// setStateSubscriberTunables overrides the package-level tuning vars for
// testing and returns a restore func. The vars are atomic.Int64s so
// concurrent reads from the running subscriber goroutine don't race
// against the test's set/restore.
func setStateSubscriberTunables(minInterval, evictAfter, evictEvery, eventCutoff time.Duration) func() {
	prevMin := stateSubscriberMinInterval.Load()
	prevEvictAfter := stateSubscriberEvictAfter.Load()
	prevEvictEvery := stateSubscriberEvictEvery.Load()
	prevEventCutoff := stateSubscriberEventCutoff.Load()
	stateSubscriberMinInterval.Store(int64(minInterval))
	stateSubscriberEvictAfter.Store(int64(evictAfter))
	stateSubscriberEvictEvery.Store(int64(evictEvery))
	stateSubscriberEventCutoff.Store(int64(eventCutoff))
	return func() {
		stateSubscriberMinInterval.Store(prevMin)
		stateSubscriberEvictAfter.Store(prevEvictAfter)
		stateSubscriberEvictEvery.Store(prevEvictEvery)
		stateSubscriberEventCutoff.Store(prevEventCutoff)
	}
}

// testWriter routes slog output to the test log so failures show context.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", string(p))
	return len(p), nil
}

// TestReplayAllPacedUnderBoundedSender is the item-1 counterpart on the bulk
// producer side: a reconnect replay of a large cluster must complete over the
// SAME 256-slot send queue that the burst saturates, pacing itself against the
// drain rate instead of shedding objects or closing the connection.
//
// It runs against a real *TunnelClient — the policy under test is the tunnel's,
// not a fake's — with a consumer held off until the queue is provably full, so
// the replay definitely meets a saturated channel rather than racing a fast
// reader.
//
// Before the fix, replayAll called the non-blocking Send in a tight loop with no
// pacing: object 257 overflowed, Send spawned failClose, IsConnected went false,
// and every remaining object of the 5000 was discarded — after which the
// reconnect drove another false->true edge and replayed the whole thing again.
func TestReplayAllPacedUnderBoundedSender(t *testing.T) {
	const objects = 5000

	// Keep the production pacing shape but at a rate a test can afford.
	defer func(prev int) { replayRatePerSecond = prev }(replayRatePerSecond)
	replayRatePerSecond = 20000

	tc := NewTunnelClient(testConfig(), testLogger())
	tc.setConnected(true)

	watcher := &fakeStateWatcher{}
	watcher.setConnected(true)

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
	}
	subscriber.recordStore("Pod", "", "v1", store, alwaysSynced)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Hold the consumer until the queue is provably saturated, then drain.
	saturated := make(chan struct{})
	var received atomic.Int64
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for len(tc.sendCh) < sendQueueSize {
			select {
			case <-ctx.Done():
				return
			default:
			}
			time.Sleep(time.Millisecond)
		}
		close(saturated)
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

	subscriber.replayAll(ctx)

	select {
	case <-saturated:
	default:
		t.Fatal("the send queue never saturated; the test did not exercise backpressure")
	}
	if !tc.IsConnected() {
		t.Fatal("a bounded-sender replay closed the tunnel; the reconnect would replay it again")
	}

	deadline := time.Now().Add(10 * time.Second)
	for received.Load() < objects && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := received.Load(); got != objects {
		t.Fatalf("replayed %d of %d cached objects; a paced replay must lose none", got, objects)
	}

	cancel()
	<-drained
}

// TestReplayAllAbandonsWhenTunnelDropsMidReplay: a replay into a connection
// that has died must stop, not spend its full pacing budget filling a queue
// nothing will ever drain. The next false->true edge starts a complete replay.
func TestReplayAllAbandonsWhenTunnelDropsMidReplay(t *testing.T) {
	defer func(prev int) { replayRatePerSecond = prev }(replayRatePerSecond)
	replayRatePerSecond = 20000

	tc := NewTunnelClient(testConfig(), testLogger())
	tc.setConnected(true)

	watcher := &fakeStateWatcher{}
	watcher.setConnected(true)

	subscriber := NewStateSubscriber(nil, tc, slog.New(slog.NewTextHandler(testWriter{t}, nil)))
	subscriber.SetConnectionWatcher(watcher)

	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for i := 0; i < 5000; i++ {
		if err := store.Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("pod-%d", i),
			Namespace:       "default",
			ResourceVersion: "1",
		}}); err != nil {
			t.Fatalf("seed store: %v", err)
		}
	}
	subscriber.recordStore("Pod", "", "v1", store, alwaysSynced)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Drop the tunnel as soon as the replay has queued a few frames.
	go func() {
		for len(tc.sendCh) < 8 {
			select {
			case <-ctx.Done():
				return
			default:
			}
			time.Sleep(time.Millisecond)
		}
		watcher.setConnected(false)
	}()

	done := make(chan struct{})
	go func() { defer close(done); subscriber.replayAll(ctx) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("replayAll did not abandon after the tunnel dropped")
	}

	// Far short of 5000: the walk stopped instead of draining the whole cache
	// into a dead connection.
	if queued := len(tc.sendCh); queued >= sendQueueSize {
		t.Fatalf("replay queued %d frames into a dropped tunnel; it should have abandoned early", queued)
	}
}
