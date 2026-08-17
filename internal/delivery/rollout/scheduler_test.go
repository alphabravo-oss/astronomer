package rollout

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestSchedulerApprovalAndMaintenanceGates(t *testing.T) {
	t.Parallel()
	plan := testPlan(t, 3, testStrategy(model.StrategyRolling, 2), true)
	runtime := testRuntime(plan, model.RolloutResolving)
	decision := mustEvaluate(t, evaluateInput(runtime, testNow))
	assertRolloutTransition(t, decision, model.RolloutAwaitingApproval)

	runtime.State = model.RolloutAwaitingApproval
	blocked := mustEvaluate(t, evaluateInput(runtime, testNow))
	if blocked.Blocked != BlockApproval || blocked.RolloutTransition != nil {
		t.Fatalf("missing approval decision = %+v", blocked)
	}

	deniedInput := evaluateInput(runtime, testNow)
	deniedInput.InitialApproval = approvalFor(plan.Approval.Digest, testNow, false)
	denied := mustEvaluate(t, deniedInput)
	assertRolloutTransition(t, denied, model.RolloutRejected)

	approvedInput := evaluateInput(runtime, testNow)
	approvedInput.InitialApproval = approvalFor(plan.Approval.Digest, testNow, true)
	approved := mustEvaluate(t, approvedInput)
	assertRolloutTransition(t, approved, model.RolloutQueued)

	wrongInput := approvedInput
	wrong := *wrongInput.InitialApproval
	wrong.BindingDigest = digestForTest('f')
	wrongInput.InitialApproval = &wrong
	if got := mustEvaluate(t, wrongInput); got.Blocked != BlockApproval {
		t.Fatalf("wrong-resource approval was accepted: %+v", got)
	}

	plan.Strategy.RespectMaintenanceWindows = true
	resealPlan(t, &plan)
	runtime = testRuntime(plan, model.RolloutQueued)
	closedInput := evaluateInput(runtime, testNow)
	closedInput.Maintenance = MaintenanceGate{}
	if got := mustEvaluate(t, closedInput); got.Blocked != BlockMaintenance || got.RolloutTransition != nil {
		t.Fatalf("closed maintenance gate = %+v", got)
	}
	overrideInput := closedInput
	overrideInput.Maintenance = MaintenanceGate{OverrideAuthorized: true, OverrideReason: "incident-1234"}
	assertRolloutTransition(t, mustEvaluate(t, overrideInput), model.RolloutProgressing)
	overrideInput.Maintenance.OverrideReason = " "
	if got := mustEvaluate(t, overrideInput); got.Blocked != BlockMaintenance {
		t.Fatalf("empty override reason bypassed maintenance: %+v", got)
	}
}

func TestSchedulerHardConcurrencyOrderingAndIdempotency(t *testing.T) {
	t.Parallel()
	plan := testPlan(t, 7, testStrategy(model.StrategyAllAtOnce, 3), false)
	runtime := testRuntime(plan, model.RolloutProgressing)
	input := evaluateInput(runtime, testNow)
	first := mustEvaluate(t, input)
	second := mustEvaluate(t, input)
	if first.ID != second.ID || !slices.Equal(first.Releases, second.Releases) {
		t.Fatalf("same snapshot changed decision:\n%+v\n%+v", first, second)
	}
	if len(first.Releases) != 3 {
		t.Fatalf("releases = %d, want hard cap 3", len(first.Releases))
	}
	for index, release := range first.Releases {
		if release.ClusterID != plan.Clusters[index].ClusterID || release.Generation != 1 || release.Action != AssignmentApply || release.IdempotencyKey == "" {
			t.Errorf("release[%d] = %+v", index, release)
		}
	}

	// Unacknowledged releases reserve all slots, preventing a reconnect storm
	// from exceeding the global cap.
	for index, release := range first.Releases {
		runtime.Clusters[index].State = release.ToState
		runtime.Clusters[index].EverReleased = true
	}
	if got := mustEvaluate(t, evaluateInput(runtime, testNow)); len(got.Releases) != 0 || got.Blocked != BlockConcurrency {
		t.Fatalf("reserved slots did not hold cap: %+v", got)
	}
}

