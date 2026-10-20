//go:build integration

package builtin

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func TestPostgresBuiltinRegistrationRetryPresenceAndOrdering(t *testing.T) {
	t.Parallel()
	dsn := testdb.DSN(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	pool := openBuiltinProvisionerTestPool(t, ctx, dsn)
	defer pool.Close()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE clusters(id uuid PRIMARY KEY, registration_phase text, is_local boolean, install_baseline boolean, annotations jsonb, decommissioned_at timestamptz);
		CREATE TABLE delivery_controller_inventory(cluster_id uuid, ready boolean, compatibility_status text);
		CREATE TABLE cluster_registration_steps(cluster_id uuid, step_name text, created_at timestamptz, step_order integer)`); err != nil {
		t.Fatal(err)
	}
	cluster := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO clusters VALUES ($1,'ready',false,NULL,'{}',NULL)`, cluster); err != nil {
		t.Fatal(err)
	}
	p := &Provisioner{pool: pool}
	state, err := p.loadRegistrationState(ctx, cluster)
	if err != nil || state.phase != "ready" || state.install || state.inventoryReady || state.latestRetryTime != nil {
		t.Fatalf("absent retry/nullable baseline was defaulted: state=%+v error=%v", state, err)
	}
	latest := time.Unix(1_700_000_100, 0).UTC()
	if _, err := pool.Exec(ctx, `INSERT INTO delivery_controller_inventory VALUES ($1,true,'compatible')`, cluster); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		cluster uuid.UUID
		name    string
		at      time.Time
		order   int32
	}{
		{cluster, "delivery_retry_requested", latest.Add(-time.Hour), 999},
		{cluster, "delivery_retry_requested", latest, 1},
		{cluster, "other_step", latest.Add(time.Hour), 1000},
		{uuid.New(), "delivery_retry_requested", latest.Add(time.Hour), 1000},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO cluster_registration_steps VALUES($1,$2,$3,$4)`, step.cluster, step.name, step.at, step.order); err != nil {
			t.Fatal(err)
		}
	}
	state, err = p.loadRegistrationState(ctx, cluster)
	if err != nil || !state.inventoryReady || state.latestRetryTime == nil || !state.latestRetryTime.Equal(latest) {
		t.Fatalf("retry ordering/cluster/step scope changed: error=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE clusters SET decommissioned_at=now() WHERE id=$1`, cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := p.loadRegistrationState(ctx, cluster); err == nil {
		t.Fatal("decommissioned cluster read produced an active registration state")
	}
}
