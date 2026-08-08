package charlie

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrWriteFenceClosed = errors.New("Charlie write admission is closed")

// WriteDrainState is safe to expose in operational errors: it contains no
// action arguments, results, identities, or other customer content.
type WriteDrainState struct {
	Closed             bool `json:"closed"`
	Drained            bool `json:"drained"`
	Active             int  `json:"active"`
	DistributedPending bool `json:"distributed_pending,omitempty"`
}

// WriteDrainError means admission is closed and cancellation was requested,
// but one or more executors did not stop before the caller's deadline. A
// disable operation must not report completion when this error is returned.
type WriteDrainError struct{ State WriteDrainState }

func (e *WriteDrainError) Error() string {
	if e.State.DistributedPending {
		return "Charlie write drain is still in progress on another server replica"
	}
	return fmt.Sprintf("Charlie write drain is still in progress (%d active)", e.State.Active)
}

// WriteFence is the product-local admission and cancellation registry for
// Charlie write actions. Begin and Close are serialized by the same mutex, so
// a write is either registered (and therefore cancelled/drained) or rejected;
// there is no untracked pre-dispatch window.
type WriteFence struct {
	mu      sync.Mutex
	closed  bool
	nextID  uint64
	active  map[uint64]context.CancelFunc
	changed chan struct{}
	pool    *pgxpool.Pool
	// One distributed write pipeline per server replica bounds database
	// connection use while a transaction-scoped advisory lock is held. This
	// avoids pool starvation under a burst of model-proposed actions.
	distributedSlot chan struct{}
}

// This lock key is intentionally stable across all Astronomer replicas sharing
// the product database. Transaction-scoped locks prevent connection-pool lock
// leakage on cancellation or process failure.
const charlieWriteAdvisoryLock int64 = 0x434841524c4945

func NewWriteFence() *WriteFence {
	return &WriteFence{active: map[uint64]context.CancelFunc{}, changed: make(chan struct{})}
}

func NewDistributedWriteFence(pool *pgxpool.Pool) *WriteFence {
	fence := NewWriteFence()
	fence.pool = pool
	fence.distributedSlot = make(chan struct{}, 1)
	fence.distributedSlot <- struct{}{}
	return fence
}

func (f *WriteFence) Begin(parent context.Context) (context.Context, func(), error) {
	if f == nil {
		return nil, nil, ErrWriteFenceClosed
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil, nil, ErrWriteFenceClosed
	}
	f.nextID++
	id := f.nextID
	ctx, cancel := context.WithCancel(parent)
	f.active[id] = cancel
	f.signalLocked()
	f.mu.Unlock()
	var transaction pgx.Tx
	distributedAcquired := false
	var once sync.Once
	release := func() {
		once.Do(func() {
			if transaction != nil {
				rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = transaction.Rollback(rollbackCtx)
				rollbackCancel()
			}
			if distributedAcquired {
				f.distributedSlot <- struct{}{}
			}
			f.mu.Lock()
			if registered, ok := f.active[id]; ok {
				delete(f.active, id)
				registered()
				f.signalLocked()
			}
			f.mu.Unlock()
		})
	}
	if f.pool != nil {
		select {
		case <-ctx.Done():
			release()
			return nil, nil, ErrWriteFenceClosed
		case <-f.distributedSlot:
			distributedAcquired = true
		}
		var err error
		transaction, err = f.pool.Begin(ctx)
		if err == nil {
			_, err = transaction.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, charlieWriteAdvisoryLock)
		}
		if err != nil || ctx.Err() != nil {
			release()
			return nil, nil, ErrWriteFenceClosed
		}
	}
	return ctx, release, nil
}

// Close prevents new writes and cancels every registered write. It is
// idempotent and intentionally does not wait.
func (f *WriteFence) Close() WriteDrainState {
	if f == nil {
		return WriteDrainState{Closed: true, Drained: true}
	}
	f.mu.Lock()
	f.closed = true
	for _, cancel := range f.active {
		cancel()
	}
	state := f.stateLocked()
	f.signalLocked()
	f.mu.Unlock()
	return state
}

// CloseAndWait closes admission before waiting. On timeout the fence remains
// closed and the returned state makes the incomplete drain explicit.
func (f *WriteFence) CloseAndWait(ctx context.Context) (WriteDrainState, error) {
	state, release, err := f.CloseAndHold(ctx)
	if release != nil {
		release()
	}
	return state, err
}

// CloseAndHold closes local admission, drains registered work, and retains the
// cross-replica exclusive advisory lock until release. Authority transitions
// use this form so another replica cannot admit a new write between the drain,
// workload-ceiling readback, and the durable mode CAS.
func (f *WriteFence) CloseAndHold(ctx context.Context) (WriteDrainState, func(), error) {
	if f == nil {
		return WriteDrainState{Closed: true, Drained: true}, func() {}, nil
	}
	f.Close()
	for {
		f.mu.Lock()
		state := f.stateLocked()
		changed := f.changed
		f.mu.Unlock()
		if state.Drained {
			release, err := f.holdDistributedWrites(ctx)
			if err != nil {
				state.Drained = false
				state.DistributedPending = true
				return state, nil, &WriteDrainError{State: state}
			}
			return state, release, nil
		}
		select {
		case <-ctx.Done():
			return state, nil, &WriteDrainError{State: state}
		case <-changed:
		}
	}
}

func (f *WriteFence) holdDistributedWrites(ctx context.Context) (func(), error) {
	if f == nil || f.pool == nil {
		return func() {}, nil
	}
	transaction, err := f.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, charlieWriteAdvisoryLock); err != nil {
		_ = transaction.Rollback(context.Background())
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = transaction.Rollback(releaseCtx)
		})
	}, nil
}

// Open admits writes again. Callers must first re-establish the feature,
// connection, mode, and runtime prerequisites; those live checks still apply.
func (f *WriteFence) Open() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.closed = false
	f.signalLocked()
	f.mu.Unlock()
}

func (f *WriteFence) State() WriteDrainState {
	if f == nil {
		return WriteDrainState{Closed: true, Drained: true}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stateLocked()
}

func (f *WriteFence) stateLocked() WriteDrainState {
	return WriteDrainState{Closed: f.closed, Drained: len(f.active) == 0, Active: len(f.active)}
}

func (f *WriteFence) signalLocked() {
	close(f.changed)
	f.changed = make(chan struct{})
}
