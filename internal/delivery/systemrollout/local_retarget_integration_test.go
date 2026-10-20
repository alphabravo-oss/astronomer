//go:build integration

package systemrollout

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// The management-plane cluster upgrades with the server, never through a
// system rollout. After a server upgrade releases a new system release, its
// seeded assignment must follow it; before the fix it kept the retired release
// and stayed 'applying' forever once the embedded agent advanced.
func TestPostgresLocalAssignmentFollowsTheReleasedRelease(t *testing.T) {
	t.Parallel()
	s, request := systemRolloutFixture(t)
	q := sqlc.New(s.pool)
	local := uuid.New()
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO clusters VALUES($1,NULL,true,'ready')`, local); err != nil {
		t.Fatal(err)
	}
	observe := func(digest string, seeding bool) sqlc.ObserveDeliverySystemAssignmentRow {
		t.Helper()
		if _, err := q.RetargetLocalDeliverySystemAssignment(t.Context(), local); err != nil {
			t.Fatal(err)
		}
		row, err := q.ObserveDeliverySystemAssignment(t.Context(), sqlc.ObserveDeliverySystemAssignmentParams{
			ClusterID: local, ObservedDistributionDigest: digest, ObservedAgentVersion: "1.0.0",
			ObservedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, InventoryReady: true, CompatibilityStatus: "compatible",
		})
		if seeding && errors.Is(err, pgx.ErrNoRows) { // the seeding statement returns no row
			return row
		}
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	previousDigest, nextDigest := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
	// Seed: the first observation creates the assignment on the released release.
	observe(previousDigest, true)
	if row := observe(previousDigest, false); row.Phase != "ready" {
		t.Fatalf("seeded local assignment = %s", row.Phase)
	}
	// Server upgrade: the next release is released, the previous retired.
	if _, err := s.pool.Exec(t.Context(), `UPDATE delivery_system_releases SET state = CASE WHEN id=$1 THEN 'released' ELSE 'retired' END`, request.ReleaseID); err != nil {
		t.Fatal(err)
	}
	row := observe(nextDigest, false)
	if row.DesiredReleaseID != request.ReleaseID || row.Phase != "ready" {
		t.Fatalf("local assignment desired=%s phase=%s, want %s ready", row.DesiredReleaseID, row.Phase, request.ReleaseID)
	}
	// Non-local clusters are untouched by the retarget.
	var remote uuid.UUID
	if err := s.pool.QueryRow(t.Context(), `SELECT id FROM clusters WHERE NOT is_local LIMIT 1`).Scan(&remote); err != nil {
		t.Fatal(err)
	}
	if n, err := q.RetargetLocalDeliverySystemAssignment(t.Context(), remote); err != nil || n != 0 {
		t.Fatalf("remote retarget rows=%d err=%v", n, err)
	}
}
