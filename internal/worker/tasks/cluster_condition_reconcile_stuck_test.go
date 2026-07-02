package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ccrStuckQuerier drives the full HandleClusterConditionReconcile sweep for
// the stuck-applying path. It returns a TemplateApplyStuck=True condition
// (and NO False conditions) so the test proves the reconciler now scans the
// True set — before the fix it only listed status='False' and the stuck row
// was never remediated.
type ccrStuckQuerier struct {
	RuntimeQuerier
	clusterID uuid.UUID

	falseListed bool
	trueListed  bool
	appStatus   string
	markedTo    string
	markCalls   int
	clearedTo   string
}

func (q *ccrStuckQuerier) ListClusterConditionsByStatus(_ context.Context, status string) ([]sqlc.ClusterCondition, error) {
	switch status {
	case ccrStatusFalse:
		q.falseListed = true
		return nil, nil
	case ccrStatusTrue:
		q.trueListed = true
		return []sqlc.ClusterCondition{{
			ClusterID: q.clusterID,
			Type:      ConditionTemplateApplyStuck,
			Status:    "True",
			Reason:    "ApplyingOverThreshold",
		}}, nil
	}
	return nil, nil
}

func (q *ccrStuckQuerier) CountClusterConditionRemediationSinceForType(_ context.Context, _ sqlc.CountClusterConditionRemediationSinceForTypeParams) (int64, error) {
	return 0, nil
}

func (q *ccrStuckQuerier) GetLatestNonSkipClusterConditionRemediation(_ context.Context, _ sqlc.GetLatestNonSkipClusterConditionRemediationParams) (sqlc.ClusterConditionRemediationAttempt, error) {
	return sqlc.ClusterConditionRemediationAttempt{}, pgx.ErrNoRows // no prior attempt → no backoff
}

func (q *ccrStuckQuerier) GetClusterTemplateApplication(_ context.Context, _ uuid.UUID) (sqlc.ClusterTemplateApplication, error) {
	return sqlc.ClusterTemplateApplication{ClusterID: q.clusterID, Status: q.appStatus}, nil
}

func (q *ccrStuckQuerier) MarkClusterTemplateApplicationStatus(_ context.Context, arg sqlc.MarkClusterTemplateApplicationStatusParams) (sqlc.ClusterTemplateApplication, error) {
	q.markCalls++
	q.markedTo = arg.Status
	return sqlc.ClusterTemplateApplication{ClusterID: q.clusterID, Status: arg.Status}, nil
}

func (q *ccrStuckQuerier) UpsertClusterCondition(_ context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error) {
	if arg.Type == ConditionTemplateApplyStuck {
		q.clearedTo = arg.Status
	}
	return sqlc.ClusterCondition{}, nil
}

func (q *ccrStuckQuerier) InsertClusterConditionRemediation(_ context.Context, _ sqlc.InsertClusterConditionRemediationParams) (sqlc.ClusterConditionRemediationAttempt, error) {
	return sqlc.ClusterConditionRemediationAttempt{}, nil
}

func (q *ccrStuckQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	return nil
}

// TestReconcile_RemediatesStuckApplyingTrueCondition verifies the sweep now
// picks up a TemplateApplyStuck=True condition (whose failure state is True,
// not False) and resets the wedged 'applying' row to 'failed' so the recovery
// sweep can re-enqueue it. Before the fix the reconciler only listed
// status='False', so the stuck row was never remediated and the wizard stayed
// red forever.
func TestReconcile_RemediatesStuckApplyingTrueCondition(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	q := &ccrStuckQuerier{clusterID: uuid.New(), appStatus: "applying"}
	runtimeDeps = RuntimeDependencies{Queries: q} // Leader nil → sweep runs inline

	if err := HandleClusterConditionReconcile(context.Background(), nil); err != nil {
		t.Fatalf("HandleClusterConditionReconcile returned error: %v", err)
	}

	if !q.trueListed {
		t.Fatalf("reconciler never listed status='True' conditions; stuck-applying rows would leak")
	}
	if q.markCalls != 1 || q.markedTo != "failed" {
		t.Fatalf("stuck 'applying' row not reset to failed: markCalls=%d markedTo=%q", q.markCalls, q.markedTo)
	}
	if q.clearedTo != "False" {
		t.Fatalf("TemplateApplyStuck condition not cleared to False after remediation, got %q", q.clearedTo)
	}
}

// TestCcrRemediableWhenTrue guards the True-status allowlist: only conditions
// whose failure state is True are dispatched from the True sweep. A healthy
// Connected=True row must NOT be routed into the connectivity remedy.
func TestCcrRemediableWhenTrue(t *testing.T) {
	if !ccrRemediableWhenTrue(ConditionTemplateApplyStuck) {
		t.Fatalf("TemplateApplyStuck must be remediable when True")
	}
	if ccrRemediableWhenTrue(ConditionConnected) {
		t.Fatalf("Connected=True is the healthy state and must NOT be remediated")
	}
}
