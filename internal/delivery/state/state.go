// Package state is the single authority for delivery state transitions. It is
// pure: persistence layers perform the compare-and-swap transaction and event
// write around these checks.
package state

import (
	"fmt"
	"math"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

type ErrorCode string

const (
	CodeInvalidState      ErrorCode = "invalid_state"
	CodeInvalidTransition ErrorCode = "invalid_transition"
	CodeStaleFence        ErrorCode = "stale_fence"
)

// TransitionError is safe to map to a stable API/worker result code.
type TransitionError struct {
	Code          ErrorCode
	Machine       string
	From          string
	To            string
	ExpectedFence int64
	ActualFence   int64
}

func (e *TransitionError) Error() string {
	if e == nil {
		return "delivery state transition failed"
	}
	if e.Code == CodeStaleFence {
		return fmt.Sprintf("%s transition rejected: stale fence %d (current %d)", e.Machine, e.ExpectedFence, e.ActualFence)
	}
	return fmt.Sprintf("%s transition %q -> %q rejected: %s", e.Machine, e.From, e.To, e.Code)
}

type Rollout struct {
	State model.RolloutState
	Fence int64
}

type RolloutCluster struct {
	State model.RolloutClusterState
	Fence int64
}

type Deployment struct {
	Phase model.DeploymentPhase
	Fence int64
}

var rolloutTransitions = transitionTable[model.RolloutState]{
	model.RolloutDraft:            set(model.RolloutResolving, model.RolloutAborted),
	model.RolloutResolving:        set(model.RolloutAwaitingApproval, model.RolloutQueued, model.RolloutFailed, model.RolloutAborted),
	model.RolloutAwaitingApproval: set(model.RolloutQueued, model.RolloutRejected, model.RolloutAborted),
	model.RolloutQueued:           set(model.RolloutProgressing, model.RolloutPaused, model.RolloutSucceeded, model.RolloutFailed, model.RolloutAborted),
	model.RolloutProgressing:      set(model.RolloutPaused, model.RolloutSucceeded, model.RolloutFailed, model.RolloutAborted),
	model.RolloutPaused:           set(model.RolloutQueued, model.RolloutProgressing, model.RolloutFailed, model.RolloutAborted, model.RolloutRollingBack),
	model.RolloutSucceeded:        set(model.RolloutRollingBack),
	model.RolloutFailed:           set(model.RolloutRollingBack),
	model.RolloutRollingBack:      set(model.RolloutRolledBack, model.RolloutRollbackFailed),
	model.RolloutRejected:         set[model.RolloutState](),
	model.RolloutAborted:          set[model.RolloutState](),
	model.RolloutRolledBack:       set[model.RolloutState](),
	model.RolloutRollbackFailed:   set[model.RolloutState](),
}

var rolloutClusterTransitions = transitionTable[model.RolloutClusterState]{
	model.RolloutClusterPending:       set(model.RolloutClusterReleased, model.RolloutClusterBlocked, model.RolloutClusterSkipped),
	model.RolloutClusterBlocked:       set(model.RolloutClusterPending, model.RolloutClusterSkipped),
	model.RolloutClusterReleased:      set(model.RolloutClusterAcknowledged, model.RolloutClusterReconciling, model.RolloutClusterReady, model.RolloutClusterTimedOut, model.RolloutClusterFailed, model.RolloutClusterRollingBack),
	model.RolloutClusterAcknowledged:  set(model.RolloutClusterReconciling, model.RolloutClusterReady, model.RolloutClusterTimedOut, model.RolloutClusterFailed, model.RolloutClusterRollingBack),
	model.RolloutClusterReconciling:   set(model.RolloutClusterReady, model.RolloutClusterTimedOut, model.RolloutClusterFailed, model.RolloutClusterRollingBack),
	model.RolloutClusterReady:         set(model.RolloutClusterReconciling, model.RolloutClusterFailed, model.RolloutClusterRollingBack),
	model.RolloutClusterTimedOut:      set(model.RolloutClusterPending, model.RolloutClusterRollingBack),
	model.RolloutClusterFailed:        set(model.RolloutClusterPending, model.RolloutClusterRollingBack),
	model.RolloutClusterRollingBack:   set(model.RolloutClusterReadyPrevious, model.RolloutClusterFailed),
	model.RolloutClusterSkipped:       set[model.RolloutClusterState](),
	model.RolloutClusterReadyPrevious: set(model.RolloutClusterRollingBack, model.RolloutClusterFailed),
}

var deploymentTransitions = transitionTable[model.DeploymentPhase]{
	model.DeploymentUnknown:   set(model.DeploymentPending, model.DeploymentBlocked, model.DeploymentApplying, model.DeploymentReady, model.DeploymentDegraded, model.DeploymentFailed, model.DeploymentSuspended, model.DeploymentDeleting, model.DeploymentRemoved),
	model.DeploymentPending:   set(model.DeploymentBlocked, model.DeploymentApplying, model.DeploymentFailed, model.DeploymentSuspended, model.DeploymentDeleting, model.DeploymentUnknown),
	model.DeploymentBlocked:   set(model.DeploymentPending, model.DeploymentApplying, model.DeploymentFailed, model.DeploymentSuspended, model.DeploymentDeleting, model.DeploymentUnknown),
	model.DeploymentApplying:  set(model.DeploymentReady, model.DeploymentDegraded, model.DeploymentFailed, model.DeploymentSuspended, model.DeploymentDeleting, model.DeploymentUnknown),
	model.DeploymentReady:     set(model.DeploymentApplying, model.DeploymentDegraded, model.DeploymentFailed, model.DeploymentSuspended, model.DeploymentDeleting, model.DeploymentUnknown),
	model.DeploymentDegraded:  set(model.DeploymentApplying, model.DeploymentReady, model.DeploymentFailed, model.DeploymentSuspended, model.DeploymentDeleting, model.DeploymentUnknown),
	model.DeploymentFailed:    set(model.DeploymentPending, model.DeploymentApplying, model.DeploymentSuspended, model.DeploymentDeleting, model.DeploymentUnknown),
	model.DeploymentSuspended: set(model.DeploymentPending, model.DeploymentApplying, model.DeploymentDeleting, model.DeploymentUnknown),
	model.DeploymentDeleting:  set(model.DeploymentRemoved, model.DeploymentFailed, model.DeploymentUnknown),
	model.DeploymentRemoved:   set(model.DeploymentPending),
}

// TransitionRollout validates a CAS transition. Idempotent repeats are
// successful no-ops, including a retried terminal transition with an old fence.
func TransitionRollout(current Rollout, desired model.RolloutState, expectedFence int64) (Rollout, error) {
	if !current.State.Valid() || !desired.Valid() {
		return current, invalidState("rollout", string(current.State), string(desired))
	}
	if current.State == desired {
		return current, nil
	}
	if current.Fence != expectedFence {
		return current, staleFence("rollout", string(current.State), string(desired), expectedFence, current.Fence)
	}
	if !rolloutTransitions.allows(current.State, desired) {
		return current, invalidTransition("rollout", string(current.State), string(desired))
	}
	current.State = desired
	return current, nil
}

func TransitionRolloutCluster(current RolloutCluster, desired model.RolloutClusterState, expectedFence int64) (RolloutCluster, error) {
	if !current.State.Valid() || !desired.Valid() {
		return current, invalidState("rollout_cluster", string(current.State), string(desired))
	}
	if current.State == desired {
		return current, nil
	}
	if current.Fence != expectedFence {
		return current, staleFence("rollout_cluster", string(current.State), string(desired), expectedFence, current.Fence)
	}
	if !rolloutClusterTransitions.allows(current.State, desired) {
		return current, invalidTransition("rollout_cluster", string(current.State), string(desired))
	}
	current.State = desired
	return current, nil
}

func TransitionDeployment(current Deployment, desired model.DeploymentPhase, expectedFence int64) (Deployment, error) {
	if !current.Phase.Valid() || !desired.Valid() {
		return current, invalidState("deployment", string(current.Phase), string(desired))
	}
	if current.Phase == desired {
		return current, nil
	}
	if current.Fence != expectedFence {
		return current, staleFence("deployment", string(current.Phase), string(desired), expectedFence, current.Fence)
	}
	if !deploymentTransitions.allows(current.Phase, desired) {
		return current, invalidTransition("deployment", string(current.Phase), string(desired))
	}
	current.Phase = desired
	return current, nil
}

// ClaimFence performs the fencing-generation CAS used when a worker acquires a
// durable claim. Returning the incremented value lets the transaction bind all
// later state changes to that exact claim.
func ClaimFence(current, expected int64) (int64, error) {
	if current < 0 || expected < 0 || current == math.MaxInt64 {
		return current, invalidState("fence", fmt.Sprint(current), fmt.Sprint(expected))
	}
	if current != expected {
		return current, staleFence("fence", fmt.Sprint(current), fmt.Sprint(current+1), expected, current)
	}
	return current + 1, nil
}

type RolloutCounters struct {
	Total         int `json:"total"`
	Pending       int `json:"pending"`
	Active        int `json:"active"`
	Ready         int `json:"ready"`
	Failed        int `json:"failed"`
	Blocked       int `json:"blocked"`
	Skipped       int `json:"skipped"`
	RollbackReady int `json:"rollback_ready"`
	Terminal      int `json:"terminal"`
	Unavailable   int `json:"unavailable"`
}

// DeriveRolloutCounters derives persisted counters from the authoritative
// rollout-cluster states. Unknown values fail instead of silently skewing
// availability and failure budgets.
func DeriveRolloutCounters(states []model.RolloutClusterState) (RolloutCounters, error) {
	var counters RolloutCounters
	for _, clusterState := range states {
		if !clusterState.Valid() {
			return RolloutCounters{}, invalidState("rollout_cluster", string(clusterState), "")
		}
		counters.Total++
		switch clusterState {
		case model.RolloutClusterPending:
			counters.Pending++
		case model.RolloutClusterReleased, model.RolloutClusterAcknowledged, model.RolloutClusterReconciling, model.RolloutClusterRollingBack:
			counters.Active++
		case model.RolloutClusterReady:
			counters.Ready++
		case model.RolloutClusterFailed, model.RolloutClusterTimedOut:
			counters.Failed++
		case model.RolloutClusterBlocked:
			counters.Blocked++
		case model.RolloutClusterSkipped:
			counters.Skipped++
		case model.RolloutClusterReadyPrevious:
			counters.RollbackReady++
		}
	}
	counters.Terminal = counters.Ready + counters.Failed + counters.Skipped + counters.RollbackReady
	counters.Unavailable = counters.Total - counters.Ready - counters.RollbackReady
	return counters, nil
}

type transitionTable[T comparable] map[T]map[T]struct{}

func (t transitionTable[T]) allows(from, to T) bool {
	_, ok := t[from][to]
	return ok
}

func set[T comparable](values ...T) map[T]struct{} {
	result := make(map[T]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func invalidState(machine, from, to string) *TransitionError {
	return &TransitionError{Code: CodeInvalidState, Machine: machine, From: from, To: to}
}

func invalidTransition(machine, from, to string) *TransitionError {
	return &TransitionError{Code: CodeInvalidTransition, Machine: machine, From: from, To: to}
}

func staleFence(machine, from, to string, expected, actual int64) *TransitionError {
	return &TransitionError{Code: CodeStaleFence, Machine: machine, From: from, To: to, ExpectedFence: expected, ActualFence: actual}
}
