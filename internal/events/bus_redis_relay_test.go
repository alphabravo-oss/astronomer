package events

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type publishRedisStub struct {
	redis.UniversalClient
	publish func(context.Context, string, any) error
}

func (s *publishRedisStub) Publish(ctx context.Context, channel string, message any) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx, "publish", channel, message)
	if s.publish == nil {
		cmd.SetVal(1)
		return cmd
	}
	if err := s.publish(ctx, channel, message); err != nil {
		cmd.SetErr(err)
		return cmd
	}
	cmd.SetVal(1)
	return cmd
}

func TestBusPublishDoesNotBlockOnRedis(t *testing.T) {
	entered := make(chan struct{}, 1)
	stub := &publishRedisStub{publish: func(ctx context.Context, _ string, _ any) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-time.After(2 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	bus := NewBus()
	bus.AttachRedis(stub, "test", nil, WithRedisRelayQueueCapacity(8))
	ctx, cancel := context.WithCancel(context.Background())
	done := bus.startRedisPublisher(ctx)

	started := time.Now()
	bus.Publish(TypeClusterMetrics, map[string]any{"cluster_id": "c1"})
	if elapsed := time.Since(started); elapsed > 25*time.Millisecond {
		t.Fatalf("Bus.Publish waited %s for Redis; want <=25ms", elapsed)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("relay worker never reached blocking Redis Publish")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay worker did not stop after cancellation")
	}
}

func TestRedisRelayFullQueueDropsAndCounts(t *testing.T) {
	oldInstanceID := observability.InstanceID()
	observability.SetInstanceID("test-event-relay-full")
	t.Cleanup(func() { observability.SetInstanceID(oldInstanceID) })

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	stub := &publishRedisStub{publish: func(ctx context.Context, _ string, _ any) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	bus := NewBus()
	bus.AttachRedis(stub, "test", nil, WithRedisRelayQueueCapacity(1))
	ctx, cancel := context.WithCancel(context.Background())
	done := bus.startRedisPublisher(ctx)

	before := counterValue(t, "astronomer_dropped_events_total", map[string]string{
		"astronomer_instance_id": "test-event-relay-full",
		"component":              "events_redis_relay",
		"reason":                 "queue_full",
	})
	bus.Publish(TypeClusterConnected, 1)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("relay did not begin first publish")
	}
	bus.Publish(TypeClusterConnected, 2) // occupies the one queue slot
	bus.Publish(TypeClusterConnected, 3) // must drop immediately
	status := bus.RelayStatus()
	if status.Enqueued != 2 || status.Dropped != 1 || status.QueueDepth != 1 || status.Capacity != 1 {
		t.Fatalf("relay status after overload = %+v, want enqueued=2 dropped=1 depth=1 capacity=1", status)
	}
	after := counterValue(t, "astronomer_dropped_events_total", map[string]string{
		"astronomer_instance_id": "test-event-relay-full",
		"component":              "events_redis_relay",
		"reason":                 "queue_full",
	})
	if after != before+1 {
		t.Fatalf("queue-full metric = %v, want %v", after, before+1)
	}

	close(release)
	waitForRelay(t, bus, time.Second, func(status RedisRelayStatus) bool { return status.Published == 2 })
	cancel()
	<-done
}

func TestRedisRelayRecoversWithinBoundedRetry(t *testing.T) {
	firstFailed := make(chan struct{})
	recovered := make(chan struct{})
	var attempts atomic.Int32
	stub := &publishRedisStub{publish: func(_ context.Context, _ string, _ any) error {
		if attempts.Add(1) == 1 {
			close(firstFailed)
			return errors.New("redis temporarily unavailable")
		}
		select {
		case <-recovered:
			return nil
		case <-time.After(time.Second):
			return errors.New("recovery signal not received")
		}
	}}
	bus := NewBus()
	bus.AttachRedis(stub, "test", nil, WithRedisRelayQueueCapacity(4))
	ctx, cancel := context.WithCancel(context.Background())
	done := bus.startRedisPublisher(ctx)
	bus.Publish(TypeClusterUpdated, map[string]any{"cluster_id": "c1"})
	select {
	case <-firstFailed:
		close(recovered)
	case <-time.After(time.Second):
		t.Fatal("first Redis failure was not observed")
	}
	waitForRelay(t, bus, time.Second, func(status RedisRelayStatus) bool {
		return status.Published == 1 && status.Dropped == 0 && status.Healthy && !status.LastSuccess.IsZero()
	})
	if got := attempts.Load(); got != 2 {
		t.Fatalf("publish attempts = %d, want bounded retry count 2", got)
	}
	cancel()
	<-done
}

func TestRedisRelayCancellationStopsWorkerAndBoundsDrain(t *testing.T) {
	entered := make(chan struct{}, 1)
	stub := &publishRedisStub{publish: func(ctx context.Context, _ string, _ any) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	bus := NewBus()
	bus.AttachRedis(stub, "test", nil, WithRedisRelayQueueCapacity(8))
	ctx, cancel := context.WithCancel(context.Background())
	done := bus.startRedisPublisher(ctx)
	for i := 0; i < 4; i++ {
		bus.Publish(TypeClusterHeartbeat, i)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("relay worker never entered publish")
	}
	started := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(redisRelayShutdownTimeout + time.Second):
		t.Fatal("relay worker leaked past bounded shutdown interval")
	}
	if elapsed := time.Since(started); elapsed > redisRelayShutdownTimeout+250*time.Millisecond {
		t.Fatalf("shutdown took %s, want <=%s", elapsed, redisRelayShutdownTimeout+250*time.Millisecond)
	}
	if status := bus.RelayStatus(); status.Running || status.Healthy {
		t.Fatalf("relay status after shutdown = %+v, want stopped/unhealthy", status)
	}
	before := bus.RelayStatus()
	bus.Publish(TypeClusterHeartbeat, "after-stop")
	after := bus.RelayStatus()
	if after.Dropped != before.Dropped+1 || after.Enqueued != before.Enqueued {
		t.Fatalf("publish after relay stop status = %+v, want one observable drop and no enqueue (before=%+v)", after, before)
	}
}

func TestRedisRelayQueueCapacityHasHardMaximum(t *testing.T) {
	stub := &publishRedisStub{}
	bus := NewBus()
	bus.AttachRedis(stub, "test", nil, WithRedisRelayQueueCapacity(MaxRedisRelayQueueCapacity+1000))
	if got := bus.RelayStatus().Capacity; got != MaxRedisRelayQueueCapacity {
		t.Fatalf("capacity = %d, want hard max %d", got, MaxRedisRelayQueueCapacity)
	}

	defaultBus := NewBus()
	defaultBus.AttachRedis(stub, "test", nil, WithRedisRelayQueueCapacity(0))
	if got := defaultBus.RelayStatus().Capacity; got != DefaultRedisRelayQueueCapacity {
		t.Fatalf("default capacity = %d, want %d", got, DefaultRedisRelayQueueCapacity)
	}
}

func TestRedisRelayStartsExactlyOnePublisherWorker(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	stub := &publishRedisStub{publish: func(ctx context.Context, _ string, _ any) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	bus := NewBus()
	bus.AttachRedis(stub, "test", nil)
	ctx, cancel := context.WithCancel(context.Background())
	doneA := bus.startRedisPublisher(ctx)
	doneB := bus.startRedisPublisher(ctx)
	if doneA != doneB {
		t.Fatal("duplicate start returned a different worker completion channel")
	}
	bus.Publish(TypeClusterConnected, nil)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("publisher worker did not start")
	}
	time.Sleep(10 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("Redis Publish calls while blocked = %d, want one worker call", got)
	}
	close(release)
	cancel()
	<-doneA
}

func TestRedisRelayRemoteSSEExactlyOnceAndSuppressesOriginEcho(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = clientA.Close()
		_ = clientB.Close()
	})
	busA := NewBus()
	busA.origin = "pod-a"
	busB := NewBus()
	busB.origin = "pod-b"
	busA.AttachRedis(clientA, DefaultRedisChannel, nil)
	busB.AttachRedis(clientB, DefaultRedisChannel, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneA := make(chan struct{})
	doneB := make(chan struct{})
	go func() { defer close(doneA); busA.StartRedisRelay(ctx) }()
	go func() { defer close(doneB); busB.StartRedisRelay(ctx) }()
	waitForRedisSubscribers(t, clientA, DefaultRedisChannel, 2)

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	local := busA.Subscribe(subCtx)
	remote := busB.Subscribe(subCtx)
	busA.Publish(TypeClusterConnected, map[string]any{"cluster_id": "c1"})

	localEvent := receiveEvent(t, local)
	if localEvent.Remote {
		t.Fatal("origin SSE delivery must be local")
	}
	remoteEvent := receiveEvent(t, remote)
	if !remoteEvent.Remote || remoteEvent.Origin != "pod-a" {
		t.Fatalf("remote SSE event = %+v, want Remote=true origin=pod-a", remoteEvent)
	}
	select {
	case duplicate := <-local:
		t.Fatalf("origin echo was not suppressed: %+v", duplicate)
	case duplicate := <-remote:
		t.Fatalf("remote SSE received duplicate: %+v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-doneA:
	case <-time.After(time.Second):
		t.Fatal("bus A relay did not stop")
	}
	select {
	case <-doneB:
	case <-time.After(time.Second):
		t.Fatal("bus B relay did not stop")
	}
}

func TestRedisRelayRateLimitsOutageLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	stub := &publishRedisStub{publish: func(context.Context, string, any) error {
		return errors.New("redis unavailable")
	}}
	bus := NewBus()
	bus.AttachRedis(stub, "test", logger, WithRedisRelayQueueCapacity(4))
	ctx, cancel := context.WithCancel(context.Background())
	done := bus.startRedisPublisher(ctx)
	bus.Publish(TypeClusterConnected, 1)
	bus.Publish(TypeClusterConnected, 2)
	waitForRelay(t, bus, time.Second, func(status RedisRelayStatus) bool { return status.Dropped == 2 })
	if got := bytes.Count(logs.Bytes(), []byte("events redis publish failed")); got != 1 {
		t.Fatalf("outage log count = %d, want 1 within suppression window; logs=%s", got, logs.String())
	}
	cancel()
	<-done
}

func TestBusConcurrentPublishAndSubscribe(t *testing.T) {
	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const publishers = 16
	const eventsPerPublisher = 100
	var wg sync.WaitGroup
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func(publisher int) {
			defer wg.Done()
			for event := 0; event < eventsPerPublisher; event++ {
				bus.Publish(TypeClusterMetrics, map[string]int{"publisher": publisher, "event": event})
			}
		}(i)
	}
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			subCtx, subCancel := context.WithCancel(ctx)
			ch := bus.Subscribe(subCtx)
			for i := 0; i < 5; i++ {
				select {
				case <-ch:
				case <-time.After(time.Millisecond):
				}
			}
			subCancel()
		}()
	}
	wg.Wait()
}

func waitForRelay(t *testing.T, bus *Bus, timeout time.Duration, predicate func(RedisRelayStatus) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate(bus.RelayStatus()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for relay status; last=%+v", bus.RelayStatus())
}

func waitForRedisSubscribers(t *testing.T, client *redis.Client, channel string, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		counts, err := client.PubSubNumSub(context.Background(), channel).Result()
		if err == nil && counts[channel] == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d Redis subscribers", want)
}

func receiveEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func ExampleBus_RelayStatus() {
	bus := NewBus()
	bus.AttachRedis(&publishRedisStub{}, "test", nil, WithRedisRelayQueueCapacity(8))
	status := bus.RelayStatus()
	fmt.Println(status.Capacity)
	// Output: 8
}