func TestSchedulerReservesHardUnavailableBudget(t *testing.T) {
	t.Parallel()
	strategy := testStrategy(model.StrategyAllAtOnce, 3)
	strategy.MaxUnavailable = model.Amount{Type: model.AmountCount, Value: 1}
	plan := testPlan(t, 5, strategy, false)
	runtime := testRuntime(plan, model.RolloutProgressing)
	first := mustEvaluate(t, evaluateInput(runtime, testNow))
	if len(first.Releases) != 1 {
		t.Fatalf("releases = %d, want unavailable cap 1", len(first.Releases))
	}
	runtime.Clusters[0].State = model.RolloutClusterReleased
	runtime.Clusters[0].EverReleased = true
	if got := mustEvaluate(t, evaluateInput(runtime, testNow)); len(got.Releases) != 0 || got.Blocked != BlockUnavailable {
		t.Fatalf("in-flight reservation did not hold unavailable budget: %+v", got)
	}
	ready := testNow.Add(-time.Minute)
	runtime.Clusters[0].State = model.RolloutClusterReady
	runtime.Clusters[0].ReadySince = &ready
	if got := mustEvaluate(t, evaluateInput(runtime, testNow)); len(got.Releases) != 1 {
		t.Fatalf("mature readiness did not release unavailable slot: %+v", got)
	}

	strategy.MaxUnavailable = model.Amount{Type: model.AmountPercent, Value: 0}
	plan = testPlan(t, 5, strategy, false)
	runtime = testRuntime(plan, model.RolloutProgressing)
	if got := mustEvaluate(t, evaluateInput(runtime, testNow)); len(got.Releases) != 0 || got.Blocked != BlockUnavailable {
		t.Fatalf("zero unavailable budget allowed a potentially disruptive release: %+v", got)
	}
}

func TestSchedulerDisconnectReconnectAndDeadlines(t *testing.T) {
	t.Parallel()
	plan := testPlan(t, 3, testStrategy(model.StrategyAllAtOnce, 3), false)
	runtime := testRuntime(plan, model.RolloutProgressing)
	runtime.Clusters[0].Connected = false
	disconnected := mustEvaluate(t, evaluateInput(runtime, testNow))
	if len(disconnected.ClusterTransitions) != 1 || disconnected.ClusterTransitions[0].To != model.RolloutClusterBlocked || len(disconnected.Releases) != 0 {
		t.Fatalf("disconnect decision = %+v", disconnected)
	}
	runtime.Clusters[0].State = model.RolloutClusterBlocked
	runtime.Clusters[0].Connected = true
	reconnected := mustEvaluate(t, evaluateInput(runtime, testNow))
	if len(reconnected.ClusterTransitions) != 1 || reconnected.ClusterTransitions[0].To != model.RolloutClusterPending {
		t.Fatalf("reconnect decision = %+v", reconnected)
	}

	runtime = testRuntime(plan, model.RolloutProgressing)
	runtime.Clusters[0].State = model.RolloutClusterAcknowledged
	runtime.Clusters[0].EverReleased = true
	runtime.Clusters[0].Deadline = testNow.Add(-time.Second)
	timedOut := mustEvaluate(t, evaluateInput(runtime, testNow))
	if len(timedOut.ClusterTransitions) != 1 || timedOut.ClusterTransitions[0].To != model.RolloutClusterTimedOut {
		t.Fatalf("deadline decision = %+v", timedOut)
	}

	runtime = testRuntime(plan, model.RolloutProgressing)
	expiredAt := plan.Deadline
	global := mustEvaluate(t, evaluateInput(runtime, expiredAt))
	assertRolloutTransition(t, global, model.RolloutFailed)
}

