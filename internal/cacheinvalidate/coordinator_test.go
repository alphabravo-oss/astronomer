package cacheinvalidate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type recordingTarget struct {
	mu     sync.Mutex
	events []string
	onAll  func(string)
}

type commandFailureHook struct {
	command string
	fail    atomic.Bool
	once    bool
}

func (h *commandFailureHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *commandFailureHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if strings.EqualFold(cmd.Name(), h.command) && h.fail.Load() {
			if !h.once || h.fail.CompareAndSwap(true, false) {
				return errors.New("injected " + h.command + " failure")
			}
		}
		return next(ctx, cmd)
	}
}
func (h *commandFailureHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// Compile-time guard: hooks share the redis client with the coordinator, so
// failures can be scoped to INCR or PUBLISH without disturbing SUBSCRIBE/MGET.
var _ redis.Hook = (*commandFailureHook)(nil)

func (t *recordingTarget) add(event string) {
	t.mu.Lock()
	t.events = append(t.events, event)
	callback := t.onAll
	t.mu.Unlock()
	if callback != nil && strings.HasSuffix(event, ":all") {
		callback(event)
	}
}
func (t *recordingTarget) InvalidateJWTJTILocal(id string)   { t.add("jwt:jti:" + id) }
func (t *recordingTarget) InvalidateJWTUserLocal(id string)  { t.add("jwt:user:" + id) }
func (t *recordingTarget) InvalidateJWTAllLocal()            { t.add("jwt:all") }
func (t *recordingTarget) InvalidateRBACUserLocal(id string) { t.add("rbac:user:" + id) }
func (t *recordingTarget) InvalidateRBACAllLocal()           { t.add("rbac:all") }
func (t *recordingTarget) reset()                            { t.mu.Lock(); t.events = nil; t.mu.Unlock() }
func (t *recordingTarget) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.events...)
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr(), DialTimeout: 50 * time.Millisecond, ReadTimeout: 50 * time.Millisecond, WriteTimeout: 50 * time.Millisecond, MaxRetries: 0})
	t.Cleanup(func() { _ = client.Close() })
	return mini, client
}

