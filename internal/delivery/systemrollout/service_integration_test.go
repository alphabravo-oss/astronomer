//go:build integration

package systemrollout

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"

	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func systemRolloutFixture(t *testing.T) (*Service, StartRequest) {
	t.Helper()
	dsn := testdb.DSN(t)
	admin, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal("cannot connect to owned system rollout database")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := admin.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	schema := "w08_system_rollout_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid owned system rollout configuration")
	}
	config.MaxConns = 6
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal("cannot create owned system rollout pool")
	}
	t.Cleanup(pool.Close)
	for _, table := range []string{"delivery_system_releases", "delivery_system_rollouts", "delivery_system_cluster_assignments", "delivery_system_events", "audit_outbox"} {
		if _, err := pool.Exec(t.Context(), "CREATE TABLE "+pgx.Identifier{table}.Sanitize()+" (LIKE public."+pgx.Identifier{table}.Sanitize()+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `CREATE TABLE clusters(id uuid PRIMARY KEY,decommissioned_at timestamptz,is_local boolean NOT NULL,registration_phase text NOT NULL); CREATE TABLE cluster_agent_tokens(cluster_id uuid,revoked_at timestamptz)`); err != nil {
		t.Fatal(err)
	}
	previous, next := uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{previous, next} {
		digest := "sha256:" + strings.Repeat(string(rune('a'+i)), 64)
		state := "released"
		if i == 1 {
			state = "draft"
		}
		if _, err := pool.Exec(t.Context(), `INSERT INTO delivery_system_releases(id,version,artifact_url,artifact_digest,distribution_digest,agent_version,agent_image,minimum_kubernetes,maximum_kubernetes,crd_storage_version,verification_policy,spec_digest,state) VALUES($1,$2,'https://example.test/release',$3,$3,'1.0.0','example.test/agent@digest','1.28.0','1.99.0','v1','{}',$3,$4)`, id, id.String(), digest, state); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		id := uuid.New()
		if _, err := pool.Exec(t.Context(), `INSERT INTO clusters VALUES($1,NULL,false,'ready');`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `INSERT INTO cluster_agent_tokens VALUES($1,NULL)`, id); err != nil {
			t.Fatal(err)
		}
	}
	strategy := model.RolloutStrategy{Type: model.StrategyRolling, MaxConcurrent: 1, MaxUnavailable: model.Amount{Type: model.AmountCount, Value: 1}, FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1}, ProgressDeadline: model.Duration(time.Hour), OnFailure: model.FailureRollback}
	request := StartRequest{ReleaseID: next, Strategy: strategy, IdempotencyKey: uuid.NewString(), ActorID: uuid.New(), Audit: systemAudit("start")}
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	return s, request
}
func systemAudit(action string) audit.Intent {
	return audit.Intent{Event: audit.Event{Action: "delivery.system_rollout." + action, ResourceType: "delivery_system_rollout", Source: "test", Detail: map[string]any{}}, DedupeKey: uuid.NewString()}
}
func countRows(t *testing.T, s *Service, table string, want int) {
	t.Helper()
	var count int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil || count != want {
		t.Fatalf("%s count=%d want=%d error=%v", table, count, want, err)
	}
}

