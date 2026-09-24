package catalogapp

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"testing"
)

func TestCatalogRolloutIntentPreservesActorScopeAndRetryIdentity(t *testing.T) {
	request := InstallRequest{InstallationID: uuid.New(), ProjectID: uuid.New(), ClusterID: uuid.New(), ActorID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, IdempotencyKey: uuid.NewString(), ReleaseName: "trivy-operator"}
	target := uuid.New()
	first := catalogRolloutAuditIntent(request, target)
	second := catalogRolloutAuditIntent(request, target)
	if first.IsZero() || first.DedupeKey != second.DedupeKey {
		t.Fatal("missing or unstable rollout audit intent")
	}
	if first.Event.UserID != request.ActorID || first.Event.Detail["project_id"] != request.ProjectID.String() || first.Event.Detail["cluster_id"] != request.ClusterID.String() {
		t.Fatal("lost initiating actor or tenant scope")
	}
	if first.Event.CorrelationID != request.IdempotencyKey {
		t.Fatal("lost catalog operation correlation")
	}
	if first.DedupeKey == catalogRolloutAuditIntent(request, uuid.New()).DedupeKey {
		t.Fatal("different targets share an audit identity")
	}
}
