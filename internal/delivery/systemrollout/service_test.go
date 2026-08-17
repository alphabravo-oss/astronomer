package systemrollout

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestActionTransitionIsClosedAndRequiresPreviousRelease(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state       string
		action      Action
		hasPrevious bool
		want        string
		wantError   bool
	}{
		{"awaiting_approval", ActionApprove, true, "queued", false},
		{"progressing", ActionPause, true, "paused", false},
		{"paused", ActionResume, true, "queued", false},
		{"queued", ActionAbort, true, "aborted", false},
		{"failed", ActionRetry, true, "queued", false},
		{"progressing", ActionRollback, true, "rolling_back", false},
		{"progressing", ActionRollback, false, "", true},
		{"succeeded", ActionResume, true, "", true},
		{"progressing", Action("inject"), true, "", true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.state+"_"+string(test.action), func(t *testing.T) {
			t.Parallel()
			got, _, err := actionTransition(test.state, test.action, test.hasPrevious)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("transition = (%q,%v), want (%q,error=%v)", got, err, test.want, test.wantError)
			}
		})
	}
}

func TestFailureThresholdUsesCountOrInclusivePercentage(t *testing.T) {
	t.Parallel()
	if failureExceeded(model.Amount{Type: model.AmountCount, Value: 2}, 10, 1) {
		t.Fatal("count budget tripped early")
	}
	if !failureExceeded(model.Amount{Type: model.AmountCount, Value: 2}, 10, 2) {
		t.Fatal("count budget did not trip at threshold")
	}
	if failureExceeded(model.Amount{Type: model.AmountPercent, Value: 20}, 10, 1) {
		t.Fatal("percentage budget tripped early")
	}
	if !failureExceeded(model.Amount{Type: model.AmountPercent, Value: 20}, 10, 2) {
		t.Fatal("percentage budget did not trip at threshold")
	}
}

func TestCanonicalStrategyIsStableForExplicitCanaryOrder(t *testing.T) {
	t.Parallel()
	left := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	right := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	strategy := func(ids []uuid.UUID) model.RolloutStrategy {
		return model.RolloutStrategy{
			Type: model.StrategyCanary, MaxConcurrent: 2,
			MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 1},
			ProgressDeadline: model.Duration(60000000000),
			FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1},
			OnFailure:        model.FailureRollback,
			Canary:           &model.CanarySpec{ClusterIDs: ids},
		}
	}
	one, digestOne, err := canonicalStrategy(strategy([]uuid.UUID{right, left}))
	if err != nil {
		t.Fatal(err)
	}
	two, digestTwo, err := canonicalStrategy(strategy([]uuid.UUID{left, right}))
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != string(two) || digestOne != digestTwo {
		t.Fatalf("canonical strategies differ:\n%s\n%s", one, two)
	}
}
