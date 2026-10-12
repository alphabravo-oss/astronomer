package catalogapp

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPostgresCatalogUninstallCommitsFencedTargetAndInstallationTogether(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "commit"
		if reject {
			name = "late_failure_rolls_back"
		}
		t.Run(name, func(t *testing.T) {
			s, request := catalogAssetsFixture(t)
			assets, _, err := s.ensureAssets(t.Context(), request, false)
			if err != nil {
				t.Fatal(err)
			}
			actor := pgtype.UUID{Bytes: uuid.New(), Valid: true}
			if _, err := s.pool.Exec(t.Context(), `INSERT INTO installed_charts(id,cluster_id,release_name,request_id,status)
				VALUES($1,$2,'synthetic',$3,'installed')`, request.InstallationID, request.ClusterID, assets.TargetID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(t.Context(), `INSERT INTO cluster_deployments(target_id,cluster_id,phase,desired_generation)
				VALUES($1,$2,'ready',1)`, assets.TargetID, request.ClusterID); err != nil {
				t.Fatal(err)
			}
			if reject {
				if _, err := s.pool.Exec(t.Context(), `CREATE FUNCTION reject_installation_change() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'synthetic failure after target CAS'; END $$;
				CREATE TRIGGER reject_installation_change BEFORE UPDATE ON installed_charts FOR EACH ROW EXECUTE FUNCTION reject_installation_change()`); err != nil {
					t.Fatal(err)
				}
			}
			err = s.Uninstall(t.Context(), request.InstallationID, actor)
			if (err != nil) != reject {
				t.Fatalf("uninstall error=%v reject=%t", err, reject)
			}
			var state, status, action, phase string
			var generation, resourceVersion, desired int64
			var updatedBy pgtype.UUID
			if err := s.pool.QueryRow(t.Context(), `SELECT t.deletion_state,t.generation,t.resource_version,t.updated_by,
				ic.status,d.action,d.phase,d.desired_generation FROM delivery_targets t
				JOIN installed_charts ic ON ic.request_id=t.id JOIN cluster_deployments d ON d.target_id=t.id
				WHERE t.id=$1`, assets.TargetID).Scan(&state, &generation, &resourceVersion, &updatedBy, &status, &action, &phase, &desired); err != nil {
				t.Fatal(err)
			}
			if reject {
				if state != "active" || generation != 1 || resourceVersion != 1 || updatedBy.Valid || status != "installed" || action != "apply" || phase != "ready" || desired != 1 {
					t.Fatal("late installation failure left a partial target/deployment mutation")
				}
			} else if state != "deleting" || generation != 2 || resourceVersion != 2 || updatedBy != actor || status != "uninstalling" || action != "delete" || phase != "pending" || desired != 2 {
				t.Fatal("accepted uninstall did not commit exact fenced identities and actor together")
			}
		})
	}
}