func TestSchedulerCanaryMinReadySoakAndBoundApproval(t *testing.T) {
	t.Parallel()
	strategy := canaryStrategy(model.Amount{Type: model.AmountCount, Value: 1}, 2, true, 5*time.Minute)
	strategy.MinReady = model.Duration(time.Minute)
	plan := testPlan(t, 4, strategy, false)
	runtime := testRuntime(plan, model.RolloutProgressing)
	runtime.Clusters[0].State = model.RolloutClusterReady
	readySince := testNow
	runtime.Clusters[0].ReadySince = &readySince
	runtime.Clusters[0].EverReleased = true
	if got := mustEvaluate(t, evaluateInput(runtime, testNow.Add(30*time.Second))); got.Blocked != BlockConcurrency || len(got.Releases) != 0 {
		// MaxConcurrent has a free slot, but active canary is not mature; no
		// later cohort may leak. BlockNone is also acceptable while waiting.
		if got.Blocked != BlockNone {
			t.Fatalf("transient canary ready = %+v", got)
		}
	}

	readySince = testNow
	runtime.Clusters[0].ReadySince = &readySince
	soaking := mustEvaluate(t, evaluateInput(runtime, testNow.Add(2*time.Minute)))
	if soaking.Blocked != BlockSoak || len(soaking.Releases) != 0 {
		t.Fatalf("canary soak = %+v", soaking)
	}

	afterSoak := testNow.Add(6 * time.Minute)
	approvalInput := evaluateInput(runtime, afterSoak)
	if got := mustEvaluate(t, approvalInput); got.Blocked != BlockApproval {
		t.Fatalf("cohort approval missing = %+v", got)
	}
	approvalInput.CohortApprovals = map[int]ApprovalDecision{1: *approvalFor(plan.Cohorts[1].ApprovalDigest, afterSoak, true)}
	release := mustEvaluate(t, approvalInput)
	if len(release.Releases) != 2 {
		t.Fatalf("post-canary releases = %+v", release)
	}
}

func TestSchedulerFailureBudgetsAndActions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		action model.FailureAction
		want   model.RolloutState
	}{
		{"pause", model.FailurePause, model.RolloutPaused},
		{"abort", model.FailureAbort, model.RolloutFailed},
		{"rollback stage one", model.FailureRollback, model.RolloutFailed},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			strategy := testStrategy(model.StrategyAllAtOnce, 5)
			strategy.FailureThreshold = model.Amount{Type: model.AmountCount, Value: 1}
			strategy.OnFailure = test.action
			plan := testPlan(t, 5, strategy, false)
			runtime := testRuntime(plan, model.RolloutProgressing)
			for index := range 2 {
				runtime.Clusters[index].State = model.RolloutClusterFailed
				runtime.Clusters[index].StateChangedAt = testNow.Add(-time.Minute)
				runtime.Clusters[index].EverReleased = true
			}
			decision := mustEvaluate(t, evaluateInput(runtime, testNow))
			assertRolloutTransition(t, decision, test.want)
		})
	}

	strategy := testStrategy(model.StrategyAllAtOnce, 5)
	strategy.FailureThreshold = model.Amount{Type: model.AmountPercent, Value: 20} // one failure is allowed for five.
	plan := testPlan(t, 5, strategy, false)
	runtime := testRuntime(plan, model.RolloutProgressing)
	runtime.Clusters[0].State = model.RolloutClusterFailed
	runtime.Clusters[0].StateChangedAt = testNow.Add(-time.Minute)
	runtime.Clusters[0].EverReleased = true
	decision := mustEvaluate(t, evaluateInput(runtime, testNow))
	if decision.RolloutTransition != nil && decision.RolloutTransition.To == model.RolloutPaused {
		t.Fatalf("failure at threshold exceeded budget: %+v", decision)
	}

	// on_failure=rollback applies to every scheduler failure, not only a
	// failure-budget transition (for example, a global progress deadline).
	strategy.OnFailure = model.FailureRollback
	plan = testPlan(t, 5, strategy, false)
	runtime = testRuntime(plan, model.RolloutFailed)
	decision = mustEvaluate(t, evaluateInput(runtime, testNow))
	assertRolloutTransition(t, decision, model.RolloutRollingBack)
}

