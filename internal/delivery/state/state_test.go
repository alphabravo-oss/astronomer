package state

import (
	"errors"
	"math"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestRolloutTransitionTableEveryPair(t *testing.T) {
	t.Parallel()

	for _, from := range model.AllRolloutStates {
		for _, to := range model.AllRolloutStates {
			got, err := TransitionRollout(Rollout{State: from, Fence: 7}, to, 7)
			_, allowed := rolloutTransitions[from][to]
			if from == to {
				allowed = true
			}
			if allowed && err != nil {
				t.Errorf("%s -> %s should be allowed: %v", from, to, err)
			}
			if !allowed && !hasCode(err, CodeInvalidTransition) {
				t.Errorf("%s -> %s should be invalid_transition, got %v", from, to, err)
			}
			if err == nil && got.State != to {
				t.Errorf("%s -> %s returned state %s", from, to, got.State)
			}
		}
	}
}

func TestRolloutClusterTransitionTableEveryPair(t *testing.T) {
	t.Parallel()

	for _, from := range model.AllRolloutClusterStates {
		for _, to := range model.AllRolloutClusterStates {
			_, err := TransitionRolloutCluster(RolloutCluster{State: from, Fence: 1}, to, 1)
			_, allowed := rolloutClusterTransitions[from][to]
			if from == to {
				allowed = true
			}
			if allowed && err != nil {
				t.Errorf("%s -> %s should be allowed: %v", from, to, err)
			}
			if !allowed && !hasCode(err, CodeInvalidTransition) {
				t.Errorf("%s -> %s should be invalid_transition, got %v", from, to, err)
			}
		}
	}
}

func TestDeploymentTransitionTableEveryPair(t *testing.T) {
	t.Parallel()

	for _, from := range model.AllDeploymentPhases {
		for _, to := range model.AllDeploymentPhases {
			_, err := TransitionDeployment(Deployment{Phase: from, Fence: 2}, to, 2)
			_, allowed := deploymentTransitions[from][to]
			if from == to {
				allowed = true
			}
			if allowed && err != nil {
				t.Errorf("%s -> %s should be allowed: %v", from, to, err)
			}
			if !allowed && !hasCode(err, CodeInvalidTransition) {
				t.Errorf("%s -> %s should be invalid_transition, got %v", from, to, err)
			}
		}
	}
}

func TestTransitionFenceAndIdempotency(t *testing.T) {
	t.Parallel()

	current := Rollout{State: model.RolloutProgressing, Fence: 4}
	if got, err := TransitionRollout(current, model.RolloutSucceeded, 3); !hasCode(err, CodeStaleFence) || got != current {
		t.Fatalf("stale transition = %+v, %v", got, err)
	}
	if got, err := TransitionRollout(current, model.RolloutProgressing, 1); err != nil || got != current {
		t.Fatalf("idempotent transition = %+v, %v", got, err)
	}
	if got, err := TransitionRollout(Rollout{State: model.RolloutSucceeded, Fence: 9}, model.RolloutSucceeded, 1); err != nil || got.State != model.RolloutSucceeded {
		t.Fatalf("terminal idempotency = %+v, %v", got, err)
	}
}

func TestPauseResumeAbortAndRollback(t *testing.T) {
	t.Parallel()

	paused, err := TransitionRollout(Rollout{State: model.RolloutProgressing, Fence: 3}, model.RolloutPaused, 3)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := TransitionRollout(paused, model.RolloutProgressing, 3)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := TransitionRollout(resumed, model.RolloutFailed, 3)
	if err != nil {
		t.Fatal(err)
	}
	rollingBack, err := TransitionRollout(failed, model.RolloutRollingBack, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TransitionRollout(rollingBack, model.RolloutRolledBack, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := TransitionRollout(Rollout{State: model.RolloutQueued, Fence: 4}, model.RolloutAborted, 4); err != nil {
		t.Fatal(err)
	}
}

func TestClaimFence(t *testing.T) {
	t.Parallel()

	if got, err := ClaimFence(8, 8); err != nil || got != 9 {
		t.Fatalf("ClaimFence(8,8) = %d, %v", got, err)
	}
	if got, err := ClaimFence(8, 7); got != 8 || !hasCode(err, CodeStaleFence) {
		t.Fatalf("ClaimFence(8,7) = %d, %v", got, err)
	}
	if got, err := ClaimFence(math.MaxInt64, math.MaxInt64); got != math.MaxInt64 || !hasCode(err, CodeInvalidState) {
		t.Fatalf("ClaimFence(max,max) = %d, %v", got, err)
	}
}

func TestDeriveRolloutCounters(t *testing.T) {
	t.Parallel()

	states := []model.RolloutClusterState{
		model.RolloutClusterPending,
		model.RolloutClusterReleased,
		model.RolloutClusterAcknowledged,
		model.RolloutClusterReconciling,
		model.RolloutClusterRollingBack,
		model.RolloutClusterReady,
		model.RolloutClusterFailed,
		model.RolloutClusterTimedOut,
		model.RolloutClusterBlocked,
		model.RolloutClusterSkipped,
		model.RolloutClusterReadyPrevious,
	}
	got, err := DeriveRolloutCounters(states)
	if err != nil {
		t.Fatal(err)
	}
	want := RolloutCounters{Total: 11, Pending: 1, Active: 4, Ready: 1, Failed: 2, Blocked: 1, Skipped: 1, RollbackReady: 1, Terminal: 5, Unavailable: 9}
	if got != want {
		t.Fatalf("counters = %+v, want %+v", got, want)
	}
	if _, err := DeriveRolloutCounters([]model.RolloutClusterState{"invented"}); !hasCode(err, CodeInvalidState) {
		t.Fatalf("invalid state error = %v", err)
	}
}

func hasCode(err error, code ErrorCode) bool {
	var transition *TransitionError
	return errors.As(err, &transition) && transition.Code == code
}
