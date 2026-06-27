package tasks

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeAgentTokenRotateQuerier struct {
	due           []sqlc.ListClustersDueForAgentTokenRotationRow
	dueErr        error
	pendingSetFor []uuid.UUID
	graceCleared  int
	graceMinutes  int32
}

func (f *fakeAgentTokenRotateQuerier) ListClustersDueForAgentTokenRotation(_ context.Context, _ int32) ([]sqlc.ListClustersDueForAgentTokenRotationRow, error) {
	if f.dueErr != nil {
		return nil, f.dueErr
	}
	return f.due, nil
}

func (f *fakeAgentTokenRotateQuerier) SetClusterAgentTokenRotationPending(_ context.Context, clusterID uuid.UUID) (int64, error) {
	f.pendingSetFor = append(f.pendingSetFor, clusterID)
	return 1, nil
}

func (f *fakeAgentTokenRotateQuerier) ClearExpiredAgentTokenRotationGrace(_ context.Context, graceMinutes int32) (int64, error) {
	f.graceCleared++
	f.graceMinutes = graceMinutes
	return 0, nil
}

// TestAgentTokenRotateSweepFlagsDueClusters: clusters returned by the policy
// query get rotation_pending_at set, and the grace backstop sweep runs.
// FAILS WITHOUT THE FIX: the periodic task / policy query did not exist.
func TestAgentTokenRotateSweepFlagsDueClusters(t *testing.T) {
	c1, c2 := uuid.New(), uuid.New()
	q := &fakeAgentTokenRotateQuerier{
		due: []sqlc.ListClustersDueForAgentTokenRotationRow{
			{ClusterID: c1, TokenRotationDays: 30},
			{ClusterID: c2, TokenRotationDays: 7},
		},
	}
	if err := runAgentTokenRotateSweep(context.Background(), AgentTokenRotateDeps{Queries: q}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(q.pendingSetFor) != 2 {
		t.Fatalf("expected 2 clusters flagged, got %d", len(q.pendingSetFor))
	}
	got := map[uuid.UUID]bool{q.pendingSetFor[0]: true, q.pendingSetFor[1]: true}
	if !got[c1] || !got[c2] {
		t.Fatalf("expected both clusters flagged, got %v", q.pendingSetFor)
	}
	if q.graceCleared != 1 {
		t.Fatalf("expected the grace backstop to run once, got %d", q.graceCleared)
	}
	if q.graceMinutes != agentTokenRotateGraceMinutes {
		t.Fatalf("grace minutes = %d, want %d", q.graceMinutes, agentTokenRotateGraceMinutes)
	}
}

// TestAgentTokenRotateSweepNoDueClusters: empty policy result flags nothing
// but still runs the grace backstop.
func TestAgentTokenRotateSweepNoDueClusters(t *testing.T) {
	q := &fakeAgentTokenRotateQuerier{due: nil}
	if err := runAgentTokenRotateSweep(context.Background(), AgentTokenRotateDeps{Queries: q}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(q.pendingSetFor) != 0 {
		t.Fatalf("expected nothing flagged, got %v", q.pendingSetFor)
	}
	if q.graceCleared != 1 {
		t.Fatalf("expected grace backstop to run, got %d", q.graceCleared)
	}
}

// TestAgentTokenRotateSweepListErrorPropagates: a list error aborts the tick.
func TestAgentTokenRotateSweepListErrorPropagates(t *testing.T) {
	q := &fakeAgentTokenRotateQuerier{dueErr: errors.New("boom")}
	if err := runAgentTokenRotateSweep(context.Background(), AgentTokenRotateDeps{Queries: q}); err == nil {
		t.Fatal("expected error to propagate")
	}
}