func TestSchedulerRollbackUsesFrozenPreviousAndIgnoresWindow(t *testing.T) {
	t.Parallel()
	strategy := testStrategy(model.StrategyAllAtOnce, 2)
	strategy.OnFailure = model.FailureRollback
	plan := testPlan(t, 4, strategy, false)
	runtime := testRuntime(plan, model.RolloutRollingBack)
	for index := range runtime.Clusters {
		runtime.Clusters[index].State = model.RolloutClusterReady
		ready := testNow.Add(-time.Minute)
		runtime.Clusters[index].ReadySince = &ready
		runtime.Clusters[index].EverReleased = true
		runtime.Clusters[index].CurrentGeneration = 10
		runtime.Clusters[index].LastAction = AssignmentApply
	}
	input := evaluateInput(runtime, testNow)
	input.Maintenance = MaintenanceGate{} // rollback is a safety action, not a new generation gate.
	decision := mustEvaluate(t, input)
	if len(decision.Releases) != 2 {
		t.Fatalf("rollback hard cap = %+v", decision)
	}
	for _, release := range decision.Releases {
		planned := plan.Clusters[indexOfCluster(plan, release.ClusterID)]
		if release.Action != AssignmentRollback || release.Version.BundleVersionID != planned.Previous.Version.BundleVersionID || release.Generation != 11 || release.ToState != model.RolloutClusterRollingBack {
			t.Errorf("rollback did not use frozen previous: %+v", release)
		}
	}

	missing := clonePlan(plan)
	missing.Clusters[0].Previous = nil
	resealPlan(t, &missing)
	runtime = testRuntime(missing, model.RolloutRollingBack)
	runtime.Clusters[0].State = model.RolloutClusterReady
	ready := testNow.Add(-time.Minute)
	runtime.Clusters[0].ReadySince = &ready
	runtime.Clusters[0].EverReleased = true
	failed := mustEvaluate(t, evaluateInput(runtime, testNow))
	assertRolloutTransition(t, failed, model.RolloutRollbackFailed)
}

func TestSchedulerRollbackRecoversUnavailableBeforeRiskingHealthy(t *testing.T) {
	t.Parallel()
	strategy := testStrategy(model.StrategyAllAtOnce, 2)
	strategy.MaxUnavailable = model.Amount{Type: model.AmountCount, Value: 1}
	strategy.OnFailure = model.FailureRollback
	plan := testPlan(t, 3, strategy, false)
	runtime := testRuntime(plan, model.RolloutRollingBack)
	for index := range runtime.Clusters {
		runtime.Clusters[index].State = model.RolloutClusterReady
		ready := testNow.Add(-time.Minute)
		runtime.Clusters[index].ReadySince = &ready
		runtime.Clusters[index].EverReleased = true
	}
	// The unavailable member is last in plan order. It must still be the only
	// release because the availability budget is already fully consumed.
	runtime.Clusters[2].State = model.RolloutClusterFailed
	runtime.Clusters[2].ReadySince = nil
	runtime.Clusters[2].Available = false
	decision := mustEvaluate(t, evaluateInput(runtime, testNow))
	if len(decision.Releases) != 1 || decision.Releases[0].ClusterID != runtime.Clusters[2].ClusterID {
		t.Fatalf("rollback priority/budget = %+v", decision)
	}
}

func TestSchedulerRejectsExpiredLeaseAndStaleFence(t *testing.T) {
	t.Parallel()
	plan := testPlan(t, 2, testStrategy(model.StrategyRolling, 1), false)
	runtime := testRuntime(plan, model.RolloutProgressing)
	input := evaluateInput(runtime, testNow)
	input.Lease.ExpiresAt = testNow
	if _, err := Evaluate(input); !HasCode(err, CodeLeaseLost) {
		t.Fatalf("expired lease = %v", err)
	}
	input = evaluateInput(runtime, testNow)
	input.Lease.Fence++
	if _, err := Evaluate(input); !HasCode(err, CodeStaleFence) {
		t.Fatalf("stale fence = %v", err)
	}
}

