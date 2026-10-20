package catalogapp

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
)

type catalogPreviewFunc func(context.Context, uuid.UUID) (rollout.PlanningSnapshot, placement.Result, error)

func (f catalogPreviewFunc) Preview(ctx context.Context, target uuid.UUID) (rollout.PlanningSnapshot, placement.Result, error) {
	return f(ctx, target)
}

type catalogPlanFunc func(context.Context, rollout.CreateRequest) (rollout.FrozenRollout, error)

func (f catalogPlanFunc) Create(ctx context.Context, request rollout.CreateRequest) (rollout.FrozenRollout, error) {
	return f(ctx, request)
}

func TestPostgresCatalogInstallRetryAndUpgradeKeepActorAndRevisionOwnership(t *testing.T) {
	s, request := catalogAssetsFixture(t)
	request.ActorID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO installed_charts(id,cluster_id,release_name,status)
		VALUES($1,$2,'synthetic','pending_install')`, request.InstallationID, request.ClusterID); err != nil {
		t.Fatal(err)
	}
	var generation int64
	s.previewer = catalogPreviewFunc(func(ctx context.Context, target uuid.UUID) (rollout.PlanningSnapshot, placement.Result, error) {
		if err := s.pool.QueryRow(ctx, `SELECT generation FROM delivery_targets WHERE id=$1`, target).Scan(&generation); err != nil {
			t.Fatal(err)
		}
		if generation < 1 {
			t.Fatal("invalid committed target generation")
		}
		return rollout.PlanningSnapshot{TargetGeneration: uint64(generation)}, placement.Result{SelectedCount: 1}, nil
	})
	planned := 0
	s.planner = catalogPlanFunc(func(_ context.Context, r rollout.CreateRequest) (rollout.FrozenRollout, error) {
		planned++
		if r.Audit.IsZero() || r.Audit.Event.UserID != request.ActorID || r.Audit.Event.Detail["project_id"] != request.ProjectID.String() || r.ExpectedTargetGeneration != uint64(generation) {
			t.Fatal("install/upgrade lost actor, tenant, mandatory audit or committed generation")
		}
		return rollout.FrozenRollout{ID: uuid.New()}, nil
	})
	first, err := s.Install(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Install(t.Context(), request)
	if err != nil || second.TargetID != first.TargetID || generation != 1 {
		t.Fatalf("install retry changed target: generation=%d error=%v", generation, err)
	}
	var revision int32
	if err := s.pool.QueryRow(t.Context(), `SELECT revision FROM installed_charts WHERE id=$1`, request.InstallationID).Scan(&revision); err != nil || revision != 1 {
		t.Fatal("install retry advanced compatibility revision")
	}
	upgrade := request
	upgrade.ProjectID, upgrade.ChartVersionID, upgrade.ChartVersion = uuid.Nil, uuid.New(), "2.0.0"
	third, err := s.Upgrade(t.Context(), upgrade)
	if err != nil || third.TargetID != first.TargetID || generation != 2 || planned != 3 {
		t.Fatalf("upgrade did not resolve existing ownership or stage exactly once: generation=%d planned=%d error=%v", generation, planned, err)
	}
	var target uuid.UUID
	var status string
	if err := s.pool.QueryRow(t.Context(), `SELECT request_id,status,revision FROM installed_charts WHERE id=$1`, request.InstallationID).Scan(&target, &status, &revision); err != nil || target != first.TargetID || status != "upgrading" || revision != 2 {
		t.Fatal("upgrade link changed target ownership or monotonic revision")
	}
}

func TestPostgresCatalogRollbackStagesBeforePlanningAndKeepsRevisionHistory(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "commit_before_preview"
		if reject {
			name = "late_failure_rolls_back_before_preview"
		}
		t.Run(name, func(t *testing.T) {
			s, request := catalogAssetsFixture(t)
			if _, err := s.pool.Exec(t.Context(), `CREATE TABLE delivery_rollouts (LIKE public.delivery_rollouts INCLUDING ALL)`); err != nil {
				t.Fatal(err)
			}
			first, _, err := s.ensureAssets(t.Context(), request, false)
			if err != nil {
				t.Fatal(err)
			}
			request.ChartVersionID, request.ChartVersion, request.ValuesYAML = uuid.New(), "2.0.0", "replicas: 2"
			second, _, err := s.ensureAssets(t.Context(), request, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(t.Context(), `INSERT INTO installed_charts(id,cluster_id,release_name,request_id,status,revision,values_override)
				VALUES($1,$2,'synthetic',$3,'installed',2,'{"replicas":2}')`, request.InstallationID, request.ClusterID, first.TargetID); err != nil {
				t.Fatal(err)
			}
			strategy, digest := validStatusStrategy(t), "sha256:"+strings.Repeat("a", 64)
			for n, version := range []uuid.UUID{first.BundleVersionID, second.BundleVersionID} {
				if _, err := s.pool.Exec(t.Context(), `INSERT INTO delivery_rollouts(id,target_id,target_generation,to_bundle_version_id,
					from_bundle_version_id,placement_digest,strategy_digest,request_digest,plan_digest,strategy,frozen_plan,state,idempotency_key)
					VALUES($1,$2,$3,$4,$5,$6,$6,$6,$6,$7::jsonb,jsonb_build_object('strategy',$7::jsonb),'succeeded',$8)`,
					uuid.New(), first.TargetID, int64(n+1), version, pgtype.UUID{Bytes: first.BundleVersionID, Valid: n == 1}, digest, strategy, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			}
			revisions, err := s.Revisions(t.Context(), request.InstallationID)
			if err != nil || len(revisions) != 2 || revisions[0].Revision != 1 || revisions[0].Status != "superseded" || revisions[1].Revision != 2 || revisions[1].Status != "deployed" {
				t.Fatalf("immutable revision projection changed: %+v error=%v", revisions, err)
			}
			actor, plannedID := pgtype.UUID{Bytes: uuid.New(), Valid: true}, uuid.New()
			previewed, planned := false, false
			s.previewer = catalogPreviewFunc(func(ctx context.Context, target uuid.UUID) (rollout.PlanningSnapshot, placement.Result, error) {
				previewed = true
				var version uuid.UUID
				var generation int64
				// This pool read uses a separate session: uncommitted staging is not visible.
				if err := s.pool.QueryRow(ctx, `SELECT bundle_version_id,generation FROM delivery_targets WHERE id=$1`, target).Scan(&version, &generation); err != nil || version != first.BundleVersionID || generation != 3 {
					t.Fatalf("planner preview saw uncommitted/wrong staging: generation=%d error=%v", generation, err)
				}
				return rollout.PlanningSnapshot{ProjectID: request.ProjectID, TargetGeneration: 3}, placement.Result{SelectedCount: 1}, nil
			})
			s.planner = catalogPlanFunc(func(_ context.Context, r rollout.CreateRequest) (rollout.FrozenRollout, error) {
				planned = true
				if r.TargetID != first.TargetID || r.ExpectedTargetGeneration != 3 || r.IdempotencyKey != "catalog-rollback:synthetic" || r.Audit.IsZero() || r.Audit.Event.UserID != actor || r.Audit.Event.Detail["project_id"] != request.ProjectID.String() {
					t.Fatal("post-commit planner lost fenced generation, actor, tenant or mandatory audit intent")
				}
				return rollout.FrozenRollout{ID: plannedID}, nil
			})
			if reject {
				if _, err := s.pool.Exec(t.Context(), `CREATE FUNCTION reject_rollback_installation() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'synthetic failure after target stage'; END $$;
				CREATE TRIGGER reject_rollback_installation BEFORE UPDATE ON installed_charts FOR EACH ROW EXECUTE FUNCTION reject_rollback_installation()`); err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.Rollback(t.Context(), request.InstallationID, 1, actor, "synthetic")
			if (err != nil) != reject || previewed == reject || planned == reject || (!reject && result.RolloutID != plannedID) {
				t.Fatalf("rollback boundary changed: previewed=%t planned=%t error=%v", previewed, planned, err)
			}
			var version uuid.UUID
			var generation int64
			var status string
			var revision int32
			if err := s.pool.QueryRow(t.Context(), `SELECT t.bundle_version_id,t.generation,ic.status,ic.revision FROM delivery_targets t
				JOIN installed_charts ic ON ic.request_id=t.id WHERE t.id=$1`, first.TargetID).Scan(&version, &generation, &status, &revision); err != nil {
				t.Fatal(err)
			}
			if reject {
				if version != second.BundleVersionID || generation != 2 || status != "installed" || revision != 2 {
					t.Fatal("late failure left partial rollback staging")
				}
			} else if version != first.BundleVersionID || generation != 3 || status != "rolling_back" || revision != 3 {
				t.Fatal("rollback did not retain monotonic revision and immutable selected version")
			}
		})
	}
}
