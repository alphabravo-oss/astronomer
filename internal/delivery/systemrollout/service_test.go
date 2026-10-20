package systemrollout

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"

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

func TestSystemReleaseSlotsEnforcesConcurrencyAndUnavailableBudgets(t *testing.T) {
	t.Parallel()
	base := model.RolloutStrategy{
		MaxConcurrent:  5,
		MaxUnavailable: model.Amount{Type: model.AmountCount, Value: 2},
	}
	tests := []struct {
		name    string
		current counts
		want    int32
	}{
		{name: "empty fleet budget", want: 2},
		{name: "in flight reserves both budgets", current: counts{inFlight: 1}, want: 1},
		{name: "failed member reserves availability", current: counts{failed: 1}, want: 1},
		{name: "immature ready reserves until soak", current: counts{immatureReady: 2}, want: 0},
		{name: "availability exhausted", current: counts{inFlight: 1, failed: 1}, want: 0},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := systemReleaseSlots(base, 10, test.current); got != test.want {
				t.Fatalf("release slots = %d, want %d", got, test.want)
			}
		})
	}
}

func TestSystemReleaseSlotsSupportsPercentageAndZeroUnavailable(t *testing.T) {
	t.Parallel()
	strategy := model.RolloutStrategy{
		MaxConcurrent:  10,
		MaxUnavailable: model.Amount{Type: model.AmountPercent, Value: 25},
	}
	if got := systemReleaseSlots(strategy, 10, counts{}); got != 2 {
		t.Fatalf("percentage release slots = %d, want floor(25%% of 10)=2", got)
	}
	strategy.MaxUnavailable.Value = 0
	if got := systemReleaseSlots(strategy, 10, counts{}); got != 1 {
		t.Fatalf("zero unavailable budget released %d assignments, want 1 (serial)", got)
	}
}

func TestSystemReleaseSlotsZeroUnavailableIsSerialNotStalled(t *testing.T) {
	t.Parallel()
	strategy := model.RolloutStrategy{MaxConcurrent: 10, MaxUnavailable: model.Amount{Type: model.AmountCount, Value: 0}}
	for _, test := range []struct {
		name    string
		total   int32
		current counts
		want    int32
	}{
		{name: "idle fleet releases one", total: 5, want: 1},
		{name: "one in flight blocks the next", total: 5, current: counts{inFlight: 1}, want: 0},
		{name: "a failure halts", total: 5, current: counts{failed: 1}, want: 0},
		{name: "soaking ready member blocks", total: 5, current: counts{immatureReady: 1}, want: 0},
		{name: "empty fleet", total: 0, want: 0},
	} {
		if got := systemReleaseSlots(strategy, test.total, test.current); got != test.want {
			t.Errorf("%s: slots=%d want=%d", test.name, got, test.want)
		}
	}
	percent := model.RolloutStrategy{MaxConcurrent: 10, MaxUnavailable: model.Amount{Type: model.AmountPercent, Value: 25}}
	if got := systemReleaseSlots(percent, 3, counts{}); got != 1 {
		t.Errorf("25%% of a 3-member fleet floored to %d slots, want 1", got)
	}
}

func TestPartialCanaryRollbackCompletionUsesOnlyRollbackTargets(t *testing.T) {
	t.Parallel()
	current := counts{
		pending:         8,
		rolledBack:      2,
		rollbackTargets: 2,
	}
	if !rollbackComplete(current) {
		t.Fatal("completed canary rollback was not recognized")
	}
	current.rollbackFailed = 1
	if rollbackComplete(current) {
		t.Fatal("failed rollback must not be classified as complete")
	}
}

func TestScanViewRejectsMalformedStoredStrategyWithoutLeakingData(t *testing.T) {
	for _, raw := range []string{`null`, `{"type":"rolling","max_concurrent":0,"private-fixture":true}`, `{"max_concurrent":"private-fixture"}`} {
		if _, err := viewFromRecord(sqlc.DeliverySystemRollout{Strategy: []byte(raw)}, nil); err == nil {
			t.Error("malformed stored strategy became a valid system rollout")
		} else if strings.Contains(err.Error(), "private-fixture") {
			t.Error("stored strategy leaked into reader error")
		}
	}
}

func TestScanViewRetainsValidAdditiveV1Strategy(t *testing.T) {
	raw, err := json.Marshal(model.RolloutStrategy{
		Type: model.StrategyRolling, MaxConcurrent: 1,
		MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 0},
		ProgressDeadline: model.Duration(60000000000),
		FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1},
		OnFailure:        model.FailurePause,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw[:len(raw)-1], []byte(`,"future_v1":true}`)...)
	view, err := viewFromRecord(sqlc.DeliverySystemRollout{Strategy: []byte(raw)}, nil)
	if err != nil || view.Strategy.MaxConcurrent != 1 {
		t.Fatalf("valid additive strategy lost: %v", err)
	}
}

func TestSystemReleaseSlotsBoundsUnsignedBudgetsBeforeNarrowing(t *testing.T) {
	for _, budget := range []uint32{1 << 31, ^uint32(0)} {
		strategy := model.RolloutStrategy{MaxConcurrent: budget, MaxUnavailable: model.Amount{Type: model.AmountCount, Value: budget}}
		if got := systemReleaseSlots(strategy, 3, counts{}); got != 3 {
			t.Errorf("large v1 budget stalled rollout: got=%d want=3", got)
		}
		if got := systemReleaseSlots(strategy, 3, counts{inFlight: 1}); got != 2 {
			t.Errorf("in-flight reservation lost: got=%d want=2", got)
		}
		if got := systemReleaseSlots(strategy, 0, counts{}); got != 0 {
			t.Errorf("empty fleet has slots: %d", got)
		}
		if got := systemReleaseSlots(strategy, -1, counts{}); got != 0 {
			t.Errorf("invalid negative fleet has slots: %d", got)
		}
	}
	strategy := model.RolloutStrategy{MaxConcurrent: ^uint32(0), MaxUnavailable: model.Amount{Type: model.AmountCount, Value: ^uint32(0)}}
	if got := systemReleaseSlots(strategy, 1<<31-1, counts{inFlight: 1<<31 - 1, immatureReady: 1<<31 - 1, failed: 1<<31 - 1}); got != 0 {
		t.Errorf("count subtraction wrapped into available slots: %d", got)
	}
}
