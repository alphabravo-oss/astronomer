package observability

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakePlatformConfigQuerier struct {
	cfg          sqlc.PlatformConfiguration
	getErr       error
	upsertCalled bool
}

func (f *fakePlatformConfigQuerier) GetPlatformConfig(context.Context) (sqlc.PlatformConfiguration, error) {
	return f.cfg, f.getErr
}

func (f *fakePlatformConfigQuerier) UpsertPlatformConfig(_ context.Context, arg sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error) {
	f.upsertCalled = true
	f.cfg = sqlc.PlatformConfiguration{
		ID:               1,
		ServerUrl:        arg.ServerUrl,
		PlatformName:     arg.PlatformName,
		TelemetryEnabled: arg.TelemetryEnabled,
		BootstrappedAt:   arg.BootstrappedAt,
		InstanceID:       arg.InstanceID,
	}
	return f.cfg, nil
}

func TestEnsureInstanceIDPreservesExistingValue(t *testing.T) {
	SetInstanceID("unknown")
	existing := uuid.New()
	q := &fakePlatformConfigQuerier{
		cfg: sqlc.PlatformConfiguration{
			ID:         1,
			InstanceID: existing,
		},
	}

	got, err := EnsureInstanceID(context.Background(), q)
	if err != nil {
		t.Fatalf("EnsureInstanceID() error = %v", err)
	}
	if got != existing.String() {
		t.Fatalf("instance id = %q, want %q", got, existing.String())
	}
	if q.upsertCalled {
		t.Fatal("did not expect upsert when instance id already exists")
	}
}

func TestEnsureInstanceIDCreatesMissingValue(t *testing.T) {
	SetInstanceID("unknown")
	q := &fakePlatformConfigQuerier{
		getErr: pgx.ErrNoRows,
		cfg: sqlc.PlatformConfiguration{
			PlatformName:   "Astronomer",
			BootstrappedAt: pgtype.Timestamptz{},
		},
	}

	got, err := EnsureInstanceID(context.Background(), q)
	if err != nil {
		t.Fatalf("EnsureInstanceID() error = %v", err)
	}
	if got == "" || got == "unknown" {
		t.Fatalf("instance id = %q, want generated uuid", got)
	}
	if !q.upsertCalled {
		t.Fatal("expected upsert when instance id is missing")
	}
	if q.cfg.InstanceID == uuid.Nil {
		t.Fatal("expected stored instance id to be non-zero")
	}
}
