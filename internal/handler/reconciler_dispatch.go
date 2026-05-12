package handler

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// claimedOp is the per-row metadata the generic dispatcher needs to
// run an operation outside the claim lock. The struct carries the
// row's ID + the type-specific "execute / record completion / record
// failure" callbacks the owning handler supplies. Callers build one
// claimedOp per row after the claim phase (under h.mu) and hand the
// slice to dispatchClaimed.
//
// Run, OnComplete, and OnFailure must each be safe to call from a
// separate goroutine; the dispatcher fans them out under a bounded
// semaphore. ID is informational only — the closures already know
// which row they belong to.
type claimedOp struct {
	ID         uuid.UUID
	Run        func(ctx context.Context) error
	OnComplete func(ctx context.Context)
	OnFailure  func(ctx context.Context, err error)
}

// dispatchClaimed runs each op via a goroutine bounded by concurrency
// (normalized through effectiveHelmConcurrency so callers can pass the
// raw struct-field knob). Caller has already claimed the rows under
// whatever lock the handler uses; this function is lock-free.
//
// Each claimedOp's Run is invoked once. On error, OnFailure handles
// the per-handler failure path (mark failed, optionally requeue, emit
// the failure event). On success, OnComplete handles the per-handler
// success path (mark completed/in-flight, emit the completion event).
// Exactly one of OnComplete/OnFailure runs per row.
//
// For handlers whose success path is non-trivial (e.g. argocd's
// async/sync split where Run needs to write the right terminal state
// into the DB itself), Run may do the bookkeeping inline and
// OnComplete can be a no-op.
func dispatchClaimed(ctx context.Context, concurrency int, claimed []claimedOp) {
	if len(claimed) == 0 {
		return
	}
	sem := make(chan struct{}, effectiveHelmConcurrency(concurrency))
	var wg sync.WaitGroup
	for _, op := range claimed {
		wg.Add(1)
		op := op
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := op.Run(ctx); err != nil {
				if op.OnFailure != nil {
					op.OnFailure(ctx, err)
				}
				return
			}
			if op.OnComplete != nil {
				op.OnComplete(ctx)
			}
		}()
	}
	wg.Wait()
}
