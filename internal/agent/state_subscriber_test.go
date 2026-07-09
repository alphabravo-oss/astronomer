package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

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

func (r *recordingSender) Send(msg *protocol.Message) error {
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

func (f failingSender) Send(*protocol.Message) error {
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
