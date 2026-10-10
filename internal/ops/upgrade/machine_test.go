package upgrade

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type recordingStore struct {
	states      []Checkpoint
	failPending Stage
}

func (s *recordingStore) Save(_ context.Context, state Checkpoint) error {
	if state.Pending == s.failPending && s.failPending != "" {
		return errors.New("synthetic checkpoint failure")
	}
	state.Completed = append([]Stage(nil), state.Completed...)
	s.states = append(s.states, state)
	return nil
}

type childFailure int

func (e childFailure) Error() string { return "synthetic child failure" }
func (e childFailure) ExitCode() int { return int(e) }

type recordingDriver struct {
	calls                 []Stage
	fail                  Stage
	cancelAt              Stage
	cancel                context.CancelFunc
	fenceError            bool
	loseAfterApply        bool
	reconciles            int
	reconcileError        bool
	unknownMutation       bool
	unknownReconciliation bool
	cleanupContextExpired bool
	store                 *recordingStore
}

func (d *recordingDriver) Fence(ctx context.Context, _ Identity, _ *DisposableLease) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if d.fenceError {
		return errors.New("synthetic owner changed")
	}
	return nil
}
func (d *recordingDriver) Step(ctx context.Context, step Stage, _ Plan) (Outcome, error) {
	d.calls = append(d.calls, step)
	if (step == Rollback || step == Cleanup) && ctx.Err() != nil {
		d.cleanupContextExpired = true
	}
	if step != Rollback && step != Cleanup && (len(d.store.states) == 0 || d.store.states[len(d.store.states)-1].Pending != step) {
		return Outcome{}, errors.New("step executed before durable intent")
	}
	if step == d.cancelAt && d.cancel != nil {
		d.cancel()
	}
	outcome := Outcome{Known: true, Applied: step == Apply, Replaced: step == ReplaceDatabase}
	if step == Apply && d.loseAfterApply {
		d.fenceError = true
	}
	if d.unknownMutation && (step == Apply || step == ReplaceDatabase) {
		return Outcome{}, childFailure(43)
	}
	if step == d.fail {
		return outcome, childFailure(43)
	}
	return outcome, nil
}
func (d *recordingDriver) Reconcile(context.Context, Checkpoint) (Outcome, error) {
	d.reconciles++
	if d.reconcileError {
		return Outcome{}, errors.New("synthetic uncertain state")
	}
	return Outcome{Known: !d.unknownReconciliation, Applied: true}, nil
}

func (d *recordingDriver) Cleanup(ctx context.Context, plan Plan, policy CleanupPolicy) error {
	if !policy.OwnedEphemeralOnly || !policy.PreserveBackup || !policy.PreserveCheckpoint {
		return errors.New("recovery retention policy missing")
	}
	_, err := d.Step(ctx, Cleanup, plan)
	return err
}

func TestUnknownMutationReconcilesBeforeRollbackAndPreservesRecoveryIfUnresolved(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unresolved_%t", unknown), func(t *testing.T) {
			machine, driver, _ := testMachine()
			driver.unknownMutation, driver.unknownReconciliation = true, unknown
			result := machine.Run(context.Background(), testPlan(), nil)
			if result.ExitCode != 43 || driver.reconciles != 1 {
				t.Fatalf("unknown mutation was not reconciled: %+v", result)
			}
			if unknown {
				if result.Result != "outcome_unknown" || result.RollbackAttempted {
					t.Fatalf("uncertain outcome reclassified: %+v", result)
				}
				for _, stage := range driver.calls {
					if stage == Cleanup || stage == Rollback {
						t.Fatal("uncertain mutation discarded recovery handles")
					}
				}
			} else if !result.RollbackAttempted {
				t.Fatal("observed applied state did not trigger bounded rollback")
			}
		})
	}
}

func TestUnknownMutationWithRecreatedIdentityDoesNotCleanRecovery(t *testing.T) {
	machine, driver, _ := testMachine()
	driver.unknownMutation = true
	// A custom fence changes immediately after mutation intent is recorded.
	driver.loseAfterApply = true
	result := machine.Run(context.Background(), testPlan(), nil)
	if result.Result != "outcome_unknown" || result.RollbackAttempted {
		t.Fatalf("changed identity accepted: %+v", result)
	}
	for _, stage := range driver.calls {
		if stage == Cleanup || stage == Rollback {
			t.Fatal("changed identity permitted recovery cleanup")
		}
	}
}
func testPlan() Plan {
	return Plan{Target: "v1.2.2", Previous: "v1.2.1", Mode: Execute, Identity: Identity{Context: "owned-context", APIServer: "https://127.0.0.1:6443", Namespace: "owned-namespace", NamespaceUID: "owned-namespace-uid", Release: "astronomer", ReleaseUID: "owned-release-uid"}, Timeout: time.Minute, CleanupTimeout: 30 * time.Second}
}
func testMachine() (Machine, *recordingDriver, *recordingStore) {
	store := &recordingStore{}
	driver := &recordingDriver{store: store}
	return Machine{Driver: driver, Store: store}, driver, store
}

