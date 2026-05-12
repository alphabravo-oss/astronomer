package tasks

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeDeferredQuerier records the state transitions the dispatcher
// performs so the tests can assert on dispatched / expired / failed.
type fakeDeferredQuerier struct {
	mu sync.Mutex

	pending []sqlc.DeferredOperation

	dispatched []sqlc.MarkDeferredDispatchedParams
	expired    []sqlc.MarkDeferredExpiredParams
	failed     []sqlc.MarkDeferredFailedParams
}

func (f *fakeDeferredQuerier) ListPendingDeferredOperations(_ context.Context, _ sqlc.ListPendingDeferredOperationsParams) ([]sqlc.DeferredOperation, error) {
	return f.pending, nil
}

func (f *fakeDeferredQuerier) MarkDeferredDispatched(_ context.Context, arg sqlc.MarkDeferredDispatchedParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatched = append(f.dispatched, arg)
	return nil
}

func (f *fakeDeferredQuerier) MarkDeferredExpired(_ context.Context, arg sqlc.MarkDeferredExpiredParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expired = append(f.expired, arg)
	return nil
}

func (f *fakeDeferredQuerier) MarkDeferredFailed(_ context.Context, arg sqlc.MarkDeferredFailedParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, arg)
	return nil
}

func makeDeferredRow(opType string, expiresAt time.Time) sqlc.DeferredOperation {
	return sqlc.DeferredOperation{
		ID:            uuid.New(),
		WindowID:      uuid.New(),
		OperationType: opType,
		OperationSpec: []byte(`{}`),
		Status:        "pending",
		DeferredUntil: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
		ExpiresAt:     pgtype.Timestamptz{Time: expiresAt, Valid: !expiresAt.IsZero()},
	}
}

func TestDispatcher_FiresDeferredAtOpen(t *testing.T) {
	resetRuntime()
	defer resetRuntime()
	row := makeDeferredRow("cluster.delete", time.Now().Add(time.Hour))
	q := &fakeDeferredQuerier{pending: []sqlc.DeferredOperation{row}}

	replayed := false
	replayer := func(ctx context.Context, r sqlc.DeferredOperation) error {
		replayed = true
		if r.ID != row.ID {
			t.Errorf("replayer got id %s want %s", r.ID, row.ID)
		}
		return nil
	}
	ConfigureDeferredDispatch(DeferredDispatchDeps{
		Queries: q,
		Replayers: map[string]DeferredReplayer{
			"cluster.delete": replayer,
		},
	})

	if err := HandleDispatchDeferred(context.Background(), nil); err != nil {
		t.Fatalf("HandleDispatchDeferred: %v", err)
	}
	if !replayed {
		t.Fatalf("expected replayer to fire")
	}
	if len(q.dispatched) != 1 {
		t.Fatalf("expected 1 dispatched mark, got %d", len(q.dispatched))
	}
	if q.dispatched[0].ID != row.ID {
		t.Fatalf("dispatched wrong id: got %s want %s", q.dispatched[0].ID, row.ID)
	}
	if len(q.expired) != 0 || len(q.failed) != 0 {
		t.Fatalf("expected no expired/failed marks, got expired=%d failed=%d", len(q.expired), len(q.failed))
	}
}

func TestDispatcher_ExpiresOldDeferred(t *testing.T) {
	resetRuntime()
	defer resetRuntime()
	// expires_at in the past → expired without invoking the replayer.
	row := makeDeferredRow("cluster.delete", time.Now().Add(-time.Hour))
	q := &fakeDeferredQuerier{pending: []sqlc.DeferredOperation{row}}

	replayed := false
	ConfigureDeferredDispatch(DeferredDispatchDeps{
		Queries: q,
		Replayers: map[string]DeferredReplayer{
			"cluster.delete": func(ctx context.Context, _ sqlc.DeferredOperation) error {
				replayed = true
				return nil
			},
		},
	})

	if err := HandleDispatchDeferred(context.Background(), nil); err != nil {
		t.Fatalf("HandleDispatchDeferred: %v", err)
	}
	if replayed {
		t.Fatalf("replayer should not have fired for expired row")
	}
	if len(q.expired) != 1 {
		t.Fatalf("expected 1 expired mark, got %d", len(q.expired))
	}
}

func TestDispatcher_MarksFailedOnReplayError(t *testing.T) {
	resetRuntime()
	defer resetRuntime()
	row := makeDeferredRow("cluster.delete", time.Now().Add(time.Hour))
	q := &fakeDeferredQuerier{pending: []sqlc.DeferredOperation{row}}

	ConfigureDeferredDispatch(DeferredDispatchDeps{
		Queries: q,
		Replayers: map[string]DeferredReplayer{
			"cluster.delete": func(ctx context.Context, _ sqlc.DeferredOperation) error {
				return errors.New("kaboom")
			},
		},
	})

	if err := HandleDispatchDeferred(context.Background(), nil); err != nil {
		t.Fatalf("HandleDispatchDeferred: %v", err)
	}
	if len(q.failed) != 1 {
		t.Fatalf("expected 1 failed mark, got %d", len(q.failed))
	}
	if len(q.dispatched) != 0 {
		t.Fatalf("expected 0 dispatched marks, got %d", len(q.dispatched))
	}
}

func TestDispatcher_NoReplayer_MarksFailed(t *testing.T) {
	resetRuntime()
	defer resetRuntime()
	row := makeDeferredRow("unknown.op", time.Now().Add(time.Hour))
	q := &fakeDeferredQuerier{pending: []sqlc.DeferredOperation{row}}
	ConfigureDeferredDispatch(DeferredDispatchDeps{
		Queries:   q,
		Replayers: map[string]DeferredReplayer{},
	})
	if err := HandleDispatchDeferred(context.Background(), nil); err != nil {
		t.Fatalf("HandleDispatchDeferred: %v", err)
	}
	if len(q.failed) != 1 {
		t.Fatalf("expected failed mark, got %d", len(q.failed))
	}
}

func TestDispatcher_NoDeps_NoOp(t *testing.T) {
	resetRuntime()
	defer resetRuntime()
	// Reset to nil deps.
	ConfigureDeferredDispatch(DeferredDispatchDeps{})
	if err := HandleDispatchDeferred(context.Background(), nil); err != nil {
		t.Fatalf("HandleDispatchDeferred: %v", err)
	}
}

func TestRegisterDeferredReplayer_AppendsToMap(t *testing.T) {
	resetRuntime()
	defer resetRuntime()
	ConfigureDeferredDispatch(DeferredDispatchDeps{
		Queries:   &fakeDeferredQuerier{},
		Replayers: nil,
	})
	RegisterDeferredReplayer("foo.bar", func(ctx context.Context, _ sqlc.DeferredOperation) error { return nil })

	deferredDispatchMu.RLock()
	defer deferredDispatchMu.RUnlock()
	if _, ok := deferredDispatchDeps.Replayers["foo.bar"]; !ok {
		t.Fatalf("expected replayer to be registered")
	}
}