func TestTwoCoordinatorsTargetedPropagationAndSLO(t *testing.T) {
	_, clientA := newRedis(t)
	clientB := redis.NewClient(clientA.Options())
	t.Cleanup(func() { _ = clientB.Close() })
	targetA, targetB := &recordingTarget{}, &recordingTarget{}
	a := New(clientA, targetA, "pod-a", 5*time.Second, nil)
	b := New(clientB, targetB, "pod-b", 5*time.Second, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	go b.Run(ctx)
	waitFor(t, time.Second, func() bool { return a.Healthy() && b.Healthy() })
	targetA.reset()
	targetB.reset()

	latencies := make([]time.Duration, 0, 100)
	for i := 0; i < 100; i++ {
		subject := "user-" + strconv.Itoa(i)
		start := time.Now()
		if err := a.Broadcast(ctx, KindRBACUser, subject); err != nil {
			t.Fatalf("Broadcast: %v", err)
		}
		want := "rbac:user:" + subject
		waitFor(t, time.Second, func() bool {
			for _, event := range targetB.snapshot() {
				if event == want {
					return true
				}
			}
			return false
		})
		latencies = append(latencies, time.Since(start))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p99 := latencies[98]
	if p99 >= time.Second {
		t.Fatalf("healthy propagation p99 = %s, want <1s", p99)
	}
}

func TestDuplicateOutOfOrderAndEpochGap(t *testing.T) {
	target := &recordingTarget{}
	c := NewLocalOnly(target, "pod", nil)
	base := Message{Version: WireVersion, Kind: KindRBACUser, SubjectID: "user-a", Time: time.Now()}
	base.Epoch = 2
	payload, _ := json.Marshal(base)
	c.handlePayload(payload)
	base.Epoch = 1
	payload, _ = json.Marshal(base)
	c.handlePayload(payload)
	base.Epoch = 2
	payload, _ = json.Marshal(base)
	c.handlePayload(payload)
	got := target.snapshot()
	if len(got) != 1 || got[0] != "rbac:all" {
		t.Fatalf("events = %v, want one conservative all invalidation", got)
	}
}

func TestMissedMessageDetectedByEpochReconciliation(t *testing.T) {
	_, client := newRedis(t)
	target := &recordingTarget{}
	c := New(client, target, "pod", 20*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	waitFor(t, time.Second, c.Healthy)
	target.reset()
	if err := client.Incr(ctx, JWTEpochKey).Err(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		for _, event := range target.snapshot() {
			if event == "jwt:all" {
				return true
			}
		}
		return false
	})
}

func TestCanceledCallerContextStillDurablyInvalidatesRemote(t *testing.T) {
	_, clientA := newRedis(t)
	clientB := redis.NewClient(clientA.Options())
	t.Cleanup(func() { _ = clientB.Close() })
	targetA, targetB := &recordingTarget{}, &recordingTarget{}
	a := New(clientA, targetA, "pod-a", 20*time.Millisecond, nil)
	b := New(clientB, targetB, "pod-b", 20*time.Millisecond, nil)
	runCtx, stop := context.WithCancel(context.Background())
	defer stop()
	go a.Run(runCtx)
	go b.Run(runCtx)
	waitFor(t, time.Second, func() bool { return a.Healthy() && b.Healthy() })
	targetA.reset()
	targetB.reset()

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	if err := a.Broadcast(requestCtx, KindJWTUser, "user-after-commit"); err != nil {
		t.Fatalf("Broadcast with canceled request context: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		for _, event := range targetB.snapshot() {
			if event == "jwt:user:user-after-commit" || event == "jwt:all" {
				return true
			}
		}
		return false
	})
	if epoch, err := clientA.Get(context.Background(), JWTEpochKey).Int64(); err != nil || epoch < 1 {
		t.Fatalf("durable JWT epoch = %d, err = %v", epoch, err)
	}
}

func TestTransientINCRFailureRetriesPendingBeforeHealthRecovery(t *testing.T) {
	_, clientA := newRedis(t)
	hook := &commandFailureHook{command: "incr"}
	hook.fail.Store(true)
	clientA.AddHook(hook)
	clientB := redis.NewClient(clientA.Options())
	t.Cleanup(func() { _ = clientB.Close() })
	targetA, targetB := &recordingTarget{}, &recordingTarget{}
	a := New(clientA, targetA, "pod-a", 20*time.Millisecond, nil)
	b := New(clientB, targetB, "pod-b", 20*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	go b.Run(ctx)
	waitFor(t, time.Second, func() bool { return a.Healthy() && b.Healthy() })
	targetA.reset()
	targetB.reset()

	if err := a.Broadcast(ctx, KindRBACUser, "transient-user"); err == nil {
		t.Fatal("expected injected INCR failure")
	}
	if a.Healthy() {
		t.Fatal("origin became healthy before pending mutation was durable")
	}
	a.mu.Lock()
	pendingBeforeRetry := len(a.pending["rbac"])
	recoveryBeforeRetry := a.recover["rbac"]
	a.mu.Unlock()
	if pendingBeforeRetry == 0 || recoveryBeforeRetry != 0 {
		t.Fatalf("failed INCR pending=%d recovery=%d, want pending and no durable epoch", pendingBeforeRetry, recoveryBeforeRetry)
	}

	hook.fail.Store(false)
	waitFor(t, time.Second, func() bool {
		for _, event := range targetB.snapshot() {
			if event == "rbac:user:transient-user" || event == "rbac:all" {
				return true
			}
		}
		return false
	})
	waitFor(t, time.Second, a.Healthy)
	if epoch, err := clientA.Get(context.Background(), RBACEpochKey).Int64(); err != nil || epoch < 1 {
		t.Fatalf("durable RBAC epoch = %d, err = %v", epoch, err)
	}
}

func TestPublishFailureRecoveredByEpochBeforeOriginHealthy(t *testing.T) {
	_, clientA := newRedis(t)
	hook := &commandFailureHook{command: "publish", once: true}
	hook.fail.Store(true)
	clientA.AddHook(hook)
	clientB := redis.NewClient(clientA.Options())
	t.Cleanup(func() { _ = clientB.Close() })
	targetA, targetB := &recordingTarget{}, &recordingTarget{}
	a := New(clientA, targetA, "pod-a", 20*time.Millisecond, nil)
	b := New(clientB, targetB, "pod-b", 20*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	go b.Run(ctx)
	waitFor(t, time.Second, func() bool { return a.Healthy() && b.Healthy() })
	targetA.reset()
	targetB.reset()
	var originClearedWhileUnhealthy atomic.Bool
	targetA.mu.Lock()
	targetA.onAll = func(string) {
		if !a.Healthy() {
			originClearedWhileUnhealthy.Store(true)
		}
	}
	targetA.mu.Unlock()

	if err := a.Broadcast(ctx, KindJWTJTI, "lost-publish-jti"); err == nil {
		t.Fatal("expected injected Publish failure")
	}
	if a.Healthy() {
		t.Fatal("origin became healthy before durable epoch reconciliation")
	}
	if epoch, err := clientA.Get(context.Background(), JWTEpochKey).Int64(); err != nil || epoch < 1 {
		t.Fatalf("Publish failure did not retain durable epoch: epoch=%d err=%v", epoch, err)
	}
	waitFor(t, time.Second, func() bool {
		for _, event := range targetB.snapshot() {
			if event == "jwt:all" {
				return true
			}
		}
		return false
	})
	waitFor(t, time.Second, a.Healthy)
	if !originClearedWhileUnhealthy.Load() {
		t.Fatal("origin did not conservatively invalidate before health recovery")
	}
}

func TestMalformedRedisEpochKeepsCoordinatorUnhealthy(t *testing.T) {
	for _, value := range []string{"not-an-integer", "1.5", "-1", ""} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			mini, client := newRedis(t)
			mini.Set(JWTEpochKey, value)
			target := &recordingTarget{}
			c := New(client, target, "pod", 20*time.Millisecond, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := c.reconcile(ctx, false); err == nil {
				t.Fatalf("reconciliation accepted malformed epoch %q", value)
			}
			go c.Run(ctx)
			waitFor(t, time.Second, c.Started)
			time.Sleep(80 * time.Millisecond)
			if c.Healthy() {
				t.Fatalf("coordinator accepted malformed epoch %q", value)
			}
		})
	}
}

func TestDisconnectUnhealthyAndReconnectInvalidatesBeforeHealthy(t *testing.T) {
	mini, client := newRedis(t)
	target := &recordingTarget{}
	c := New(client, target, "pod", 20*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	waitFor(t, time.Second, c.Healthy)
	target.reset()
	mini.Close()
	waitFor(t, 2*time.Second, func() bool { return !c.Healthy() })
	var unhealthyAtInvalidate atomic.Bool
	target.mu.Lock()
	target.onAll = func(string) {
		if !c.Healthy() {
			unhealthyAtInvalidate.Store(true)
		}
	}
	target.mu.Unlock()
	if err := mini.Restart(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, c.Healthy)
	if !unhealthyAtInvalidate.Load() {
		t.Fatal("reconnect marked healthy before local cache invalidation")
	}
}

func TestWireMessageContainsNoSecretFieldsOrLogs(t *testing.T) {
	msg := Message{Version: WireVersion, Kind: KindJWTJTI, SubjectID: "opaque-jti", Epoch: 1, Origin: "pod", Time: time.Now().UTC()}
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{"token", "cookie", "password", "rules", "secret"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("wire payload contains forbidden field %q: %s", forbidden, text)
		}
	}
	if strings.Contains(text, "Bearer ") {
		t.Fatalf("wire payload contains bearer material: %s", text)
	}

	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr(), DialTimeout: 20 * time.Millisecond, MaxRetries: 0})
	defer func() { _ = client.Close() }()
	var logs bytes.Buffer
	c := New(client, &recordingTarget{}, "pod", 20*time.Millisecond, slog.New(slog.NewTextHandler(&logs, nil)))
	mini.Close()
	const marker = "dummy-sensitive-marker"
	if err := c.Broadcast(context.Background(), KindJWTJTI, marker); err == nil {
		t.Fatal("expected Redis failure")
	}
	if strings.Contains(logs.String(), marker) {
		t.Fatalf("coordinator log leaked subject identifier: %s", logs.String())
	}
}

func TestNilTargetAndUnavailableCoordinatorAreSafeAndUnhealthy(t *testing.T) {
	c := New(nil, nil, "pod", 0, nil)
	if c.Healthy() {
		t.Fatal("unavailable coordinator must start unhealthy")
	}
	c.handlePayload([]byte(`{"version":1,"kind":"rbac_all","epoch":1,"origin":"pod","time":"2026-01-01T00:00:00Z"}`))
	c.invalidateJWTAll()
	c.invalidateRBACAll()
}