func TestPreviewRetainsBackupAndDryRunWithoutApplying(t *testing.T) {
	machine, driver, _ := testMachine()
	plan := testPlan()
	plan.Mode = Preview
	result := machine.Run(context.Background(), plan, nil)
	if result.ExitCode != 0 || !reflect.DeepEqual(driver.calls, []Stage{Verify, Backup, DryRun, Cleanup}) {
		t.Fatalf("preview sequence/result differs: %+v %v", result, driver.calls)
	}
}
func TestApplyObserveAndOwnedCleanupOrdering(t *testing.T) {
	machine, driver, store := testMachine()
	result := machine.Run(context.Background(), testPlan(), nil)
	if result.ExitCode != 0 || !reflect.DeepEqual(driver.calls, []Stage{Verify, Backup, DryRun, Apply, Observe, Cleanup}) {
		t.Fatalf("upgrade sequence/result differs: %+v %v", result, driver.calls)
	}
	if !store.states[len(store.states)-1].Applied {
		t.Fatal("acknowledged apply not checkpointed")
	}
}
func TestStageFailuresPreserveChildExitAndRollbackAcknowledgedApply(t *testing.T) {
	for _, stage := range []Stage{Verify, Backup, DryRun, Apply, Observe} {
		t.Run(string(stage), func(t *testing.T) {
			machine, driver, _ := testMachine()
			driver.fail = stage
			result := machine.Run(context.Background(), testPlan(), nil)
			applied := stage == Apply || stage == Observe
			if result.ExitCode != 43 || result.FailedStage != stage || result.RollbackAttempted != applied || driver.calls[len(driver.calls)-1] != Cleanup {
				t.Fatalf("failed stage contract differs: %+v %v", result, driver.calls)
			}
		})
	}
}
func TestFailedCheckpointDoesNotLaunchMutation(t *testing.T) {
	machine, driver, store := testMachine()
	store.failPending = Apply
	result := machine.Run(context.Background(), testPlan(), nil)
	if result.ExitCode == 0 || result.FailedStage != Apply || result.RollbackAttempted {
		t.Fatalf("checkpoint failure outcome differs: %+v", result)
	}
	for _, stage := range driver.calls {
		if stage == Apply {
			t.Fatal("apply launched after failed intent persistence")
		}
	}
}
func TestCancellationAfterApplyUsesFreshFiniteRollbackAndCleanup(t *testing.T) {
	machine, driver, _ := testMachine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	driver.cancelAt, driver.cancel = Apply, cancel
	result := machine.Run(ctx, testPlan(), nil)
	if result.ExitCode != 130 || !result.RollbackAttempted || result.CleanupFailed || driver.cleanupContextExpired {
		t.Fatalf("cancel cleanup contract differs: %+v", result)
	}
}
func TestLostIdentityDoesNotRollbackOrCleanUnownedRelease(t *testing.T) {
	machine, driver, _ := testMachine()
	driver.loseAfterApply = true
	result := machine.Run(context.Background(), testPlan(), nil)
	if result.ExitCode == 0 || !result.RollbackFailed || !result.CleanupFailed {
		t.Fatalf("lost identity contract differs: %+v", result)
	}
	for _, stage := range driver.calls {
		if stage == Rollback || stage == Cleanup {
			t.Fatal("mutated after ownership fence failed")
		}
	}
}
func TestRollbackFailureRetainsOriginalChildExit(t *testing.T) {
	machine, driver, _ := testMachine()
	driver.fail = Rollback
	// A cancelled observation supplies the original failure, then rollback fails.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	driver.cancelAt, driver.cancel = Observe, cancel
	result := machine.Run(ctx, testPlan(), nil)
	if result.ExitCode != 130 || !result.RollbackFailed || driver.calls[len(driver.calls)-1] != Cleanup {
		t.Fatalf("rollback failure replaced original outcome: %+v", result)
	}
}
func TestCleanupFailureFailsSuccessfulRun(t *testing.T) {
	machine, driver, _ := testMachine()
	driver.fail = Cleanup
	result := machine.Run(context.Background(), testPlan(), nil)
	if result.ExitCode == 0 || result.Result != "cleanup_failed" {
		t.Fatalf("cleanup failure hidden: %+v", result)
	}
}
func TestResumeObservesPendingApplyWithoutReplayingAnyStage(t *testing.T) {
	machine, driver, _ := testMachine()
	plan := testPlan()
	state := Checkpoint{SchemaVersion: 1, Target: plan.Target, PlanDigest: planDigest(plan), Identity: plan.Identity, Completed: []Stage{Verify, Backup, DryRun}, Pending: Apply}
	result := machine.Run(context.Background(), plan, &state)
	if result.Result != "resume_assessment_required" || len(driver.calls) != 0 || driver.reconciles != 1 {
		t.Fatalf("uncertain apply replayed: %+v %v", result, driver.calls)
	}
	driver.reconcileError = true
	result = machine.Run(context.Background(), plan, &state)
	if result.Result != "outcome_unknown" || len(driver.calls) != 0 {
		t.Fatalf("uncertain observation accepted: %+v", result)
	}
}
func TestResumeRejectsChangedModeLeaseIdentityAndStageOrder(t *testing.T) {
	plan := testPlan()
	state := Checkpoint{SchemaVersion: 1, Target: plan.Target, PlanDigest: planDigest(plan), Identity: plan.Identity, Completed: []Stage{Verify, Backup, DryRun}, Pending: Apply}
	for _, kind := range []string{"mode", "identity", "order", "lease"} {
		t.Run(kind, func(t *testing.T) {
			machine, driver, _ := testMachine()
			p, s := plan, state
			s.Completed = append([]Stage(nil), state.Completed...)
			switch kind {
			case "mode":
				p.Mode = Preview
			case "identity":
				s.Identity.NamespaceUID = "recreated"
			case "order":
				s.Completed[1] = DryRun
			case "lease":
				p.Replace = &DisposableLease{NamespaceUID: p.Identity.NamespaceUID, DatabasePodUID: "owned-pod", DatabaseName: "live", RestoreDatabaseName: "restored", Owner: "owned"}
			}
			result := machine.Run(context.Background(), p, &s)
			if result.ExitCode == 0 || driver.reconciles != 0 || len(driver.calls) != 0 {
				t.Fatalf("changed checkpoint accepted: %+v", result)
			}
		})
	}
}
func TestReplacementRequiresExplicitLeaseAndPrecedesApply(t *testing.T) {
	machine, driver, _ := testMachine()
	plan := testPlan()
	plan.Replace = &DisposableLease{NamespaceUID: plan.Identity.NamespaceUID, DatabasePodUID: "owned-pod", DatabaseName: "live", RestoreDatabaseName: "restored", Owner: "owned"}
	result := machine.Run(context.Background(), plan, nil)
	if result.ExitCode != 0 || !reflect.DeepEqual(driver.calls, []Stage{Verify, Backup, DryRun, ReplaceDatabase, Apply, Observe, Cleanup}) {
		t.Fatalf("replacement order differs: %+v %v", result, driver.calls)
	}
	plan.Replace.NamespaceUID = "other"
	machine, driver, _ = testMachine()
	result = machine.Run(context.Background(), plan, nil)
	if result.ExitCode == 0 || len(driver.calls) != 0 {
		t.Fatal("unleased replacement allowed")
	}
}

func TestCheckpointRejectsContradictoryAcknowledgementAndRollbackFlags(t *testing.T) {
	plan := testPlan()
	valid := Checkpoint{SchemaVersion: 1, Target: plan.Target, PlanDigest: planDigest(plan), Identity: plan.Identity, Completed: []Stage{Verify, Backup, DryRun}}
	for _, kind := range []string{"completed_apply_without_ack", "replacement_before_stage", "rollback_before_apply"} {
		t.Run(kind, func(t *testing.T) {
			state := valid
			state.Completed = append([]Stage(nil), valid.Completed...)
			switch kind {
			case "completed_apply_without_ack":
				state.Completed = append(state.Completed, Apply)
			case "replacement_before_stage":
				state.Replaced = true
			case "rollback_before_apply":
				state.RollbackAttempted = true
			}
			if validateCheckpoint(state, plan) == nil {
				t.Fatal("contradictory checkpoint accepted")
			}
		})
	}
}
