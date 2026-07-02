package dashboards

import (
	"strconv"
	"testing"
	"time"
)

// A cache entry read after its TTL has elapsed must be deleted, not merely
// reported as a miss. Before the fix, getMatrix/getStat returned a miss but
// left the dead entry resident forever — a monotonically growing map that only
// a process restart could reclaim.
func TestPromCache_EvictsExpiredEntryOnRead(t *testing.T) {
	c := &promCache{entries: map[string]cacheEntry{}, ttl: 30 * time.Second}

	// Matrix entry that expired a minute ago.
	c.entries["m"] = cacheEntry{expires: time.Now().Add(-time.Minute), matrix: PromMatrix{}, kind: "matrix"}
	if _, ok := c.getMatrix("m"); ok {
		t.Fatal("expired matrix entry reported as hit")
	}
	if _, present := c.entries["m"]; present {
		t.Fatal("expired matrix entry was not evicted on read")
	}

	// Stat entry that expired a minute ago.
	c.entries["s"] = cacheEntry{expires: time.Now().Add(-time.Minute), stat: 1, statOK: true, kind: "stat"}
	if _, _, ok := c.getStat("s"); ok {
		t.Fatal("expired stat entry reported as hit")
	}
	if _, present := c.entries["s"]; present {
		t.Fatal("expired stat entry was not evicted on read")
	}
}

// A key that is never re-read (deleted widget, retired time-range) can't be
// reclaimed by delete-on-read alone. Once the map crosses the sweep threshold,
// putMatrix/putStat must sweep expired keys so the cache can't grow without
// bound.
func TestPromCache_SweepsExpiredOnPutPastThreshold(t *testing.T) {
	c := &promCache{entries: map[string]cacheEntry{}, ttl: 30 * time.Second}

	// Fill the map past the sweep threshold with already-expired entries that
	// will never be read again.
	for i := 0; i < cacheSweepThreshold; i++ {
		c.entries["dead-"+strconv.Itoa(i)] = cacheEntry{expires: time.Now().Add(-time.Minute), kind: "matrix"}
	}
	if len(c.entries) < cacheSweepThreshold {
		t.Fatalf("setup: expected >= %d entries, got %d", cacheSweepThreshold, len(c.entries))
	}

	// A single put should trigger the sweep and drop every expired key, leaving
	// only the freshly-inserted one.
	c.putMatrix("live", PromMatrix{})
	if len(c.entries) != 1 {
		t.Fatalf("expected sweep to drop expired keys leaving 1 entry, got %d", len(c.entries))
	}
	if _, ok := c.getMatrix("live"); !ok {
		t.Fatal("freshly-put entry should be a hit")
	}
}
