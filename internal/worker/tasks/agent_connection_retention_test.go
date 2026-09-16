package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

type agentConnectionRetentionQuerier struct {
	RuntimeQuerier
	calls  int
	cutoff time.Time
	pruned int64
	err    error
}

func (q *agentConnectionRetentionQuerier) PruneAgentConnectionHistoryBefore(_ context.Context, cutoff time.Time) (int64, error) {
	q.calls++
	q.cutoff = cutoff
	return q.pruned, q.err
}

func TestAgentConnectionRetentionPrunesDefaultWindow(t *testing.T) {
	q := &agentConnectionRetentionQuerier{pruned: 9}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q})

	before := time.Now().UTC().Add(-AgentConnectionHistoryRetention)
	if err := HandleAgentConnectionRetention(ctx, &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	after := time.Now().UTC().Add(-AgentConnectionHistoryRetention)
	if q.calls != 1 {
		t.Fatalf("prune calls = %d, want 1", q.calls)
	}
	if q.cutoff.Before(before) || q.cutoff.After(after) {
		t.Fatalf("cutoff %s outside [%s, %s]", q.cutoff, before, after)
	}
}

func TestAgentConnectionRetentionSkipsNonLeader(t *testing.T) {
	q := &agentConnectionRetentionQuerier{}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q, Leader: &fakeLeader{held: false}})
	if err := HandleAgentConnectionRetention(ctx, &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if q.calls != 0 {
		t.Fatalf("non-leader prune calls = %d, want 0", q.calls)
	}
}

func TestAgentConnectionRetentionPropagatesDeleteFailure(t *testing.T) {
	want := errors.New("database unavailable")
	q := &agentConnectionRetentionQuerier{err: want}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q})
	err := HandleAgentConnectionRetention(ctx, &asynq.Task{})
	if !errors.Is(err, want) {
		t.Fatalf("handle error = %v, want wrapped %v", err, want)
	}
}
