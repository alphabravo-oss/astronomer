package rollout

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/state"
)

type AssignmentAction string

const (
	AssignmentApply    AssignmentAction = "apply"
	AssignmentRollback AssignmentAction = "rollback"
)

type ClusterRuntime struct {
	ClusterID         uuid.UUID
	State             model.RolloutClusterState
	Fence             int64
	Connected         bool
	Available         bool
	ReadySince        *time.Time
	StateChangedAt    time.Time
	Deadline          time.Time
	CurrentGeneration int64
	EverReleased      bool
	LastAction        AssignmentAction
}

type RuntimeSnapshot struct {
	Plan     FrozenRollout
	State    model.RolloutState
	Fence    int64
	Clusters []ClusterRuntime
}

type EvaluateInput struct {
	Now             time.Time
	Snapshot        RuntimeSnapshot
	Lease           Lease
	InitialApproval *ApprovalDecision
	CohortApprovals map[int]ApprovalDecision
	Maintenance     MaintenanceGate
}

type BlockReason string

const (
	BlockNone         BlockReason = ""
	BlockApproval     BlockReason = "approval_required"
	BlockMaintenance  BlockReason = "maintenance_window_closed"
	BlockSoak         BlockReason = "soak_in_progress"
	BlockConcurrency  BlockReason = "max_concurrent"
	BlockUnavailable  BlockReason = "max_unavailable"
	BlockDisconnected BlockReason = "clusters_disconnected"
)

type RolloutTransition struct {
	From          model.RolloutState
	To            model.RolloutState
	ExpectedFence int64
	Code          string
}

type ClusterTransition struct {
	ClusterID     uuid.UUID
	From          model.RolloutClusterState
	To            model.RolloutClusterState
	ExpectedFence int64
	Code          string
}

type Release struct {
	ClusterID      uuid.UUID
	Action         AssignmentAction
	Version        VersionIdentity
	Generation     int64
	Deadline       time.Time
	ExpectedFence  int64
	IdempotencyKey model.Digest
	FromState      model.RolloutClusterState
	ToState        model.RolloutClusterState
}

type Decision struct {
	ID                   model.Digest
	ExpectedRolloutFence int64
	RolloutTransition    *RolloutTransition
	ClusterTransitions   []ClusterTransition
	Releases             []Release
	Counters             state.RolloutCounters
	Blocked              BlockReason
}

