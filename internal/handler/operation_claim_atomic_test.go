package handler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// fakeOp models a DB row claimed via MarkRunning CAS semantics.
type fakeOp struct {
	id     uuid.UUID
	target string
	status string
}

// TestClaimLatestOperationsAtomicMarkRunningSimulatesHA proves that when
// MarkRunning implements a CAS (second caller fails), claimLatestOperations
// only returns one claimed op — the shipped HA contract for tool/catalog/etc.
func TestClaimLatestOperationsAtomicMarkRunningSimulatesHA(t *testing.T) {
	opID := uuid.New()
	var winners atomic.Int32
	var mu sync.Mutex
	status := "pending"

	mark := func(ctx context.Context, op fakeOp) (fakeOp, error) {
		mu.Lock()
		defer mu.Unlock()
		// Only first transition from pending wins (mirrors SQL AND status='pending'
		// for the non-stale case).
		if status != "pending" {
			return fakeOp{}, errors.New("already claimed")
		}
		status = "running"
		winners.Add(1)
		op.status = "running"
		return op, nil
	}

	ops := []fakeOp{{id: opID, target: "cluster-a/tool-x", status: "pending"}}

	var wg sync.WaitGroup
	var claimedTotal atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed := claimLatestOperations(context.Background(), ops, operationRunnerConfig[fakeOp]{
				ID:          func(o fakeOp) uuid.UUID { return o.id },
				TargetKey:   func(o fakeOp) string { return o.target },
				Status:      func(o fakeOp) string { return o.status },
				MarkRunning: mark,
				Claimed: func(o fakeOp) claimedOp {
					return claimedOp{ID: o.id}
				},
			})
			claimedTotal.Add(int32(len(claimed)))
		}()
	}
	wg.Wait()

	if winners.Load() != 1 {
		t.Fatalf("MarkRunning winners = %d, want 1", winners.Load())
	}
	if claimedTotal.Load() != 1 {
		t.Fatalf("total claimed ops across goroutines = %d, want 1", claimedTotal.Load())
	}
}
