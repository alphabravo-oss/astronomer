package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ccrPrecheckQuerier embeds RuntimeQuerier so it satisfies the interface
// without implementing every method; only the methods the
// remediateConnectedFalse path touches are overridden. Any other call
// would nil-deref, which is the desired loud failure for an unexpected
// path in this focused test.
type ccrPrecheckQuerier struct {
	RuntimeQuerier
	cluster       sqlc.Cluster
	tokensCreated int
	attempts      []sqlc.InsertClusterConditionRemediationParams
}

func (q *ccrPrecheckQuerier) GetClusterByID(_ context.Context, _ uuid.UUID) (sqlc.Cluster, error) {
	return q.cluster, nil
}

func (q *ccrPrecheckQuerier) CreateClusterRegistrationToken(_ context.Context, _ sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error) {
	q.tokensCreated++
	return sqlc.ClusterRegistrationToken{ID: uuid.New(), ExpiresAt: time.Now().Add(24 * time.Hour)}, nil
}

func (q *ccrPrecheckQuerier) InsertClusterConditionRemediation(_ context.Context, arg sqlc.InsertClusterConditionRemediationParams) (sqlc.ClusterConditionRemediationAttempt, error) {
	q.attempts = append(q.attempts, arg)
	return sqlc.ClusterConditionRemediationAttempt{}, nil
}

// CreateAuditLogV1 is implemented so the auditWriterV1ForReconciler
// type-switch in remediateConnectedFalse doesn't reach a nil method on
// the embedded interface.
func (q *ccrPrecheckQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	return nil
}

func TestRemediateConnectedFalse_SkipsWhenAgentReconnected(t *testing.T) {
	clusterID := uuid.New()
	row := sqlc.ClusterCondition{ClusterID: clusterID, Type: ConditionConnected, Status: "False"}

	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	// Fresh heartbeat (30s ago, well inside the 2m window) => reconnected.
	freshQ := &ccrPrecheckQuerier{
		cluster: sqlc.Cluster{
			ID:            clusterID,
			LastHeartbeat: pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Second), Valid: true},
		},
	}
	runtimeDeps = RuntimeDependencies{Queries: freshQ}
	if err := remediateConnectedFalse(context.Background(), row); err != nil {
		t.Fatalf("remediateConnectedFalse (fresh) returned error: %v", err)
	}
	if freshQ.tokensCreated != 0 {
		t.Fatalf("expected no token reissue when agent reconnected, got %d", freshQ.tokensCreated)
	}
	if len(freshQ.attempts) != 1 || freshQ.attempts[0].Action != ccrActionNoopReconnected || freshQ.attempts[0].Outcome != ccrOutcomeSkip {
		t.Fatalf("expected one skipped %q attempt, got %+v", ccrActionNoopReconnected, freshQ.attempts)
	}

	// Stale heartbeat (5m ago, outside the window) => still down, reissue.
	staleQ := &ccrPrecheckQuerier{
		cluster: sqlc.Cluster{
			ID:            clusterID,
			LastHeartbeat: pgtype.Timestamptz{Time: time.Now().Add(-5 * time.Minute), Valid: true},
		},
	}
	runtimeDeps = RuntimeDependencies{Queries: staleQ}
	if err := remediateConnectedFalse(context.Background(), row); err != nil {
		t.Fatalf("remediateConnectedFalse (stale) returned error: %v", err)
	}
	if staleQ.tokensCreated != 1 {
		t.Fatalf("expected one token reissue when agent still disconnected, got %d", staleQ.tokensCreated)
	}
	if len(staleQ.attempts) != 1 || staleQ.attempts[0].Action != ccrActionTokenReissued || staleQ.attempts[0].Outcome != ccrOutcomeOk {
		t.Fatalf("expected one successful %q attempt, got %+v", ccrActionTokenReissued, staleQ.attempts)
	}
}
