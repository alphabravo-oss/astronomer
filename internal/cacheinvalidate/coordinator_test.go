package cacheinvalidate

import (
	"bytes"
	"context"
	"encoding/json"
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
