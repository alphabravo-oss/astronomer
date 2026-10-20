//go:build integration

package systemrollout

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// An agent that already reports the target release's digest and version must
// not promote its assignment while the cohort is still pending: only
// ReleaseSystemRolloutNextCohort moves pending → released. Promoting early
// made the cluster count as done before it was ever released and stalled the
// engine.
func TestPostgresObserveDoesNotPromotePendingAssignment(t *testing.T) {
	t.Parallel()
	s, request := systemRolloutFixture(t)
	view, err := s.Start(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	view, err = s.Act(t.Context(), view.ID, view.FencingGeneration, ActionApprove, request.ActorID, "approve", systemAudit("approve"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileOne(t.Context(), view.ID); err != nil {
		t.Fatal(err)
	}
	phases := assignmentPhases(t, s, view.ID)
	var released, pending []uuid.UUID
	for cluster, phase := range phases {
		switch phase {
		case "released":
			released = append(released, cluster)
		case "pending":
			pending = append(pending, cluster)
		}
	}
	if len(released) != 1 || len(pending) != 2 {
		t.Fatalf("phases after first reconcile = %v", phases)
	}

	targetDigest := "sha256:" + strings.Repeat("b", 64) // fixture's draft release
	observe := func(cluster uuid.UUID, digest, version string) sqlc.ObserveDeliverySystemAssignmentRow {
		t.Helper()
		row, err := sqlc.New(s.pool).ObserveDeliverySystemAssignment(t.Context(), sqlc.ObserveDeliverySystemAssignmentParams{
			ClusterID: cluster, ObservedDistributionDigest: digest, ObservedAgentVersion: version,
			ObservedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, InventoryReady: true,
			CompatibilityStatus: "compatible",
		})
		if err != nil {
			t.Fatalf("observe %s: %v", cluster, err)
		}
		return row
	}

	for _, version := range []string{"1.0.0", "0.9.0"} { // matching and stale observations
		row := observe(pending[0], targetDigest, version)
		if row.Phase != "pending" || row.PreviousPhase != "pending" || row.ReadyAt.Valid {
			t.Fatalf("pending assignment observed at %s became phase=%s ready_at=%v", version, row.Phase, row.ReadyAt)
		}
		if row.ObservedAgentVersion != version {
			t.Fatalf("observation not recorded: %q", row.ObservedAgentVersion)
		}
	}
	if row := observe(released[0], targetDigest, "1.0.0"); row.Phase != "ready" || !row.ReadyAt.Valid {
		t.Fatalf("released assignment was not promoted: phase=%s", row.Phase)
	}

	// Once the next cohort is released, the same observation promotes it.
	if _, err := s.pool.Exec(t.Context(), `UPDATE delivery_system_cluster_assignments SET ready_at=now()-interval '1 hour' WHERE rollout_id=$1 AND phase='ready'`, view.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileOne(t.Context(), view.ID); err != nil {
		t.Fatal(err)
	}
	if phase := assignmentPhases(t, s, view.ID)[pending[0]]; phase != "released" {
		if phase = assignmentPhases(t, s, view.ID)[pending[1]]; phase != "released" {
			t.Fatalf("next cohort not released: %v", assignmentPhases(t, s, view.ID))
		}
		pending[0] = pending[1]
	}
	if row := observe(pending[0], targetDigest, "1.0.0"); row.Phase != "ready" || row.PreviousPhase != "released" {
		t.Fatalf("released cohort member not promoted: %s -> %s", row.PreviousPhase, row.Phase)
	}
}

func assignmentPhases(t *testing.T, s *Service, rolloutID uuid.UUID) map[uuid.UUID]string {
	t.Helper()
	rows, err := s.pool.Query(t.Context(), `SELECT cluster_id, phase FROM delivery_system_cluster_assignments WHERE rollout_id=$1`, rolloutID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var phase string
		if err := rows.Scan(&id, &phase); err != nil {
			t.Fatal(err)
		}
		out[id] = phase
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
