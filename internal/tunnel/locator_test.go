package tunnel

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestLocator(rdb *redis.Client, addr string) *Locator {
	return &Locator{rdb: rdb, address: addr, cancels: map[string]context.CancelFunc{}}
}

// TestLocatorDelete_CAS_DoesNotClobberNewOwner is the M10 fix: when an agent
// moves pod A -> B, B overwrites the locator entry with B's address. A's stale
// disconnect/refresh-stop must NOT delete B's fresh entry (a value-checked CAS
// delete), or the cluster is connected-but-undirectoried and non-owning replicas
// 503 until the TTL refresh.
func TestLocatorDelete_CAS_DoesNotClobberNewOwner(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	locA := newTestLocator(rdb, "10.0.0.1:8000")
	locB := newTestLocator(rdb, "10.0.0.2:8000")
	const cid = "cluster-x"

	// Agent is now owned by B.
	if err := locB.write(ctx, cid); err != nil {
		t.Fatalf("B write: %v", err)
	}

	// Stale owner A tries to clean up — CAS must spare B's entry.
	locA.Delete(ctx, cid)

	got, err := rdb.Get(ctx, locatorKeyPrefix+cid).Result()
	if err != nil {
		t.Fatalf("M10: B's locator entry was CLOBBERED by stale owner A (err=%v)", err)
	}
	if got != "10.0.0.2:8000" {
		t.Fatalf("entry = %q, want B's address 10.0.0.2:8000", got)
	}

	// The true owner B CAN delete its own entry.
	locB.Delete(ctx, cid)
	if _, err := rdb.Get(ctx, locatorKeyPrefix+cid).Result(); err != redis.Nil {
		t.Fatalf("owner B's CAS delete should have removed the entry, err=%v", err)
	}
}

// TestLocatorRefresh_SupersededDoesNotClobber is the H-01 fix: after an agent
// moves pod A -> B (B claims the entry on connect), A's periodic refresh loop
// must NOT rewrite the key back to A's dead address. The owner-checked refresh
// reports B as the owner, leaves redis pointing at B, and A self-cancels its
// refresh loop so it stops fighting B (split-brain routing guard).
func TestLocatorRefresh_SupersededDoesNotClobber(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	locA := newTestLocator(rdb, "10.0.0.1:8000")
	locB := newTestLocator(rdb, "10.0.0.2:8000")
	const cid = "cluster-move"

	// A owns initially, then the agent moves to B (connect-time unconditional
	// claim, newest-owner-wins).
	if err := locA.write(ctx, cid); err != nil {
		t.Fatalf("A write: %v", err)
	}
	if err := locB.write(ctx, cid); err != nil {
		t.Fatalf("B write: %v", err)
	}

	// Register A's refresh-loop cancel so refreshTick can drop it on supersede.
	loopCtx, cancel := context.WithCancel(ctx)
	locA.cancels[cid] = cancel

	// A's refresh tick observes B as the owner: must self-cancel, must NOT
	// clobber B's entry.
	if superseded := locA.refreshTick(loopCtx, cid); !superseded {
		t.Fatal("H-01: A's refresh should have detected it was superseded by B")
	}
	got, err := rdb.Get(ctx, locatorKeyPrefix+cid).Result()
	if err != nil {
		t.Fatalf("locator entry missing after A refresh: %v", err)
	}
	if got != "10.0.0.2:8000" {
		t.Fatalf("H-01: A's refresh CLOBBERED B; entry = %q, want B 10.0.0.2:8000", got)
	}
	// A dropped its loop bookkeeping and cancelled the loop context.
	if _, ok := locA.cancels[cid]; ok {
		t.Fatal("A should have dropped its refresh-loop cancel after being superseded")
	}
	select {
	case <-loopCtx.Done():
	default:
		t.Fatal("A's refresh-loop context should have been cancelled")
	}

	// B's own refresh keeps ownership and extends the TTL.
	owner, err := locB.refresh(ctx, cid)
	if err != nil {
		t.Fatalf("B refresh: %v", err)
	}
	if owner != "10.0.0.2:8000" {
		t.Fatalf("B refresh reported owner %q, want B's own address", owner)
	}
}

// TestLocatorDelete_CAS_OwnerDeletes confirms the happy path: the owning pod's
// delete removes its own entry.
func TestLocatorDelete_CAS_OwnerDeletes(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	loc := newTestLocator(rdb, "10.0.0.1:8000")
	const cid = "cluster-y"
	if err := loc.write(ctx, cid); err != nil {
		t.Fatalf("write: %v", err)
	}
	loc.Delete(ctx, cid)
	if _, err := rdb.Get(ctx, locatorKeyPrefix+cid).Result(); err != redis.Nil {
		t.Fatalf("owner delete should remove the entry, err=%v", err)
	}
}
