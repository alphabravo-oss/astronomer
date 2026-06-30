package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ccrTTLQuerier captures the ExpiresAt of the registration token the
// Connected=False remediation reissue mints.
type ccrTTLQuerier struct {
	RuntimeQuerier
	cluster         sqlc.Cluster
	lastTokenExpiry time.Time
}

func (q *ccrTTLQuerier) GetClusterByID(_ context.Context, _ uuid.UUID) (sqlc.Cluster, error) {
	return q.cluster, nil
}

func (q *ccrTTLQuerier) CreateClusterRegistrationToken(_ context.Context, arg sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error) {
	q.lastTokenExpiry = arg.ExpiresAt
	return sqlc.ClusterRegistrationToken{ID: uuid.New(), ExpiresAt: arg.ExpiresAt}, nil
}

func (q *ccrTTLQuerier) InsertClusterConditionRemediation(_ context.Context, _ sqlc.InsertClusterConditionRemediationParams) (sqlc.ClusterConditionRemediationAttempt, error) {
	return sqlc.ClusterConditionRemediationAttempt{}, nil
}

func (q *ccrTTLQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	return nil
}

// TestReissueRegistrationTokenHonorsConfiguredTTL asserts the worker reissue
// path (L1) uses the configured RegistrationTokenTTLHours instead of the old
// hardcoded 24h, and that ConfigureRuntime defaults a zero value to 1h.
func TestReissueRegistrationTokenHonorsConfiguredTTL(t *testing.T) {
	clusterID := uuid.New()
	row := sqlc.ClusterCondition{ClusterID: clusterID, Type: ConditionConnected, Status: "False"}
	staleCluster := sqlc.Cluster{
		ID:            clusterID,
		LastHeartbeat: pgtype.Timestamptz{Time: time.Now().Add(-5 * time.Minute), Valid: true},
	}
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	approx := func(t *testing.T, expiry time.Time, want time.Duration) {
		t.Helper()
		if expiry.IsZero() {
			t.Fatal("no token minted")
		}
		if d := time.Until(expiry); d < want-2*time.Minute || d > want+2*time.Minute {
			t.Fatalf("token TTL = %s, want ~%s", d, want)
		}
	}

	// Explicit override (3h) flows into the mint.
	q := &ccrTTLQuerier{cluster: staleCluster}
	runtimeDeps = RuntimeDependencies{Queries: q, RegistrationTokenTTLHours: 3}
	if err := remediateConnectedFalse(context.Background(), row); err != nil {
		t.Fatalf("remediate (override): %v", err)
	}
	approx(t, q.lastTokenExpiry, 3*time.Hour)

	// ConfigureRuntime defaults a zero TTL to 1h (not the old 24h).
	q2 := &ccrTTLQuerier{cluster: staleCluster}
	ConfigureRuntime(RuntimeDependencies{Queries: q2}) // RegistrationTokenTTLHours unset -> 0
	if runtimeDeps.RegistrationTokenTTLHours != 1 {
		t.Fatalf("ConfigureRuntime must default RegistrationTokenTTLHours to 1, got %d", runtimeDeps.RegistrationTokenTTLHours)
	}
	if err := remediateConnectedFalse(context.Background(), row); err != nil {
		t.Fatalf("remediate (default): %v", err)
	}
	approx(t, q2.lastTokenExpiry, time.Hour)
}
