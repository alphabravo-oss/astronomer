package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// apiserverAuditPurgeQuerier embeds RuntimeQuerier so only the pruning method
// needs an implementation; any other call would nil-deref, flagging an
// unexpected path.
type apiserverAuditPurgeQuerier struct {
	RuntimeQuerier
	calls  int
	cutoff time.Time
	pruned int64
}

func (q *apiserverAuditPurgeQuerier) PruneApiserverAuditEventsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	q.calls++
	q.cutoff = cutoff
	return q.pruned, nil
}

func TestApiserverAuditRetention_PrunesWithDefaultWindow(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	q := &apiserverAuditPurgeQuerier{pruned: 5}
	runtimeDeps = RuntimeDependencies{Queries: q}

	before := time.Now().UTC().Add(-apiserverAuditRetention)
	if err := HandleApiserverAuditRetention(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if q.calls != 1 {
		t.Fatalf("expected 1 prune call, got %d", q.calls)
	}
	after := time.Now().UTC().Add(-apiserverAuditRetention)
	if q.cutoff.Before(before.Add(-time.Minute)) || q.cutoff.After(after.Add(time.Minute)) {
		t.Errorf("cutoff %s not within the retention window [%s, %s]", q.cutoff, before, after)
	}
}

// The prune must be leader-gated so only the lease holder runs the DELETE.
func TestApiserverAuditRetention_SkippedOnNonLeader(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	q := &apiserverAuditPurgeQuerier{}
	runtimeDeps = RuntimeDependencies{Queries: q, Leader: &fakeLeader{held: false}}

	if err := HandleApiserverAuditRetention(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if q.calls != 0 {
		t.Fatalf("non-leader replica must not prune, got %d calls", q.calls)
	}
}
