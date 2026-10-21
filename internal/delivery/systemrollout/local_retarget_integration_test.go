//go:build integration

package systemrollout

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/status"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"

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

// Authentication is outside this ordering test. All inventory coalescing,
// assignment writes and release transitions below use real PostgreSQL queries.
type localStatusTx struct{ *sqlc.Queries }

func (tx localStatusTx) FenceDeliveryAgentSession(_ context.Context, p sqlc.FenceDeliveryAgentSessionParams) (uuid.UUID, error) {
	return p.ClusterID, nil
}

type localStatusRunner struct{ pool *pgxpool.Pool }

func (r localStatusRunner) Run(ctx context.Context, work func(status.Transaction) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := work(localStatusTx{sqlc.New(tx)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func TestPostgresCoalescedInventoryAfterPromotionRetargetsLocal(t *testing.T) {
	t.Parallel()
	s, request := systemRolloutFixture(t)
	if _, err := s.pool.Exec(t.Context(), `CREATE TABLE delivery_controller_inventory (LIKE public.delivery_controller_inventory INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	local := uuid.New()
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO clusters VALUES($1,NULL,true,'ready')`, local); err != nil {
		t.Fatal(err)
	}
	payload := protocol.DeliveryStatusV2{ProtocolVersion: protocol.DeliveryProtocolVersion, ClusterID: local.String(), SessionSequence: 1, ControllerInventory: protocol.DeliveryControllerInventory{AgentVersion: "1.0.0", FluxVersion: fluxdistribution.Version(), DistributionDigest: "sha256:" + strings.Repeat("b", 64), KubernetesVersion: "v1.35.0", Ready: false}}
	payload.StatusDigest = payload.SemanticDigest()
	ingester := status.NewIngester(localStatusRunner{s.pool})
	connection := uuid.New()
	// The upgraded local agent reports before promotion. Its inventory is
	// persisted while the previous release remains the released release.
	if err := ingester.Ingest(t.Context(), local, connection, "session", payload); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE delivery_system_releases SET state=CASE WHEN id=$1 THEN 'released' ELSE 'retired' END`, request.ReleaseID); err != nil {
		t.Fatal(err)
	}
	// Only sequence changes: the real AcceptDeliveryStatusInventory SQL must
	// report status_changed=false, reproducing the release/observation race.
	payload.SessionSequence++
	if err := ingester.Ingest(t.Context(), local, connection, "session", payload); err != nil {
		t.Fatal(err)
	}
	var desired uuid.UUID
	var generation, semanticSequence, sequence int64
	if err := s.pool.QueryRow(t.Context(), `SELECT a.desired_release_id,a.generation,i.semantic_sequence,i.agent_sequence FROM delivery_system_cluster_assignments a JOIN delivery_controller_inventory i ON i.cluster_id=a.cluster_id WHERE a.cluster_id=$1`, local).Scan(&desired, &generation, &semanticSequence, &sequence); err != nil {
		t.Fatal(err)
	}
	if desired != request.ReleaseID || generation < 2 || semanticSequence != 1 || sequence != 2 {
		t.Fatalf("coalesced promotion: desired=%s generation=%d semantic=%d sequence=%d", desired, generation, semanticSequence, sequence)
	}
}
