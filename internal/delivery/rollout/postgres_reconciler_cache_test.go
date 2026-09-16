package rollout

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestRuntimeCacheIsGenerationGatedAndReturnsSnapshots(t *testing.T) {
	now := time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC)
	reconciler := &Reconciler{
		now: func() time.Time { return now }, runtimeCache: make(map[uuid.UUID]runtimeCacheEntry),
	}
	rolloutID := uuid.New()
	rows := []sqlc.ListDeliveryRolloutRuntimeRow{{ClusterID: uuid.New(), State: "pending", RuntimeGeneration: 7}}
	reconciler.storeRuntime(rolloutID, 7, rows)
	rows[0].State = "failed"

	cached, ok := reconciler.cachedRuntime(rolloutID, 7)
	if !ok || len(cached) != 1 || cached[0].State != "pending" {
		t.Fatalf("cached snapshot = %#v, hit=%v", cached, ok)
	}
	cached[0].State = "ready"
	again, ok := reconciler.cachedRuntime(rolloutID, 7)
	if !ok || again[0].State != "pending" {
		t.Fatalf("caller mutated cached snapshot: %#v", again)
	}
	if _, ok := reconciler.cachedRuntime(rolloutID, 8); ok {
		t.Fatal("stale runtime generation produced a cache hit")
	}
}

func TestRuntimeCacheHasBoundedCardinality(t *testing.T) {
	reconciler := &Reconciler{now: time.Now, runtimeCache: make(map[uuid.UUID]runtimeCacheEntry)}
	for index := 0; index <= maxRuntimeCacheEntries; index++ {
		reconciler.storeRuntime(uuid.New(), 1, nil)
	}
	if got := len(reconciler.runtimeCache); got != maxRuntimeCacheEntries {
		t.Fatalf("runtime cache size = %d, want %d", got, maxRuntimeCacheEntries)
	}
}
