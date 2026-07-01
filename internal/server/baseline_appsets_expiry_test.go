package server

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestLeaveLocalExclusionsSkipsExpiredDecisions is the regression for the
// "ownership decisions never expire" bug. An expired leave_local row must
// NOT exclude its cluster from the baseline fan-out (ArgoCD should adopt
// the component once the cutover window lapses); a not-yet-expired row and
// a row with no expiry must still exclude their clusters.
func TestLeaveLocalExclusionsSkipsExpiredDecisions(t *testing.T) {
	expiredCluster := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	activeCluster := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	permanentCluster := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	q := baselineAppSetQuerierStub{
		decisions: []sqlc.ArgocdBaselineOwnershipDecision{
			{
				ClusterID: expiredCluster, ComponentSlug: "kube-state-metrics", Decision: "leave_local",
				ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
			},
			{
				ClusterID: activeCluster, ComponentSlug: "kube-state-metrics", Decision: "leave_local",
				ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
			},
			{
				ClusterID: permanentCluster, ComponentSlug: "kube-state-metrics", Decision: "leave_local",
			},
		},
	}

	out := leaveLocalExclusionsByComponent(context.Background(), q)
	got := map[string]bool{}
	for _, id := range out["kube-state-metrics"] {
		got[id] = true
	}

	if got[expiredCluster.String()] {
		t.Errorf("expired leave_local decision must NOT exclude cluster %s", expiredCluster)
	}
	if !got[activeCluster.String()] {
		t.Errorf("unexpired leave_local decision must exclude cluster %s", activeCluster)
	}
	if !got[permanentCluster.String()] {
		t.Errorf("leave_local decision with no expiry must exclude cluster %s", permanentCluster)
	}
}
