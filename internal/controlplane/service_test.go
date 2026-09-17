package controlplane

import (
	"context"
	"errors"
	"testing"
)

type summaryFunc func(context.Context) (map[string]any, error)

func (f summaryFunc) ControlPlaneSummary(ctx context.Context) (map[string]any, error) {
	return f(ctx)
}

func TestSnapshotAppliesPolicyAndAggregates(t *testing.T) {
	service := NewService(map[string]SummaryProvider{
		"monitoring": summaryFunc(func(context.Context) (map[string]any, error) {
			return map[string]any{
				"reconciler":         map[string]any{"queueDepth": 4, "staleRunningCount": 1},
				"recentFailureCount": 2,
			}, nil
		}),
		"security": summaryFunc(func(context.Context) (map[string]any, error) {
			return map[string]any{"health": "healthy"}, nil
		}),
		"unavailable": summaryFunc(func(context.Context) (map[string]any, error) {
			return nil, errors.New("unavailable")
		}),
	})
	snapshot := service.Snapshot(context.Background(), Policy{
		RecentFailureWindowMinutes: 15,
		Controllers: map[string]Thresholds{
			"monitoring": {QueueDepth: 4, StaleRunning: 2, RecentFailure: 3},
		},
	})

	if got := snapshot.Aggregate["controllers"]; got != 2 {
		t.Fatalf("controllers = %v, want 2", got)
	}
	if got := snapshot.Aggregate["degradedControllers"]; got != 1 {
		t.Fatalf("degradedControllers = %v, want 1", got)
	}
	if snapshot.Controllers["monitoring"]["health"] != "degraded" {
		t.Fatalf("monitoring health = %v, want degraded", snapshot.Controllers["monitoring"]["health"])
	}
	if len(snapshot.Evaluations) != 2 || !snapshot.Evaluations[0].QueueDepthExceeded {
		t.Fatalf("evaluations = %#v", snapshot.Evaluations)
	}
	policy := snapshot.Controllers["security"]["policy"].(map[string]any)
	if policy["managed"] != false {
		t.Fatalf("security policy = %#v, want unmanaged", policy)
	}
}