// Evaluate is deterministic for an authoritative runtime snapshot. The caller
// applies the decision with the included fences and records Decision.ID in the
// same transaction; replaying a committed decision is therefore a no-op.
func Evaluate(input EvaluateInput) (Decision, error) {
	now := input.Now.UTC()
	if now.IsZero() {
		return Decision{}, fail(CodeInvalidInput, "now", "must not be zero")
	}
	if err := input.Snapshot.Plan.Validate(); err != nil {
		return Decision{}, &Error{Code: CodeInvariant, Field: "plan", Cause: err}
	}
	if !input.Snapshot.State.Valid() || input.Snapshot.Fence < 0 {
		return Decision{}, fail(CodeInvariant, "rollout_runtime", "state and fence must be valid")
	}
	if err := input.Lease.validate(now, input.Snapshot.Fence); err != nil {
		return Decision{}, err
	}
	clusters, err := validateRuntime(input.Snapshot.Plan, input.Snapshot.Clusters)
	if err != nil {
		return Decision{}, err
	}
	decision := Decision{ExpectedRolloutFence: input.Snapshot.Fence}
	states := make([]model.RolloutClusterState, len(clusters))
	for index := range clusters {
		states[index] = clusters[index].State
	}
	decision.Counters, err = state.DeriveRolloutCounters(states)
	if err != nil {
		return Decision{}, err
	}

	switch input.Snapshot.State {
	case model.RolloutResolving:
		desired := model.RolloutQueued
		code := "plan_resolved"
		if input.Snapshot.Plan.Approval.Required {
			desired = model.RolloutAwaitingApproval
			code = "approval_requested"
		}
		err = setRolloutTransition(&decision, input.Snapshot, desired, code)
	case model.RolloutAwaitingApproval:
		if input.InitialApproval == nil || !input.InitialApproval.validFor(input.Snapshot.Plan.Approval.Digest, now) {
			decision.Blocked = BlockApproval
			break
		}
		if !input.InitialApproval.Approved {
			err = setRolloutTransition(&decision, input.Snapshot, model.RolloutRejected, "approval_rejected")
		} else {
			err = setRolloutTransition(&decision, input.Snapshot, model.RolloutQueued, "approval_granted")
		}
	case model.RolloutQueued:
		if input.Snapshot.Plan.Strategy.RespectMaintenanceWindows && !input.Maintenance.allowsRelease() {
			decision.Blocked = BlockMaintenance
			break
		}
		err = setRolloutTransition(&decision, input.Snapshot, model.RolloutProgressing, "rollout_started")
	case model.RolloutProgressing:
		err = evaluateProgressing(input, clusters, &decision)
	case model.RolloutFailed:
		if input.Snapshot.Plan.Strategy.OnFailure == model.FailureRollback {
			err = setRolloutTransition(&decision, input.Snapshot, model.RolloutRollingBack, "automatic_rollback_started")
		}
	case model.RolloutRollingBack:
		err = evaluateRollback(input, clusters, &decision)
	case model.RolloutDraft:
		err = fail(CodeInvariant, "rollout.state", "draft rollout is not schedulable")
	case model.RolloutPaused, model.RolloutRejected, model.RolloutAborted, model.RolloutSucceeded,
		model.RolloutRolledBack, model.RolloutRollbackFailed:
		// Operator-driven or terminal state: a worker intentionally emits a
		// stable no-op decision and performs no external effect.
	default:
		err = fail(CodeInvariant, "rollout.state", "unsupported schedulable state")
	}
	if err != nil {
		return Decision{}, err
	}
	decision.ID, err = decisionDigest(input.Snapshot.Plan.ID, decision)
	if err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func evaluateProgressing(input EvaluateInput, clusters []ClusterRuntime, decision *Decision) error {
	plan := input.Snapshot.Plan
	now := input.Now.UTC()

	// Reconnect transitions and deadline failures are CAS changes of their own;
	// a cluster is never released through an invalid blocked->released shortcut.
	for _, cluster := range clusters {
		switch {
		case cluster.State == model.RolloutClusterPending && !cluster.Connected:
			if err := addClusterTransition(decision, cluster, model.RolloutClusterBlocked, "cluster_disconnected"); err != nil {
				return err
			}
		case cluster.State == model.RolloutClusterBlocked && cluster.Connected:
			if err := addClusterTransition(decision, cluster, model.RolloutClusterPending, "cluster_reconnected"); err != nil {
				return err
			}
		case isApplyActive(cluster.State) && !deadlineFor(plan, cluster).After(now):
			if err := addClusterTransition(decision, cluster, model.RolloutClusterTimedOut, "progress_deadline_exceeded"); err != nil {
				return err
			}
		}
	}
	if len(decision.ClusterTransitions) != 0 {
		return nil
	}

	if !plan.Deadline.After(now) {
		return setRolloutTransition(decision, input.Snapshot, model.RolloutFailed, "rollout_deadline_exceeded")
	}
	failed := decision.Counters.Failed
	if failureBudgetExceeded(plan.Strategy.FailureThreshold, len(clusters), failed) {
		switch plan.Strategy.OnFailure {
		case model.FailurePause:
			return setRolloutTransition(decision, input.Snapshot, model.RolloutPaused, "failure_budget_exceeded")
		case model.FailureAbort, model.FailureRollback:
			return setRolloutTransition(decision, input.Snapshot, model.RolloutFailed, "failure_budget_exceeded")
		}
	}

	cohortIndex, completedAt, complete, err := activeCohort(plan, clusters, now)
	if err != nil {
		return err
	}
	if complete {
		if failed != 0 {
			return setRolloutTransition(decision, input.Snapshot, model.RolloutFailed, "rollout_completed_with_failures")
		}
		return setRolloutTransition(decision, input.Snapshot, model.RolloutSucceeded, "rollout_completed")
	}
	cohort := plan.Cohorts[cohortIndex]
	if cohortIndex > 0 {
		previous := plan.Cohorts[cohortIndex-1]
		if completedAt.Add(time.Duration(previous.SoakAfter)).After(now) {
			decision.Blocked = BlockSoak
			return nil
		}
	}
	if cohort.ApprovalRequired {
		approval, exists := input.CohortApprovals[cohort.Index]
		if !exists || !approval.validFor(cohort.ApprovalDigest, now) {
			decision.Blocked = BlockApproval
			return nil
		}
		if !approval.Approved {
			return setRolloutTransition(decision, input.Snapshot, model.RolloutPaused, "cohort_approval_rejected")
		}
	}
	if plan.Strategy.RespectMaintenanceWindows && !input.Maintenance.allowsRelease() {
		decision.Blocked = BlockMaintenance
		return nil
	}

	reserved := 0
	atRisk := 0
	for _, cluster := range clusters {
		if consumesConcurrency(cluster, plan.Strategy.MinReady, now) {
			reserved++
			// An in-flight cluster reserves one unavailable slot even while its
			// previous deployment remains healthy. Without this reservation, a
			// full batch could become unavailable simultaneously and exceed the
			// advertised hard budget before the next status report.
			atRisk++
		} else if !cluster.Available {
			atRisk++
		}
	}
	concurrencySlots := int(plan.Strategy.MaxConcurrent) - reserved
	if concurrencySlots <= 0 {
		decision.Blocked = BlockConcurrency
		return nil
	}
	maxUnavailable := amountLimit(plan.Strategy.MaxUnavailable, len(clusters))
	availabilitySlots := maxUnavailable - atRisk
	if availabilitySlots <= 0 {
		decision.Blocked = BlockUnavailable
		return nil
	}
	concurrencySlots = min(concurrencySlots, availabilitySlots)
	for _, planned := range plan.Clusters {
		if concurrencySlots == 0 {
			break
		}
		if planned.Cohort != cohortIndex {
			continue
		}
		cluster := clusters[planned.Order]
		if cluster.State != model.RolloutClusterPending || !cluster.Connected {
			continue
		}
		release, releaseErr := newRelease(plan, planned, cluster, AssignmentApply, plan.Desired, now)
		if releaseErr != nil {
			return releaseErr
		}
		decision.Releases = append(decision.Releases, release)
		concurrencySlots--
	}
	if len(decision.Releases) == 0 && decision.Blocked == BlockNone {
		for _, planned := range plan.Clusters {
			cluster := clusters[planned.Order]
			if planned.Cohort == cohortIndex && (cluster.State == model.RolloutClusterBlocked || !cluster.Connected) {
				decision.Blocked = BlockDisconnected
				break
			}
		}
	}
	return nil
}

func evaluateRollback(input EvaluateInput, clusters []ClusterRuntime, decision *Decision) error {
	plan := input.Snapshot.Plan
	now := input.Now.UTC()
	rollbackTargets := 0
	complete := 0
	reserved := 0
	atRisk := 0
	for _, planned := range plan.Clusters {
		cluster := clusters[planned.Order]
		if !cluster.EverReleased {
			continue
		}
		rollbackTargets++
		if planned.Previous == nil {
			return setRolloutTransition(decision, input.Snapshot, model.RolloutRollbackFailed, "previous_version_unavailable")
		}
		if cluster.State == model.RolloutClusterReadyPrevious {
			complete++
			continue
		}
		if (cluster.State == model.RolloutClusterFailed || cluster.State == model.RolloutClusterTimedOut) && cluster.LastAction == AssignmentRollback {
			return setRolloutTransition(decision, input.Snapshot, model.RolloutRollbackFailed, "rollback_cluster_failed")
		}
		if cluster.State == model.RolloutClusterRollingBack {
			reserved++
			if !deadlineFor(plan, cluster).After(now) {
				return setRolloutTransition(decision, input.Snapshot, model.RolloutRollbackFailed, "rollback_deadline_exceeded")
			}
		}
		if cluster.State == model.RolloutClusterRollingBack || !cluster.Available {
			atRisk++
		}
	}
	if rollbackTargets == 0 || complete == rollbackTargets {
		return setRolloutTransition(decision, input.Snapshot, model.RolloutRolledBack, "rollback_completed")
	}
	slots := int(plan.Strategy.MaxConcurrent) - reserved
	if slots <= 0 {
		decision.Blocked = BlockConcurrency
		return nil
	}
	// Recover already-unavailable clusters before touching healthy ones. This
	// deterministic priority makes rollback corrective even when the budget is
	// already exhausted, while healthy clusters still reserve a hard slot.
	rollbackOrder := append([]PlannedCluster(nil), plan.Clusters...)
	sort.SliceStable(rollbackOrder, func(i, j int) bool {
		leftUnavailable := !clusters[rollbackOrder[i].Order].Available
		rightUnavailable := !clusters[rollbackOrder[j].Order].Available
		return leftUnavailable && !rightUnavailable
	})
	maxUnavailable := amountLimit(plan.Strategy.MaxUnavailable, len(clusters))
	for _, planned := range rollbackOrder {
		if slots == 0 {
			break
		}
		cluster := clusters[planned.Order]
		if !cluster.EverReleased || cluster.State == model.RolloutClusterReadyPrevious || cluster.State == model.RolloutClusterRollingBack {
			continue
		}
		if !cluster.Connected {
			decision.Blocked = BlockDisconnected
			continue
		}
		if cluster.Available && atRisk >= maxUnavailable {
			decision.Blocked = BlockUnavailable
			continue
		}
		if planned.Previous == nil {
			return setRolloutTransition(decision, input.Snapshot, model.RolloutRollbackFailed, "previous_version_unavailable")
		}
		if !canStartRollback(cluster.State) {
			return fail(CodeInvariant, "cluster.state", fmt.Sprintf("cannot roll back cluster %s from %s", cluster.ClusterID, cluster.State))
		}
		release, err := newRelease(plan, planned, cluster, AssignmentRollback, planned.Previous.Version, now)
		if err != nil {
			return err
		}
		decision.Releases = append(decision.Releases, release)
		slots--
		if cluster.Available {
			atRisk++
		}
	}
	return nil
}

func validateRuntime(plan FrozenRollout, input []ClusterRuntime) ([]ClusterRuntime, error) {
	if len(input) != len(plan.Clusters) {
		return nil, fail(CodeInvariant, "runtime.clusters", "does not match frozen membership count")
	}
	byID := make(map[uuid.UUID]ClusterRuntime, len(input))
	for _, cluster := range input {
		if cluster.ClusterID == uuid.Nil || !cluster.State.Valid() || cluster.Fence < 0 || cluster.CurrentGeneration < 0 {
			return nil, fail(CodeInvariant, "runtime.clusters", "contains invalid identity, state, fence, or generation")
		}
		if _, exists := byID[cluster.ClusterID]; exists {
			return nil, fail(CodeInvariant, "runtime.clusters", "contains duplicate cluster")
		}
		if cluster.State == model.RolloutClusterReady && (cluster.ReadySince == nil || cluster.ReadySince.IsZero()) {
			return nil, fail(CodeInvariant, "runtime.ready_since", "ready cluster requires a stable timestamp")
		}
		byID[cluster.ClusterID] = cluster
	}
	ordered := make([]ClusterRuntime, len(plan.Clusters))
	for index, planned := range plan.Clusters {
		cluster, exists := byID[planned.ClusterID]
		if !exists {
			return nil, fail(CodeInvariant, "runtime.clusters", "is missing frozen cluster")
		}
		ordered[index] = cluster
	}
	return ordered, nil
}

func activeCohort(plan FrozenRollout, clusters []ClusterRuntime, now time.Time) (index int, previousCompletedAt time.Time, allComplete bool, err error) {
	previousCompletedAt = plan.CreatedAt
	for _, cohort := range plan.Cohorts {
		complete := true
		completedAt := plan.CreatedAt
		for _, planned := range plan.Clusters {
			if planned.Cohort != cohort.Index {
				continue
			}
			cluster := clusters[planned.Order]
			switch cluster.State {
			case model.RolloutClusterReady:
				matureAt := cluster.ReadySince.Add(time.Duration(plan.Strategy.MinReady))
				if matureAt.After(now) {
					complete = false
				}
				if matureAt.After(completedAt) {
					completedAt = matureAt
				}
			case model.RolloutClusterFailed, model.RolloutClusterTimedOut, model.RolloutClusterSkipped:
				if cluster.StateChangedAt.IsZero() {
					return 0, time.Time{}, false, fail(CodeInvariant, "runtime.state_changed_at", "terminal cluster requires a timestamp")
				}
				if cluster.StateChangedAt.After(completedAt) {
					completedAt = cluster.StateChangedAt
				}
			default:
				complete = false
			}
		}
		if !complete {
			return cohort.Index, previousCompletedAt, false, nil
		}
		previousCompletedAt = completedAt
	}
	return len(plan.Cohorts), previousCompletedAt, true, nil
}

func consumesConcurrency(cluster ClusterRuntime, minReady model.Duration, now time.Time) bool {
	switch cluster.State {
	case model.RolloutClusterReleased, model.RolloutClusterAcknowledged, model.RolloutClusterReconciling, model.RolloutClusterRollingBack:
		return true
	case model.RolloutClusterReady:
		return cluster.ReadySince.Add(time.Duration(minReady)).After(now)
	default:
		return false
	}
}

func amountLimit(amount model.Amount, total int) int {
	if amount.Type == model.AmountCount {
		return min(int(amount.Value), total)
	}
	return int(uint64(total) * uint64(amount.Value) / 100)
}

func failureBudgetExceeded(amount model.Amount, total, failures int) bool {
	return failures > amountLimit(amount, total)
}

func deadlineFor(plan FrozenRollout, cluster ClusterRuntime) time.Time {
	if !cluster.Deadline.IsZero() {
		return cluster.Deadline
	}
	return plan.Deadline
}

func isApplyActive(value model.RolloutClusterState) bool {
	return value == model.RolloutClusterReleased || value == model.RolloutClusterAcknowledged || value == model.RolloutClusterReconciling
}

func canStartRollback(value model.RolloutClusterState) bool {
	switch value {
	case model.RolloutClusterReleased, model.RolloutClusterAcknowledged, model.RolloutClusterReconciling,
		model.RolloutClusterReady, model.RolloutClusterTimedOut, model.RolloutClusterFailed:
		return true
	default:
		return false
	}
}

func newRelease(plan FrozenRollout, planned PlannedCluster, cluster ClusterRuntime, action AssignmentAction, version VersionIdentity, now time.Time) (Release, error) {
	if cluster.CurrentGeneration == math.MaxInt64 {
		return Release{}, fail(CodeInvariant, "generation", "cannot increment maximum generation")
	}
	desiredState := model.RolloutClusterReleased
	if action == AssignmentRollback {
		desiredState = model.RolloutClusterRollingBack
	}
	if _, err := state.TransitionRolloutCluster(state.RolloutCluster{State: cluster.State, Fence: cluster.Fence}, desiredState, cluster.Fence); err != nil {
		return Release{}, &Error{Code: CodeInvariant, Field: "cluster.transition", Cause: err}
	}
	key, err := model.CanonicalDigest(struct {
		RolloutID       uuid.UUID        `json:"rollout_id"`
		ClusterID       uuid.UUID        `json:"cluster_id"`
		Action          AssignmentAction `json:"action"`
		BundleVersionID uuid.UUID        `json:"bundle_version_id"`
		SpecDigest      model.Digest     `json:"spec_digest"`
	}{plan.ID, planned.ClusterID, action, version.BundleVersionID, version.SpecDigest})
	if err != nil {
		return Release{}, err
	}
	deadline := plan.Deadline
	if action == AssignmentRollback {
		deadline = now.Add(time.Duration(plan.Strategy.ProgressDeadline))
	}
	return Release{ClusterID: planned.ClusterID, Action: action, Version: version, Deadline: deadline,
		Generation: cluster.CurrentGeneration + 1, ExpectedFence: cluster.Fence,
		IdempotencyKey: key, FromState: cluster.State, ToState: desiredState}, nil
}

func setRolloutTransition(decision *Decision, snapshot RuntimeSnapshot, desired model.RolloutState, code string) error {
	if decision.RolloutTransition != nil {
		return fail(CodeInvariant, "decision", "contains multiple rollout transitions")
	}
	if _, err := state.TransitionRollout(state.Rollout{State: snapshot.State, Fence: snapshot.Fence}, desired, snapshot.Fence); err != nil {
		return &Error{Code: CodeInvariant, Field: "rollout.transition", Cause: err}
	}
	decision.RolloutTransition = &RolloutTransition{From: snapshot.State, To: desired, ExpectedFence: snapshot.Fence, Code: code}
	return nil
}

func addClusterTransition(decision *Decision, cluster ClusterRuntime, desired model.RolloutClusterState, code string) error {
	if _, err := state.TransitionRolloutCluster(state.RolloutCluster{State: cluster.State, Fence: cluster.Fence}, desired, cluster.Fence); err != nil {
		return &Error{Code: CodeInvariant, Field: "cluster.transition", Cause: err}
	}
	decision.ClusterTransitions = append(decision.ClusterTransitions, ClusterTransition{
		ClusterID: cluster.ClusterID, From: cluster.State, To: desired, ExpectedFence: cluster.Fence, Code: code,
	})
	return nil
}

func decisionDigest(rolloutID uuid.UUID, decision Decision) (model.Digest, error) {
	copy := decision
	copy.ID = ""
	// The runtime is already frozen in plan order, but explicit sorting makes
	// adapter-created snapshots incapable of changing decision identity.
	sort.Slice(copy.ClusterTransitions, func(i, j int) bool {
		return copy.ClusterTransitions[i].ClusterID.String() < copy.ClusterTransitions[j].ClusterID.String()
	})
	sort.Slice(copy.Releases, func(i, j int) bool { return copy.Releases[i].ClusterID.String() < copy.Releases[j].ClusterID.String() })
	return model.CanonicalDigest(struct {
		RolloutID uuid.UUID `json:"rollout_id"`
		Decision  Decision  `json:"decision"`
	}{rolloutID, copy})
}
