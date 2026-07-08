package tasks

import (
	"context"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

// evaluateGlobalRuleClusters must report allFailed ONLY when a non-empty fleet
// produced zero evaluations. This guards the F6 regression fix: allFailed must
// NOT be spuriously true (which would fail every tick), and an empty fleet must
// report allFailed=false so the caller still runs the resolve-all sentinel.
// The all-errored -> allFailed=true path (a real monitoring outage, which needs
// a live/mocked PromQL client) is enforced by the evaluateRule guard and covered
// by review; here we pin the deterministic invariants.
func TestEvaluateGlobalRuleClusters_AllFailedInvariant(t *testing.T) {
	rule := sqlc.AlertRule{ID: uuid.New(), RuleType: "promql"}
	health := func(_ context.Context, _ sqlc.Cluster) (sqlc.ClusterHealthStatus, bool) {
		return sqlc.ClusterHealthStatus{}, false
	}

	// Non-empty fleet, evals succeed (non-triggering) -> out non-empty, allFailed false.
	clusters := []sqlc.Cluster{{ID: uuid.New(), Name: "a"}, {ID: uuid.New(), Name: "b"}}
	out, allFailed := evaluateGlobalRuleClusters(context.Background(), rule, map[string]any{}, clusters, health)
	if len(out) == 0 {
		t.Fatal("expected non-empty evaluations for a succeeding fleet")
	}
	if allFailed {
		t.Fatal("allFailed must be false when the fleet produced evaluations")
	}

	// Empty fleet -> no evaluations, allFailed false (this is the resolve-all case).
	out2, allFailed2 := evaluateGlobalRuleClusters(context.Background(), rule, map[string]any{}, nil, health)
	if len(out2) != 0 || allFailed2 {
		t.Fatalf("empty fleet must yield (0 evals, allFailed=false); got (%d, %v)", len(out2), allFailed2)
	}
}
