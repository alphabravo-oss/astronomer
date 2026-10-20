//go:build integration

package systemrollout

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

// A max_unavailable of {count:0} (astro's former default) used to resolve to
// zero release slots, so an approved system rollout never released a single
// cluster. It now rolls out serially.
func TestPostgresSystemRolloutZeroMaxUnavailableReleasesSerially(t *testing.T) {
	t.Parallel()
	s, request := systemRolloutFixture(t)
	request.Strategy.MaxConcurrent = 10
	request.Strategy.MaxUnavailable = model.Amount{Type: model.AmountCount, Value: 0}
	view, err := s.Start(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	view, err = s.Act(t.Context(), view.ID, view.FencingGeneration, ActionApprove, request.ActorID, "approve", systemAudit("approve"))
	if err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 3; round++ {
		if err := s.ReconcileOne(t.Context(), view.ID); err != nil {
			t.Fatal(err)
		}
		var released, ready int
		if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE phase='released'), count(*) FILTER (WHERE phase='ready') FROM delivery_system_cluster_assignments WHERE rollout_id=$1`, view.ID).Scan(&released, &ready); err != nil {
			t.Fatal(err)
		}
		if released != 1 || ready != round-1 {
			t.Fatalf("round %d: released=%d ready=%d, want exactly one released cluster per round", round, released, ready)
		}
		if _, err := s.pool.Exec(t.Context(), `UPDATE delivery_system_cluster_assignments SET phase='ready',ready_at=now()-interval '1 hour' WHERE rollout_id=$1 AND phase='released'`, view.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReconcileOne(t.Context(), view.ID); err != nil {
		t.Fatal(err)
	}
	view, err = s.Get(t.Context(), view.ID)
	if err != nil || view.State != "succeeded" || view.ReadyClusters != 3 {
		t.Fatalf("state=%s ready=%d err=%v, want succeeded with 3 ready", view.State, view.ReadyClusters, err)
	}
}
