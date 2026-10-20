package state

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestPersistedTerminalClusterStatesRemainReadableAndCannotResume(t *testing.T) {
	t.Parallel()
	for _, current := range []model.RolloutClusterState{model.RolloutClusterAborted, model.RolloutClusterRollbackFailed} {
		if !current.Valid() {
			t.Fatalf("historically allowed DB state %q is rejected", current)
		}
		for _, next := range model.AllRolloutClusterStates {
			_, err := TransitionRolloutCluster(RolloutCluster{State: current, Fence: 7}, next, 7)
			if next == current && err != nil {
				t.Errorf("terminal observation is not idempotent: %v", err)
			}
			if next != current && !hasCode(err, CodeInvalidTransition) {
				t.Errorf("terminal %s resumed into %s: %v", current, next, err)
			}
		}
	}
	counters, err := DeriveRolloutCounters([]model.RolloutClusterState{model.RolloutClusterAborted, model.RolloutClusterRollbackFailed})
	if err != nil {
		t.Fatal(err)
	}
	want := RolloutCounters{Total: 2, Failed: 1, Skipped: 1, Terminal: 2, Unavailable: 2}
	if counters != want {
		t.Fatalf("terminal states must never become successful/available: got%+v want%+v", counters, want)
	}
}
