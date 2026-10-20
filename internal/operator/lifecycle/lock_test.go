package lifecycle

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyedLockSerializesSameKey(t *testing.T) {
	t.Parallel()
	var locks KeyedLock
	var inside, maxInside atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			unlock, err := locks.Lock(context.Background(), "ns/a")
			if err != nil {
				t.Error(err)
				return
			}
			defer unlock()
			n := inside.Add(1)
			for {
				m := maxInside.Load()
				if n <= m || maxInside.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inside.Add(-1)
		})
	}
	wg.Wait()
	if got := maxInside.Load(); got != 1 {
		t.Fatalf("max holders of one key = %d, want 1", got)
	}
	if got := locks.Tracked(); got != 0 {
		t.Fatalf("tracked keys after release = %d, want 0", got)
	}
}

func TestKeyedLockDifferentKeysDoNotBlock(t *testing.T) {
	t.Parallel()
	var locks KeyedLock
	unlockA, err := locks.Lock(context.Background(), "ns/a")
	if err != nil {
		t.Fatal(err)
	}
	defer unlockA()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlockB, err := locks.Lock(ctx, "ns/b")
	if err != nil {
		t.Fatalf("second key blocked behind first: %v", err)
	}
	unlockB()
}

func TestKeyedLockWaitHonoursContext(t *testing.T) {
	t.Parallel()
	var locks KeyedLock
	unlock, err := locks.Lock(context.Background(), "ns/a")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := locks.Lock(ctx, "ns/a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter error = %v, want deadline exceeded", err)
	}
	unlock()
	unlock() // idempotent
	if got := locks.Tracked(); got != 0 {
		t.Fatalf("tracked keys = %d, want 0", got)
	}
	again, err := locks.Lock(context.Background(), "ns/a")
	if err != nil {
		t.Fatalf("lock not reusable after cancelled waiter: %v", err)
	}
	again()
}

func TestKeyedLockCancelledContextNeverAcquires(t *testing.T) {
	t.Parallel()
	var locks KeyedLock
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := locks.Lock(ctx, "ns/a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want canceled", err)
	}
	if got := locks.Tracked(); got != 0 {
		t.Fatalf("tracked keys = %d, want 0", got)
	}
}
