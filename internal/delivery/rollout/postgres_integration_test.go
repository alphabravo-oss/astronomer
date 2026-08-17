package rollout

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// This opt-in test exercises the production transaction adapter and rollout
// claim fencing against real PostgreSQL. CI and release rehearsals provide a
// disposable database; the test owns and drops an isolated schema.
func TestPostgresPlanningTransactionAndHAFencing(t *testing.T) {
	dsn := os.Getenv("DELIVERY_ROLLOUT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DELIVERY_ROLLOUT_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	schema := "delivery_rollout_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
	}()

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 12
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, rolloutIntegrationDDL); err != nil {
		t.Fatal(err)
	}

	snapshot, preview := testSnapshot(t, 2)
	plan, err := mustPlanner(t, newMemoryPlanningStore(snapshot)).Create(ctx, testCreateRequest(preview, testStrategy("rolling", 1)))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewPostgresPlanningStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("force transaction rollback")
	err = store.InTransaction(ctx, func(tx PlanningTransaction) error {
		if err := tx.InsertRollout(ctx, plan); err != nil {
			return err
		}
		if err := tx.AppendRolloutCreated(ctx, plan); err != nil {
			return err
		}
		if err := tx.EnqueueRollout(ctx, plan.ID); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("rollback error = %v", err)
	}
	assertRolloutIntegrationCounts(t, ctx, pool, 0, 0, 0, 0)

	if err := store.InTransaction(ctx, func(tx PlanningTransaction) error {
		if err := tx.InsertRollout(ctx, plan); err != nil {
			return err
		}
		if err := tx.AppendRolloutCreated(ctx, plan); err != nil {
			return err
		}
		return tx.EnqueueRollout(ctx, plan.ID)
	}); err != nil {
		t.Fatal(err)
	}
	assertRolloutIntegrationCounts(t, ctx, pool, 1, 2, 1, 1)
	var totalClusters int
	if err := pool.QueryRow(ctx, `SELECT total_clusters FROM delivery_rollouts WHERE id=$1`, plan.ID).Scan(&totalClusters); err != nil {
		t.Fatal(err)
	}
	if totalClusters != len(plan.Clusters) {
		t.Fatalf("persisted total_clusters = %d, want %d", totalClusters, len(plan.Clusters))
	}

	if err := store.InTransaction(ctx, func(tx PlanningTransaction) error {
		stored, found, err := tx.FindByIdempotency(ctx, plan.TargetID, plan.IdempotencyKey)
		if err != nil {
			return err
		}
		if !found || stored.ID != plan.ID || stored.PlanDigest != plan.PlanDigest {
			return fmt.Errorf("idempotency lookup returned found=%v plan=%s digest=%s", found, stored.ID, stored.PlanDigest)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	const replicas = 8
	var group sync.WaitGroup
	results := make(chan error, replicas)
	winners := make(chan sqlc.DeliveryRollout, 1)
	for index := range replicas {
		group.Add(1)
		go func() {
			defer group.Done()
			row, claimErr := sqlc.New(pool).ClaimDeliveryRollout(ctx, sqlc.ClaimDeliveryRolloutParams{
				LeaseOwner: fmt.Sprintf("replica-%d", index), LeaseDuration: interval(10 * time.Second), ID: plan.ID,
			})
			if claimErr == nil {
				winners <- row
			}
			results <- claimErr
		}()
	}
	group.Wait()
	close(results)
	close(winners)
	winnerCount := 0
	for claimErr := range results {
		switch {
		case claimErr == nil:
			winnerCount++
		case errors.Is(claimErr, pgx.ErrNoRows):
		default:
			t.Fatalf("claim error = %v", claimErr)
		}
	}
	if winnerCount != 1 {
		t.Fatalf("HA claim winners = %d, want one", winnerCount)
	}
	winner := <-winners
	queries := sqlc.New(pool)
	if _, err := queries.ApplyDeliveryRolloutTransitionCAS(ctx, sqlc.ApplyDeliveryRolloutTransitionCASParams{
		ToState: "queued", DecisionDigest: plan.PlanDigest.String(), ID: plan.ID, FromState: "resolving",
		ExpectedLeaseOwner: winner.LeaseOwner, ExpectedFence: winner.FencingGeneration - 1,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale fence transition = %v, want no rows", err)
	}
	transitioned, err := queries.ApplyDeliveryRolloutTransitionCAS(ctx, sqlc.ApplyDeliveryRolloutTransitionCASParams{
		ToState: "queued", DecisionDigest: plan.PlanDigest.String(), ID: plan.ID, FromState: "resolving",
		ExpectedLeaseOwner: winner.LeaseOwner, ExpectedFence: winner.FencingGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if transitioned.LeaseOwner != "" || transitioned.FencingGeneration != winner.FencingGeneration {
		t.Fatalf("transition did not release lease without changing fence: %+v", transitioned)
	}

	second, err := queries.ClaimDeliveryRollout(ctx, sqlc.ClaimDeliveryRolloutParams{
		LeaseOwner: "replacement", LeaseDuration: interval(10 * time.Second), ID: plan.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.FencingGeneration != winner.FencingGeneration+1 {
		t.Fatalf("replacement fence = %d, want %d", second.FencingGeneration, winner.FencingGeneration+1)
	}
	if _, err := queries.ClaimDeliveryRollout(ctx, sqlc.ClaimDeliveryRolloutParams{
		LeaseOwner: "blocked", LeaseDuration: interval(10 * time.Second), ID: plan.ID,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("live lease was stolen: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_rollouts SET lease_expires_at = now() - interval '1 second' WHERE id=$1`, plan.ID); err != nil {
		t.Fatal(err)
	}
	takeover, err := queries.ClaimDeliveryRollout(ctx, sqlc.ClaimDeliveryRolloutParams{
		LeaseOwner: "takeover", LeaseDuration: interval(10 * time.Second), ID: plan.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if takeover.FencingGeneration != second.FencingGeneration+1 {
		t.Fatalf("takeover fence = %d, want %d", takeover.FencingGeneration, second.FencingGeneration+1)
	}
}

func assertRolloutIntegrationCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rollouts, clusters, events, tasks int) {
	t.Helper()
	for table, want := range map[string]int{
		"delivery_rollouts": rollouts, "delivery_rollout_clusters": clusters,
		"delivery_rollout_events": events, "task_outbox": tasks,
	} {
		var got int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count = %d, want %d", table, got, want)
		}
	}
}

const rolloutIntegrationDDL = `
CREATE TABLE delivery_rollouts (
 id uuid PRIMARY KEY, target_id uuid NOT NULL, target_generation bigint NOT NULL,
 from_bundle_version_id uuid, to_bundle_version_id uuid NOT NULL,
 placement_digest varchar(80) NOT NULL, placement_snapshot jsonb NOT NULL DEFAULT '{}',
 strategy jsonb NOT NULL DEFAULT '{}', strategy_digest varchar(80) NOT NULL,
 approval_policy jsonb NOT NULL DEFAULT '{}', request_digest varchar(80) NOT NULL,
 plan_digest varchar(80) NOT NULL, frozen_plan jsonb NOT NULL, state varchar(24) NOT NULL,
 fencing_generation bigint NOT NULL DEFAULT 1, lease_owner varchar(253) NOT NULL DEFAULT '',
 lease_expires_at timestamptz, last_decision_digest varchar(80) NOT NULL DEFAULT '',
 idempotency_key varchar(128) NOT NULL, total_clusters integer NOT NULL DEFAULT 0,
 ready_clusters integer NOT NULL DEFAULT 0, failed_clusters integer NOT NULL DEFAULT 0,
 blocked_clusters integer NOT NULL DEFAULT 0, released_clusters integer NOT NULL DEFAULT 0,
 progress_deadline timestamptz, started_at timestamptz, completed_at timestamptz,
 last_error_code varchar(96) NOT NULL DEFAULT '', initiated_by uuid,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(target_id, idempotency_key)
);
CREATE TABLE delivery_rollout_clusters (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), rollout_id uuid NOT NULL,
 cluster_id uuid NOT NULL, cohort integer NOT NULL DEFAULT 0, release_order integer NOT NULL DEFAULT 0,
 previous_bundle_version_id uuid, desired_bundle_version_id uuid NOT NULL,
 desired_spec_digest varchar(80) NOT NULL, state varchar(24) NOT NULL DEFAULT 'pending',
 assignment_action varchar(16) NOT NULL DEFAULT 'apply', attempt integer NOT NULL DEFAULT 0,
 fence bigint NOT NULL DEFAULT 0, released_at timestamptz, acknowledged_at timestamptz,
 ready_at timestamptz, completed_at timestamptz,
 deadline timestamptz, last_error_code varchar(96) NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(rollout_id, cluster_id)
);
CREATE TABLE delivery_rollout_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), rollout_id uuid NOT NULL, cluster_id uuid,
 decision_digest varchar(80) NOT NULL, event_type varchar(48) NOT NULL,
 from_state varchar(24) NOT NULL DEFAULT '', to_state varchar(24) NOT NULL DEFAULT '',
 reason_code varchar(96) NOT NULL DEFAULT '', fence bigint NOT NULL,
 occurred_at timestamptz NOT NULL DEFAULT now(), created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE task_outbox (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), dedupe_key text, task_type text NOT NULL,
 payload bytea NOT NULL DEFAULT ''::bytea, queue_name text NOT NULL DEFAULT 'default',
 max_retry integer NOT NULL DEFAULT 3, timeout_seconds integer NOT NULL DEFAULT 0,
 unique_seconds integer NOT NULL DEFAULT 0, max_delivery_attempts integer NOT NULL DEFAULT 20,
 status text NOT NULL DEFAULT 'pending', attempt_count integer NOT NULL DEFAULT 0,
 next_attempt_at timestamptz NOT NULL DEFAULT now(), locked_until timestamptz, delivered_at timestamptz,
 last_error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX task_outbox_dedupe_key_unique ON task_outbox(dedupe_key) WHERE dedupe_key IS NOT NULL;
`
