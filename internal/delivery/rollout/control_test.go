package rollout

import (
	"reflect"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestActionTransitionMatrix(t *testing.T) {
	tests := []struct {
		action  Action
		allowed []string
		next    string
	}{
		{ActionPause, []string{string(model.RolloutResolving), string(model.RolloutQueued), string(model.RolloutProgressing)}, string(model.RolloutPaused)},
		{ActionResume, []string{string(model.RolloutPaused)}, string(model.RolloutQueued)},
		{ActionAbort, []string{string(model.RolloutResolving), string(model.RolloutAwaitingApproval), string(model.RolloutQueued), string(model.RolloutProgressing), string(model.RolloutPaused), string(model.RolloutFailed)}, string(model.RolloutAborted)},
		{ActionRetry, []string{string(model.RolloutFailed), string(model.RolloutPaused)}, string(model.RolloutQueued)},
		{ActionRollback, []string{string(model.RolloutSucceeded), string(model.RolloutFailed), string(model.RolloutPaused)}, string(model.RolloutRollingBack)},
	}
	for _, test := range tests {
		allowed, next, err := actionTransition(test.action)
		if err != nil || next != test.next || !reflect.DeepEqual(allowed, test.allowed) {
			t.Fatalf("%s: allowed=%v next=%s err=%v", test.action, allowed, next, err)
		}
	}
	if _, _, err := actionTransition("delete"); !HasCode(err, CodeInvalidInput) {
		t.Fatalf("unsupported action error=%v", err)
	}
}
