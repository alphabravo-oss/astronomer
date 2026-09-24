package builtin

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
)

type scanningRollouts struct {
	pool     *pgxpool.Pool
	requests []rollout.CreateRequest
}

func (s *scanningRollouts) Preview(context.Context, uuid.UUID) (rollout.PlanningSnapshot, placement.Result, error) {
	return rollout.PlanningSnapshot{TargetGeneration: 1}, placement.Result{SelectedCount: 1}, nil
}

func (s *scanningRollouts) Create(ctx context.Context, req rollout.CreateRequest) (rollout.FrozenRollout, error) {
	s.requests = append(s.requests, req)
	_, err := s.pool.Exec(ctx, `INSERT INTO delivery_rollouts(id,target_id,state,created_at) VALUES ($1,$2,'progressing',now())`, uuid.New(), req.TargetID)
	return rollout.FrozenRollout{}, err
}

type scanningRegistration struct{ starts, successes, failures int }

func (s *scanningRegistration) OnDeliveryApplyStart(context.Context, uuid.UUID) error {
	s.starts++
	return nil
}
func (s *scanningRegistration) OnDeliveryApplySuccess(context.Context, uuid.UUID) error {
	s.successes++
	return nil
}
func (s *scanningRegistration) OnDeliveryApplyFailure(context.Context, uuid.UUID, string) error {
	s.failures++
	return nil
}

func TestAutomaticScanningPostgres(t *testing.T) {
	dsn := os.Getenv("BUILTIN_PROVISIONER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BUILTIN_PROVISIONER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := openBuiltinProvisionerTestPool(t, ctx, dsn)
	defer pool.Close()
	_, err := pool.Exec(ctx, `
CREATE TABLE clusters(id uuid PRIMARY KEY, registration_phase text, is_local boolean, install_baseline boolean, annotations jsonb, decommissioned_at timestamptz);
CREATE TABLE delivery_controller_inventory(cluster_id uuid, ready boolean, compatibility_status text);
CREATE TABLE cluster_registration_steps(cluster_id uuid, step_name text, created_at timestamptz, step_order integer);
CREATE TABLE delivery_rollouts(id uuid PRIMARY KEY, target_id uuid, state text, created_at timestamptz);
CREATE TABLE cluster_deployments(target_id uuid, cluster_id uuid, phase text, last_error_code text);
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, phase                      string
		baseline, local, ready, disabled bool
		want                             int
	}{
		{"existing remote with baseline off", "ready", false, false, true, false, 1},
		{"existing remote with baseline on", "ready", true, false, true, false, 1},
		{"explicit opt out", "ready", true, false, true, true, 0},
		{"unready inventory", "ready", false, false, false, false, 0},
		{"local cluster", "ready", true, true, true, false, 0},
		{"not enrolled", "awaiting_agent", true, false, true, false, 0},
		{"failed registration", "failed", true, false, true, false, 0},
		{"new baseline", "connected", true, false, true, false, 3},
		{"new metrics only", "connected", true, false, true, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			annotations := `{}`
			if tc.disabled {
				annotations = `{"astronomer.io/image-scanning":"disabled"}`
			}
			if _, err := pool.Exec(ctx, `INSERT INTO clusters VALUES ($1,$2,$3,$4,$5,NULL)`, id, tc.phase, tc.local, tc.baseline, annotations); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO delivery_controller_inventory VALUES ($1,$2,'compatible')`, id, tc.ready); err != nil {
				t.Fatal(err)
			}
			plans, registration := &scanningRollouts{pool: pool}, &scanningRegistration{}
			p := &Provisioner{pool: pool, previewer: plans, planner: plans, registration: registration}
			for i := 0; i < 2; i++ {
				if err := p.Reconcile(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			if len(plans.requests) != tc.want {
				t.Fatalf("created %d rollouts, want %d", len(plans.requests), tc.want)
			}
			for _, req := range plans.requests {
				if req.Audit.IsZero() {
					t.Fatal("automatic rollout missing audit intent")
				}
				if tc.phase == "ready" && req.Audit.Event.Detail["builtin_slug"] != "trivy-operator" {
					t.Fatalf("unexpected automatic component: %+v", req.Audit.Event.Detail)
				}
			}
			if tc.phase == "ready" {
				if _, err := pool.Exec(ctx, `UPDATE delivery_rollouts SET state='failed' WHERE target_id IN (SELECT id FROM delivery_targets WHERE project_id=$1)`, stableID("project", id.String())); err != nil {
					t.Fatal(err)
				}
				if err := p.Reconcile(ctx, id); err != nil {
					t.Fatal(err)
				}
				if registration.starts+registration.successes+registration.failures != 0 {
					t.Fatal("automatic scanning reopened terminal registration")
				}
			}
		})
	}
}