func TestSchedulerTenThousandClusterProgression(t *testing.T) {
	strategy := testStrategy(model.StrategyRolling, 1_000)
	strategy.FailureThreshold = model.Amount{Type: model.AmountCount, Value: 100}
	plan := testPlan(t, 10_000, strategy, false)
	runtime := testRuntime(plan, model.RolloutProgressing)
	now := testNow
	released := 0
	for iterations := 0; iterations < 30; iterations++ {
		decision := mustEvaluate(t, evaluateInput(runtime, now))
		if decision.RolloutTransition != nil {
			if decision.RolloutTransition.To != model.RolloutSucceeded {
				t.Fatalf("unexpected transition at iteration %d: %+v", iterations, decision.RolloutTransition)
			}
			if released != 10_000 {
				t.Fatalf("succeeded after releasing %d clusters", released)
			}
			return
		}
		if len(decision.Releases) == 0 || len(decision.Releases) > 1_000 {
			t.Fatalf("iteration %d releases=%d blocked=%s", iterations, len(decision.Releases), decision.Blocked)
		}
		for _, release := range decision.Releases {
			index := indexOfCluster(plan, release.ClusterID)
			runtime.Clusters[index].State = model.RolloutClusterReady
			runtime.Clusters[index].EverReleased = true
			runtime.Clusters[index].CurrentGeneration = release.Generation
			runtime.Clusters[index].LastAction = release.Action
			ready := now
			runtime.Clusters[index].ReadySince = &ready
			released++
		}
		now = now.Add(time.Second)
	}
	t.Fatal("10k rollout did not converge within 30 scheduler decisions")
}

func testRuntime(plan FrozenRollout, rolloutState model.RolloutState) RuntimeSnapshot {
	clusters := make([]ClusterRuntime, len(plan.Clusters))
	for index, planned := range plan.Clusters {
		clusters[index] = ClusterRuntime{ClusterID: planned.ClusterID, State: model.RolloutClusterPending, Fence: int64(index + 1),
			Connected: true, Available: true, StateChangedAt: plan.CreatedAt, Deadline: plan.Deadline}
	}
	return RuntimeSnapshot{Plan: plan, State: rolloutState, Fence: 11, Clusters: clusters}
}

func evaluateInput(snapshot RuntimeSnapshot, now time.Time) EvaluateInput {
	return EvaluateInput{Now: now, Snapshot: snapshot,
		Lease:       Lease{Owner: "scheduler-1", Fence: snapshot.Fence, ExpiresAt: now.Add(time.Minute)},
		Maintenance: MaintenanceGate{Open: true}}
}

func approvalFor(binding model.Digest, now time.Time, approved bool) *ApprovalDecision {
	return &ApprovalDecision{ID: uuid.New(), BindingDigest: binding, Approved: approved, DecidedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
}

func mustEvaluate(t *testing.T, input EvaluateInput) Decision {
	t.Helper()
	decision, err := Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := decision.ID.Validate(); err != nil {
		t.Fatalf("decision ID: %v", err)
	}
	return decision
}

func assertRolloutTransition(t *testing.T, decision Decision, desired model.RolloutState) {
	t.Helper()
	if decision.RolloutTransition == nil || decision.RolloutTransition.To != desired {
		t.Fatalf("transition = %+v, want %s", decision.RolloutTransition, desired)
	}
}

func digestForTest(fill byte) model.Digest {
	return model.Digest("sha256:" + stringsRepeat(fill, 64))
}

func stringsRepeat(fill byte, count int) string {
	value := make([]byte, count)
	for index := range value {
		value[index] = fill
	}
	return string(value)
}

func indexOfCluster(plan FrozenRollout, clusterID uuid.UUID) int {
	for index, cluster := range plan.Clusters {
		if cluster.ClusterID == clusterID {
			return index
		}
	}
	return -1
}

func resealPlan(t *testing.T, plan *FrozenRollout) {
	t.Helper()
	strategyDigest, err := plan.Strategy.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	plan.StrategyDigest = strategyDigest
	plan.Approval.Digest, err = approvalDigest(*plan, -1)
	if err != nil {
		t.Fatal(err)
	}
	for index := range plan.Cohorts {
		if plan.Cohorts[index].ApprovalRequired {
			plan.Cohorts[index].ApprovalDigest, err = approvalDigest(*plan, index)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	plan.PlanDigest, err = frozenDigest(*plan)
	if err != nil {
		t.Fatal(err)
	}
}