func TestPostgresSystemRolloutStartIdempotencyAndEligibility(t *testing.T) {
	t.Parallel()
	s, request := systemRolloutFixture(t)
	for _, row := range []struct {
		local          bool
		phase          string
		decommissioned bool
		token          bool
	}{{true, "ready", false, true}, {false, "pending", false, true}, {false, "ready", true, true}, {false, "ready", false, false}} {
		id := uuid.New()
		var gone any
		if row.decommissioned {
			gone = time.Now()
		}
		if _, err := s.pool.Exec(t.Context(), `INSERT INTO clusters VALUES($1,$2,$3,$4)`, id, gone, row.local, row.phase); err != nil {
			t.Fatal(err)
		}
		if row.token {
			if _, err := s.pool.Exec(t.Context(), `INSERT INTO cluster_agent_tokens VALUES($1,NULL)`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	first, err := s.Start(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Start(t.Context(), request)
	if err != nil || first.ID != second.ID || second.TotalClusters != 3 || second.PreviousReleaseID == nil {
		t.Fatalf("idempotent snapshot changed: error=%v", err)
	}
	countRows(t, s, "delivery_system_rollouts", 1)
	countRows(t, s, "delivery_system_cluster_assignments", 3)
	countRows(t, s, "delivery_system_events", 1)
	countRows(t, s, "audit_outbox", 1)
	request.IdempotencyKey = uuid.NewString()
	if _, err := s.Start(t.Context(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("parallel active rollout error=%v", err)
	}
	if _, err := s.Get(t.Context(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing row error=%v", err)
	}
	assignments, err := s.Assignments(t.Context(), first.ID)
	if err != nil || len(assignments) != 3 {
		t.Fatalf("assignments error=%v", err)
	}
	for _, item := range assignments {
		if item.Generation != 1 || item.Phase != "pending" || item.LastObservedAt != nil {
			t.Fatal("new assignment defaults changed")
		}
	}
}

//nolint:tparallel // subtests run sequentially against one shared per-test database fixture
func TestPostgresSystemRolloutAuditFailureRollsBackStartAndAction(t *testing.T) {
	t.Parallel()
	for _, action := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "action"}[action], func(t *testing.T) {
			s, request := systemRolloutFixture(t)
			var original View
			var err error
			if action {
				original, err = s.Start(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.pool.Exec(t.Context(), `CREATE FUNCTION reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit write failure'; END $$; CREATE TRIGGER reject_audit BEFORE INSERT OR UPDATE ON audit_outbox FOR EACH ROW EXECUTE FUNCTION reject_audit()`); err != nil {
				t.Fatal(err)
			}
			if action {
				_, err = s.Act(t.Context(), original.ID, original.FencingGeneration, ActionApprove, request.ActorID, "approve", systemAudit("approve"))
			} else {
				_, err = s.Start(t.Context(), request)
			}
			if !errors.Is(err, audit.ErrOutboxUnavailable) {
				t.Fatalf("mandatory audit failure=%v", err)
			}
			if action {
				current, err := s.Get(t.Context(), original.ID)
				if err != nil || current.State != original.State || current.FencingGeneration != original.FencingGeneration {
					t.Fatal("failed audit committed action/fence")
				}
				countRows(t, s, "delivery_system_events", 1)
				countRows(t, s, "audit_outbox", 1)
			} else {
				for _, table := range []string{"delivery_system_rollouts", "delivery_system_cluster_assignments", "delivery_system_events", "audit_outbox"} {
					countRows(t, s, table, 0)
				}
			}
		})
	}
}
func TestPostgresSystemRolloutConcurrentFenceHasOneWinner(t *testing.T) {
	t.Parallel()
	s, request := systemRolloutFixture(t)
	first, err := s.Start(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	ready := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			_, err := s.Act(t.Context(), first.ID, first.FencingGeneration, ActionApprove, request.ActorID, "approve", systemAudit("approve"))
			errs <- err
		}()
	}
	close(ready)
	wg.Wait()
	close(errs)
	wins := 0
	for err := range errs {
		if err == nil {
			wins++
			continue
		}
		var pgErr interface{ SQLState() string }
		if !errors.Is(err, ErrConflict) && (!errors.As(err, &pgErr) || pgErr.SQLState() != "40001") {
			t.Fatalf("concurrent action error=%v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("winners=%d", wins)
	}
	current, err := s.Get(t.Context(), first.ID)
	if err != nil || current.State != "queued" || current.FencingGeneration != 2 {
		t.Fatal("CAS fence changed")
	}
	countRows(t, s, "delivery_system_events", 2)
	countRows(t, s, "audit_outbox", 2)
}

//nolint:tparallel // subtests run sequentially against one shared per-test database fixture
func TestPostgresSystemRolloutCohortPromotionAndRollback(t *testing.T) {
	t.Parallel()
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "promotion", true: "partial rollback"}[rollback], func(t *testing.T) {
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
			assignments, err := s.Assignments(t.Context(), view.ID)
			if err != nil {
				t.Fatal(err)
			}
			released := 0
			for _, item := range assignments {
				if item.Phase == "released" {
					released++
				}
			}
			if released != 1 {
				t.Fatalf("released=%d exceeds concurrency", released)
			}
			if rollback {
				view, err = s.Get(t.Context(), view.ID)
				if err != nil {
					t.Fatal(err)
				}
				view, err = s.Act(t.Context(), view.ID, view.FencingGeneration, ActionRollback, request.ActorID, "rollback", systemAudit("rollback"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.pool.Exec(t.Context(), `UPDATE delivery_system_cluster_assignments SET phase='rolled_back' WHERE rollout_id=$1 AND phase='rolling_back'`, view.ID); err != nil {
					t.Fatal(err)
				}
				if err := s.ReconcileOne(t.Context(), view.ID); err != nil {
					t.Fatal(err)
				}
				view, err = s.Get(t.Context(), view.ID)
				if err != nil || view.State != "rolled_back" {
					t.Fatal("partial rollback waited for pending cohorts")
				}
				return
			}
			for i := 0; i < 3; i++ {
				if _, err := s.pool.Exec(t.Context(), `UPDATE delivery_system_cluster_assignments SET phase='ready',ready_at=now()-interval '1 hour' WHERE rollout_id=$1 AND phase='released'`, view.ID); err != nil {
					t.Fatal(err)
				}
				if err := s.ReconcileOne(t.Context(), view.ID); err != nil {
					t.Fatal(err)
				}
			}
			view, err = s.Get(t.Context(), view.ID)
			if err != nil || view.State != "succeeded" || view.ReadyClusters != 3 || view.CompletedAt == nil {
				t.Fatalf("promotion state=%s error=%v", view.State, err)
			}
			var releaseState string
			if err := s.pool.QueryRow(t.Context(), `SELECT state FROM delivery_system_releases WHERE id=$1`, request.ReleaseID).Scan(&releaseState); err != nil || releaseState != "released" {
				t.Fatal("rollout and release promotion did not commit together")
			}
		})
	}
}

func TestPostgresSystemRolloutPromotionConflictRollsBackRetirement(t *testing.T) {
	t.Parallel()
	s, request := systemRolloutFixture(t)
	request.Strategy.MaxConcurrent = 3
	request.Strategy.MaxUnavailable.Value = 3
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
	if _, err := s.pool.Exec(t.Context(), `UPDATE delivery_system_releases SET state='revoked' WHERE id=$1;`, request.ReleaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE delivery_system_cluster_assignments SET phase='ready',ready_at=now()-interval '1 hour' WHERE rollout_id=$1`, view.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(t.Context(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileOne(t.Context(), view.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("zero-row promotion error=%v", err)
	}
	after, err := s.Get(t.Context(), view.ID)
	if err != nil || after.State != before.State || after.FencingGeneration != before.FencingGeneration || after.ReadyClusters != before.ReadyClusters {
		t.Fatal("promotion conflict committed rollout counters/fence")
	}
	var previousState string
	if err := s.pool.QueryRow(t.Context(), `SELECT state FROM delivery_system_releases WHERE id=$1`, *view.PreviousReleaseID).Scan(&previousState); err != nil || previousState != "released" {
		t.Fatal("zero-row promotion committed prior release retirement")
	}
	countRows(t, s, "delivery_system_events", 3)
}
