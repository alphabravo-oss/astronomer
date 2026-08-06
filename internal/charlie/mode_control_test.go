package charlie

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type modeCeilingRolloutFunc func(context.Context, ModeCeilingTarget) error

func (f modeCeilingRolloutFunc) Reconcile(ctx context.Context, target ModeCeilingTarget) error {
	return f(ctx, target)
}

type bridgeWithoutModeCeiling struct{ delegate *fakeModeBridge }

func (b bridgeWithoutModeCeiling) SetMode(ctx context.Context, mode Mode, revision int64) (ModeState, error) {
	return b.delegate.SetMode(ctx, mode, revision)
}

func (b bridgeWithoutModeCeiling) Status(ctx context.Context) (ModeState, error) {
	return b.delegate.Status(ctx)
}

type fakeModeStore struct {
	state          ModeState
	error          error
	emergencyCalls int
	clearCalls     int
	requestCalls   int
	verifyCalls    int
}

func (f *fakeModeStore) LoadModeState(context.Context) (ModeState, error) { return f.state, f.error }
func (f *fakeModeStore) SetRequestedMode(_ context.Context, connectionID string, mode Mode, expected int64) (ModeState, error) {
	f.requestCalls++
	if f.error != nil || connectionID != f.state.ConnectionID || expected != f.state.Revision {
		return ModeState{}, errors.New("CAS failed")
	}
	f.state.Requested = mode
	f.state.Revision++
	return f.state, nil
}
func (f *fakeModeStore) SetVerifiedMode(_ context.Context, connectionID string, mode Mode, expected, next int64, digest string) (ModeState, error) {
	f.verifyCalls++
	if f.error != nil || connectionID != f.state.ConnectionID || expected != f.state.Revision || next < expected {
		return ModeState{}, errors.New("verify CAS failed")
	}
	f.state.Verified = mode
	f.state.Revision = next
	f.state.DisclosureDigest = digest
	return f.state, nil
}

func TestModeReconcilePersistsNewAuthoritativeSnapshotWithoutRaisingLocalCeiling(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{state: ModeState{Active: true, Requested: ModeAuto, Verified: ModeAuto, Revision: 9, DisclosureDigest: "disclosure-b"}}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})

	got, err := controller.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Requested != ModeReadOnly || got.Verified != ModeAuto || got.Revision != 9 || got.DisclosureDigest != "disclosure-b" {
		t.Fatalf("unexpected reconciled state: %+v", got)
	}
	if EffectiveMode(got.Requested, got.Verified, got.EmergencyDisabled) != ModeReadOnly {
		t.Fatal("remote reconciliation raised the local authority ceiling")
	}
}

func TestModeReconcileCentralSuspensionClearsDisclosure(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{state: ModeState{Active: false, Requested: ModeReadOnly, Verified: ModeReadOnly, Revision: 8, DisclosureDigest: "stale-disclosure"}}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})

	got, err := controller.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Verified != ModeDisabled || got.DisclosureDigest != "" || got.Requested != ModeReadOnly || got.Revision != 8 {
		t.Fatalf("central suspension was not imported fail-closed: %+v", got)
	}
}

func TestModeReconcileRejectsRevisionDrift(t *testing.T) {
	for name, remote := range map[string]ModeState{
		"stale":                  {Active: true, Verified: ModeReadOnly, Revision: 3, DisclosureDigest: "disclosure-a"},
		"same revision mismatch": {Active: true, Verified: ModeApproval, Revision: 4, DisclosureDigest: "disclosure-b"},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeModeStore{state: activeModeState()}
			controller, _ := NewModeController(store, &fakeModeBridge{state: remote}, &authorityAuditFake{})
			if _, err := controller.Reconcile(context.Background()); err == nil || store.verifyCalls != 0 {
				t.Fatal("revision drift was accepted")
			}
		})
	}
}

func TestModeReconcileDoesNotContactRemoteDuringEmergencyDisable(t *testing.T) {
	state := activeModeState()
	state.EmergencyDisabled = true
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{error: errors.New("must not be called")}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	got, err := controller.Reconcile(context.Background())
	if err != nil || !got.EmergencyDisabled {
		t.Fatalf("emergency state was not preserved: state=%+v err=%v", got, err)
	}
}

func TestModeReconcileCentralDowngradeCancelsAndDrainsAdmittedWrite(t *testing.T) {
	state := activeModeState()
	state.Requested, state.Verified = ModeAuto, ModeAuto
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{state: ModeState{ConnectionID: state.ConnectionID, Active: true, Verified: ModeReadOnly, Revision: 5, DisclosureDigest: "disclosure-b"}}
	controller, _ := NewModeController(store, bridgeWithoutModeCeiling{delegate: bridge}, &authorityAuditFake{})
	fence := NewWriteFence()
	controller.SetWriteFence(fence)
	writeCtx, releaseWrite, err := fence.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() {
		<-writeCtx.Done()
		releaseWrite()
		close(drained)
	}()
	controller.SetModeCeilingRollout(modeCeilingRolloutFunc(func(_ context.Context, target ModeCeilingTarget) error {
		select {
		case <-drained:
		default:
			t.Fatal("central downgrade rolled out before the admitted write drained")
		}
		if target.ExpectedRequested != ModeAuto || target.ExpectedRevision != 5 || target.Desired != ModeReadOnly ||
			!fence.State().Closed || !fence.State().Drained || store.verifyCalls != 1 || store.state.Verified != ModeReadOnly {
			t.Fatalf("downgrade ordering fence=%+v verify_calls=%d", fence.State(), store.verifyCalls)
		}
		return nil
	}))

	got, err := controller.Reconcile(t.Context())
	if err != nil || got.Verified != ModeReadOnly || !fence.State().Closed || !fence.State().Drained {
		t.Fatalf("central downgrade did not remain drained and closed: state=%+v fence=%+v err=%v", got, fence.State(), err)
	}
}

func TestModeReconcileUpwardRestorationRequiresFreshReadbackBeforeOpen(t *testing.T) {
	state := activeModeState()
	state.Requested, state.Verified = ModeAuto, ModeApproval
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{state: ModeState{ConnectionID: state.ConnectionID, Active: true, Verified: ModeAuto, Revision: 5, DisclosureDigest: "disclosure-b"}}
	controller, _ := NewModeController(store, bridgeWithoutModeCeiling{delegate: bridge}, &authorityAuditFake{})
	controller.writes.Close()
	controller.SetModeCeilingRollout(modeCeilingRolloutFunc(func(_ context.Context, target ModeCeilingTarget) error {
		if target.Desired != ModeAuto || target.ExpectedRevision != 4 || !controller.writes.State().Closed || store.verifyCalls != 0 || bridge.statusCalls != 1 {
			t.Fatalf("upward rollout occurred outside the closed pre-CAS boundary: target=%+v fence=%+v verifies=%d status=%d", target, controller.writes.State(), store.verifyCalls, bridge.statusCalls)
		}
		return nil
	}))

	got, err := controller.Reconcile(t.Context())
	if err != nil || got.Verified != ModeAuto || bridge.statusCalls != 2 || controller.writes.State().Closed {
		t.Fatalf("upward restoration opened without exact readback: state=%+v statuses=%d fence=%+v err=%v", got, bridge.statusCalls, controller.writes.State(), err)
	}
}

func TestModeReconcileRolloutFailureLeavesPriorStateAndFenceClosed(t *testing.T) {
	state := activeModeState()
	state.Requested, state.Verified = ModeAuto, ModeApproval
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{state: ModeState{ConnectionID: state.ConnectionID, Active: true, Verified: ModeAuto, Revision: 5, DisclosureDigest: "disclosure-b"}}
	controller, _ := NewModeController(store, bridgeWithoutModeCeiling{delegate: bridge}, &authorityAuditFake{})
	controller.SetModeCeilingRollout(modeCeilingRolloutFunc(func(context.Context, ModeCeilingTarget) error {
		return errors.New("partial rollout")
	}))

	got, err := controller.Reconcile(t.Context())
	if err == nil || got != state || store.verifyCalls != 0 || !controller.writes.State().Closed {
		t.Fatalf("rollout failure changed authority: state=%+v verifies=%d fence=%+v err=%v", got, store.verifyCalls, controller.writes.State(), err)
	}
}

func TestModeReconcileRejectsLocalStateChangeAfterRollout(t *testing.T) {
	state := activeModeState()
	state.Requested, state.Verified = ModeAuto, ModeApproval
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{state: ModeState{ConnectionID: state.ConnectionID, Active: true, Verified: ModeAuto, Revision: 5, DisclosureDigest: "disclosure-b"}}
	controller, _ := NewModeController(store, bridgeWithoutModeCeiling{delegate: bridge}, &authorityAuditFake{})
	controller.SetModeCeilingRollout(modeCeilingRolloutFunc(func(context.Context, ModeCeilingTarget) error {
		// Model an HA controller winning a durable transition while this replica
		// was reconciling the workload ceiling.
		store.state.EmergencyDisabled = true
		store.state.Requested = ModeDisabled
		store.state.Revision++
		return nil
	}))

	if _, err := controller.Reconcile(t.Context()); err == nil || store.verifyCalls != 0 || !controller.writes.State().Closed {
		t.Fatalf("stale post-rollout state was persisted: verifies=%d fence=%+v err=%v", store.verifyCalls, controller.writes.State(), err)
	}
}

func TestModeReconcileDowngradeFailuresLeaveDurableLowerAuthority(t *testing.T) {
	for _, test := range []struct {
		name        string
		auditor     AuthorityMutationAuditor
		rolloutErr  error
		readbackErr error
		wantErr     bool
	}{
		{name: "audit unavailable", auditor: &authorityAuditFake{err: errors.New("audit unavailable")}},
		{name: "rollout unavailable", auditor: &authorityAuditFake{}, rolloutErr: errors.New("partial rollout"), wantErr: true},
		{name: "central readback unavailable", auditor: &authorityAuditFake{}, readbackErr: errors.New("central unavailable"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := activeModeState()
			state.Requested, state.Verified = ModeAuto, ModeAuto
			store := &fakeModeStore{state: state}
			remote := ModeState{ConnectionID: state.ConnectionID, Active: true, Verified: ModeReadOnly, Revision: 5, DisclosureDigest: "disclosure-b"}
			bridge := &fakeModeBridge{state: remote}
			if test.readbackErr != nil {
				bridge.statusStates = []ModeState{remote}
				bridge.statusErrors = []error{nil, test.readbackErr}
			}
			controller, _ := NewModeController(store, bridgeWithoutModeCeiling{delegate: bridge}, test.auditor)
			controller.SetModeCeilingRollout(modeCeilingRolloutFunc(func(_ context.Context, target ModeCeilingTarget) error {
				if store.state.Verified != ModeReadOnly || store.state.Revision != 5 || store.verifyCalls != 1 || target.Desired != ModeReadOnly {
					t.Fatalf("signed downgrade was not durable before rollout: state=%+v target=%+v", store.state, target)
				}
				return test.rolloutErr
			}))

			got, err := controller.Reconcile(t.Context())
			if (err != nil) != test.wantErr || got.Verified != ModeReadOnly || got.Revision != 5 ||
				store.state.Verified != ModeReadOnly || !controller.writes.State().Closed {
				t.Fatalf("downgrade failure reopened stale authority: state=%+v durable=%+v fence=%+v err=%v", got, store.state, controller.writes.State(), err)
			}
		})
	}
}

func TestModeReconcileSameRevisionReadOnlyReprovesAllReplicaCeiling(t *testing.T) {
	state := activeModeState()
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{state: state}
	controller, _ := NewModeController(store, bridgeWithoutModeCeiling{delegate: bridge}, &authorityAuditFake{})
	rollouts := 0
	controller.SetModeCeilingRollout(modeCeilingRolloutFunc(func(_ context.Context, target ModeCeilingTarget) error {
		rollouts++
		if target.ExpectedRevision != state.Revision || target.Desired != ModeReadOnly {
			t.Fatalf("same-revision lower ceiling target=%+v", target)
		}
		return nil
	}))
	if _, err := controller.Reconcile(t.Context()); err != nil || rollouts != 1 || bridge.statusCalls != 2 || !controller.writes.State().Closed {
		t.Fatalf("same-revision lower ceiling was not reproved: rollouts=%d statuses=%d fence=%+v err=%v", rollouts, bridge.statusCalls, controller.writes.State(), err)
	}
}

func TestModeReconcileSecondReplicaActionDeniedAfterDowngradeLockRelease(t *testing.T) {
	state := activeModeState()
	state.Requested, state.Verified = ModeAuto, ModeAuto
	store := &fakeModeStore{state: state}
	remote := ModeState{ConnectionID: state.ConnectionID, Active: true, Verified: ModeReadOnly, Revision: 5, DisclosureDigest: "disclosure-b"}
	controller, _ := NewModeController(store, &fakeModeBridge{state: remote}, &authorityAuditFake{})
	if _, err := controller.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	facts := allowedWriteFacts(EffectiveMode(store.state.Requested, store.state.Verified, store.state.EmergencyDisabled))
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{verified: true}
	guard, key := newTestActionGuard(t, authority, receipts, executor)
	result := guard.Execute(t.Context(), signedTestAction(t, key, "astronomer.queue.retry_task", map[string]any{
		"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a",
	}))
	if result.Code != DeniedReadOnlyWrite || executor.calls != 0 || len(receipts.transitions) != 0 {
		t.Fatalf("second-replica action used stale pre-downgrade authority: result=%+v executes=%d receipts=%v", result, executor.calls, receipts.transitions)
	}
}

type concurrentModeStore struct {
	mu              sync.Mutex
	state           ModeState
	verifyAttempts  int
	verifySuccesses int
}

func (s *concurrentModeStore) LoadModeState(context.Context) (ModeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}

func (s *concurrentModeStore) SetVerifiedMode(_ context.Context, connectionID string, mode Mode, expected, next int64, digest string) (ModeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verifyAttempts++
	if connectionID != s.state.ConnectionID || expected != s.state.Revision || next <= expected {
		return ModeState{}, errors.New("verify CAS failed")
	}
	s.state.Verified, s.state.Revision, s.state.DisclosureDigest = mode, next, digest
	s.verifySuccesses++
	return s.state, nil
}

func (s *concurrentModeStore) SetRequestedMode(context.Context, string, Mode, int64) (ModeState, error) {
	return ModeState{}, errors.New("not used")
}
func (s *concurrentModeStore) SetEmergencyDisabled(context.Context, string, string) (ModeState, error) {
	return ModeState{}, errors.New("not used")
}
func (s *concurrentModeStore) ClearEmergencyDisabled(context.Context, string, string) (ModeState, error) {
	return ModeState{}, errors.New("not used")
}

type staticModeBridge struct{ state ModeState }

func (b staticModeBridge) SetMode(context.Context, Mode, int64) (ModeState, error) {
	return b.state, nil
}
func (b staticModeBridge) Status(context.Context) (ModeState, error) { return b.state, nil }

func TestModeReconcileConcurrentControllersUseDurableCASAndFailClosed(t *testing.T) {
	state := activeModeState()
	state.Requested, state.Verified = ModeAuto, ModeApproval
	store := &concurrentModeStore{state: state}
	remote := ModeState{ConnectionID: state.ConnectionID, Active: true, Verified: ModeAuto, Revision: 5, DisclosureDigest: "disclosure-b"}
	arrived := sync.WaitGroup{}
	arrived.Add(2)
	releaseRollout := make(chan struct{})
	controllers := make([]*ModeController, 2)
	for index := range controllers {
		controllers[index], _ = NewModeController(store, staticModeBridge{state: remote}, &authorityAuditFake{})
		controllers[index].SetModeCeilingRollout(modeCeilingRolloutFunc(func(context.Context, ModeCeilingTarget) error {
			arrived.Done()
			<-releaseRollout
			return nil
		}))
	}
	results := make(chan error, 2)
	for _, controller := range controllers {
		go func(controller *ModeController) {
			_, err := controller.Reconcile(t.Context())
			results <- err
		}(controller)
	}
	arrived.Wait()
	close(releaseRollout)
	errorsSeen := 0
	for range controllers {
		if err := <-results; err != nil {
			errorsSeen++
		}
	}
	store.mu.Lock()
	successes, final := store.verifySuccesses, store.state
	store.mu.Unlock()
	openFences := 0
	for _, controller := range controllers {
		if !controller.writes.State().Closed {
			openFences++
		}
	}
	if successes != 1 || errorsSeen != 1 || final.Verified != ModeAuto || final.Revision != 5 || openFences != 1 {
		t.Fatalf("concurrent reconcile did not fail closed: successes=%d errors=%d state=%+v open_fences=%d", successes, errorsSeen, final, openFences)
	}
}

func TestModeReconcilerStopsWithContext(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{state: activeModeState()}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { controller.Run(ctx, time.Millisecond); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("mode reconciler did not stop")
	}
}

func TestModeReconcilerCanceledBeforeStartCreatesNoTimerOrRemoteRead(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{state: activeModeState()}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	timers := 0
	controller.ticker = func(time.Duration) runtimeTicker {
		timers++
		return &fakeRuntimeTicker{channel: make(chan time.Time)}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	controller.Run(ctx, time.Second)
	if timers != 0 || bridge.statusCalls != 0 {
		t.Fatalf("timers=%d remote_calls=%d", timers, bridge.statusCalls)
	}
}
func (f *fakeModeStore) SetEmergencyDisabled(_ context.Context, connectionID, _ string) (ModeState, error) {
	if f.error != nil || connectionID != f.state.ConnectionID {
		return ModeState{}, errors.New("disable failed")
	}
	f.emergencyCalls++
	f.state.EmergencyDisabled = true
	f.state.Requested = ModeDisabled
	f.state.Revision++
	return f.state, nil
}
func (f *fakeModeStore) ClearEmergencyDisabled(_ context.Context, connectionID, _ string) (ModeState, error) {
	if f.error != nil || connectionID != f.state.ConnectionID {
		return ModeState{}, errors.New("clear failed")
	}
	f.clearCalls++
	f.state.EmergencyDisabled = false
	f.state.Requested = ModeDisabled
	f.state.Verified = ModeDisabled
	f.state.Revision++
	return f.state, nil
}

type fakeModeBridge struct {
	state        ModeState
	error        error
	setError     error
	statusStates []ModeState
	statusErrors []error
	calls        int
	statusCalls  int
	lastMode     Mode
}

func (f *fakeModeBridge) Reconcile(_ context.Context, target ModeCeilingTarget) error {
	f.lastMode = target.Desired
	return nil
}

func (f *fakeModeBridge) SetMode(_ context.Context, mode Mode, revision int64) (ModeState, error) {
	f.calls++
	f.lastMode = mode
	if f.setError != nil {
		return ModeState{}, f.setError
	}
	if f.error != nil {
		return ModeState{}, f.error
	}
	f.state.Requested = mode
	f.state.Verified = mode
	f.state.Revision = revision + 1
	if f.state.DisclosureDigest == "" {
		f.state.DisclosureDigest = "disclosure-a"
	}
	return f.state, nil
}
func (f *fakeModeBridge) Status(context.Context) (ModeState, error) {
	index := f.statusCalls
	f.statusCalls++
	if index < len(f.statusErrors) && f.statusErrors[index] != nil {
		return ModeState{}, f.statusErrors[index]
	}
	if index < len(f.statusStates) {
		return f.statusStates[index], nil
	}
	return f.state, f.error
}

type notifyingModeBridge struct {
	*fakeModeBridge
	notifications int
}

func (b *notifyingModeBridge) activationChanged(context.Context) { b.notifications++ }

func activeModeState() ModeState {
	return ModeState{ConnectionID: "connection-a", Active: true, Requested: ModeReadOnly, Verified: ModeReadOnly, Revision: 4, DisclosureDigest: "disclosure-a"}
}

func TestEffectiveModeUsesLeastAuthority(t *testing.T) {
	for _, test := range []struct {
		requested, verified, want Mode
		emergency                 bool
	}{
		{ModeAuto, ModeReadOnly, ModeReadOnly, false},
		{ModeReadOnly, ModeAuto, ModeReadOnly, false},
		{ModeApproval, ModeApproval, ModeApproval, false},
		{ModeAuto, ModeAuto, ModeDisabled, true},
		{Mode("invalid"), ModeAuto, ModeDisabled, false},
	} {
		if got := EffectiveMode(test.requested, test.verified, test.emergency); got != test.want {
			t.Errorf("effective(%s,%s,%t)=%s want %s", test.requested, test.verified, test.emergency, got, test.want)
		}
	}
}

func TestModeRequestRequiresAuthoritativeReadback(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{state: ModeState{ConnectionID: "connection-a", Active: true}}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	state, err := controller.Request(context.Background(), ModeApproval, 4, ModePrerequisites{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Verified != ModeApproval || state.Requested != ModeApproval || state.Revision != 6 || bridge.calls != 1 {
		t.Fatalf("mode not authoritatively verified: %+v bridge=%d", state, bridge.calls)
	}
}

func TestModeMutationNotifiesRuntimeAfterEachDurableAuthorityChange(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &notifyingModeBridge{fakeModeBridge: &fakeModeBridge{state: ModeState{ConnectionID: "connection-a", Active: true}}}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	if _, err := controller.Request(t.Context(), ModeApproval, 4, ModePrerequisites{}); err != nil {
		t.Fatal(err)
	}
	if bridge.notifications != 2 || store.verifyCalls != 1 {
		t.Fatalf("notifications=%d durable_verifications=%d", bridge.notifications, store.verifyCalls)
	}

	store.error = errors.New("persist failed")
	if _, err := controller.Request(t.Context(), ModeReadOnly, store.state.Revision, ModePrerequisites{}); err == nil {
		t.Fatal("mode mutation unexpectedly survived durable persistence failure")
	}
	if bridge.notifications != 2 {
		t.Fatalf("failed persistence notified runtime: %d", bridge.notifications)
	}
}

func TestModeDisableNotifiesRuntimeBeforeUnavailableRemoteConfirmation(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &notifyingModeBridge{fakeModeBridge: &fakeModeBridge{
		state: ModeState{ConnectionID: "connection-a", Active: true}, setError: errors.New("offline"),
	}}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	if _, err := controller.Request(t.Context(), ModeDisabled, 4, ModePrerequisites{}); err == nil {
		t.Fatal("remote outage unexpectedly confirmed disabled mode")
	}
	if bridge.notifications != 1 || store.state.Requested != ModeDisabled {
		t.Fatalf("notifications=%d requested=%s", bridge.notifications, store.state.Requested)
	}
}

func TestModeAuditFailureCannotWidenOrReconcileAuthority(t *testing.T) {
	failing := &authorityAuditFake{err: errors.New("database-SENTINEL")}
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{state: ModeState{ConnectionID: "connection-a", Active: true, Requested: ModeApproval, Verified: ModeApproval, Revision: 5, DisclosureDigest: "disclosure-b"}}
	controller, _ := NewModeController(store, bridge, failing)

	if _, err := controller.Request(context.Background(), ModeApproval, 4, ModePrerequisites{}); err == nil || strings.Contains(err.Error(), "database-SENTINEL") {
		t.Fatalf("mode audit failure was not bounded: %v", err)
	}
	if store.requestCalls != 0 || store.verifyCalls != 0 || bridge.calls != 0 {
		t.Fatalf("audit failure changed mode authority: requests=%d verifies=%d bridge=%d", store.requestCalls, store.verifyCalls, bridge.calls)
	}

	widening := activeModeState()
	widening.Requested = ModeAuto
	store = &fakeModeStore{state: widening}
	bridge = &fakeModeBridge{state: ModeState{ConnectionID: "connection-a", Active: true, Requested: ModeAuto, Verified: ModeAuto, Revision: 6, DisclosureDigest: "disclosure-b"}}
	controller, _ = NewModeController(store, bridge, failing)
	if _, err := controller.Reconcile(context.Background()); err == nil || store.verifyCalls != 0 {
		t.Fatalf("audit failure admitted reconciled authority: verifies=%d err=%v", store.verifyCalls, err)
	}
}

type equalRevisionModeBridge struct{}

func (equalRevisionModeBridge) Reconcile(context.Context, ModeCeilingTarget) error { return nil }

func (equalRevisionModeBridge) SetMode(_ context.Context, mode Mode, revision int64) (ModeState, error) {
	return ModeState{
		ConnectionID:     "connection-a",
		Requested:        mode,
		Verified:         mode,
		Revision:         revision,
		DisclosureDigest: "disclosure-a",
		Active:           true,
	}, nil
}

func (equalRevisionModeBridge) Status(context.Context) (ModeState, error) {
	return ModeState{}, nil
}

func TestModeRequestAcceptsEqualAuthoritativeRevision(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	controller, _ := NewModeController(store, equalRevisionModeBridge{}, &authorityAuditFake{})
	state, err := controller.Request(context.Background(), ModeDisabled, 4, ModePrerequisites{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Requested != ModeDisabled || state.Verified != ModeDisabled || state.Revision != 5 {
		t.Fatalf("equal authoritative revision was not committed: %+v", state)
	}
}

func TestModeRequestRetriesPendingLocalRequestWithoutAdvancingRevision(t *testing.T) {
	state := activeModeState()
	state.Requested = ModeDisabled
	state.Verified = ModeReadOnly
	state.Revision = 5
	store := &fakeModeStore{state: state}
	controller, _ := NewModeController(store, equalRevisionModeBridge{}, &authorityAuditFake{})
	got, err := controller.Request(context.Background(), ModeDisabled, 5, ModePrerequisites{})
	if err != nil {
		t.Fatal(err)
	}
	if store.requestCalls != 0 || got.Requested != ModeDisabled || got.Verified != ModeDisabled || got.Revision != 5 {
		t.Fatalf("pending request was not reconciled at the same revision: state=%+v request_calls=%d", got, store.requestCalls)
	}
}

func TestModeRequestKeepsCentralLowerUntilAllReplicaCeilingReadback(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{state: ModeState{ConnectionID: "connection-a", Active: true, Requested: ModeAuto, Verified: ModeAuto, Revision: 5, DisclosureDigest: "disclosure-b"}}
	controller, _ := NewModeController(store, bridgeWithoutModeCeiling{delegate: bridge}, &authorityAuditFake{})
	controller.SetModeCeilingRollout(modeCeilingRolloutFunc(func(_ context.Context, target ModeCeilingTarget) error {
		if target.ConnectionID != "connection-a" || target.Desired != ModeAuto || target.ExpectedRevision != 5 || bridge.calls != 0 || store.state.Requested != ModeAuto || store.state.Verified != ModeReadOnly {
			t.Fatalf("rollout ordering did not retain the lower central mode: state=%+v bridge_calls=%d", store.state, bridge.calls)
		}
		return nil
	}))
	got, err := controller.Request(t.Context(), ModeAuto, 4, ModePrerequisites{DisclosureAcknowledged: true, AutomationAllowlistReady: true, AutomationIdentityReady: true, AutomationTargetReady: true})
	if err != nil || got.Verified != ModeAuto || bridge.calls != 1 || controller.writes.State().Closed {
		t.Fatalf("verified upward transition=%+v bridge_calls=%d fence=%+v err=%v", got, bridge.calls, controller.writes.State(), err)
	}
}

func TestModeReductionPersistsAndStaysFencedWhenCeilingReadbackFails(t *testing.T) {
	state := activeModeState()
	state.Requested, state.Verified = ModeAuto, ModeAuto
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{}
	controller, _ := NewModeController(store, bridgeWithoutModeCeiling{delegate: bridge}, &authorityAuditFake{})
	controller.SetModeCeilingRollout(modeCeilingRolloutFunc(func(context.Context, ModeCeilingTarget) error {
		return errors.New("partial rollout")
	}))
	got, err := controller.Request(t.Context(), ModeReadOnly, 4, ModePrerequisites{})
	if err == nil || got.Requested != ModeReadOnly || got.Verified != ModeAuto || bridge.calls != 0 || !controller.writes.State().Closed {
		t.Fatalf("failed reduction was not locally bounded: state=%+v bridge_calls=%d fence=%+v err=%v", got, bridge.calls, controller.writes.State(), err)
	}
}

func TestModeTransitionFailsClosedWithoutKubernetesRolloutDependency(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{}
	controller, _ := NewModeController(store, bridgeWithoutModeCeiling{delegate: bridge}, &authorityAuditFake{})
	got, err := controller.Request(t.Context(), ModeApproval, 4, ModePrerequisites{})
	if err == nil || got.Requested != ModeApproval || got.Verified != ModeReadOnly || bridge.calls != 0 || !controller.writes.State().Closed {
		t.Fatalf("missing rollout dependency did not fail closed: state=%+v bridge_calls=%d fence=%+v err=%v", got, bridge.calls, controller.writes.State(), err)
	}
}

func TestAutoModeRequiresAllReviewedPrerequisites(t *testing.T) {
	for name, prerequisites := range map[string]ModePrerequisites{
		"none":         {},
		"no_allowlist": {DisclosureAcknowledged: true, AutomationIdentityReady: true, AutomationTargetReady: true},
		"no_identity":  {DisclosureAcknowledged: true, AutomationAllowlistReady: true, AutomationTargetReady: true},
		"no_target":    {DisclosureAcknowledged: true, AutomationAllowlistReady: true, AutomationIdentityReady: true},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeModeStore{state: activeModeState()}
			bridge := &fakeModeBridge{}
			controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
			if _, err := controller.Request(context.Background(), ModeAuto, 4, prerequisites); err == nil || bridge.calls != 0 {
				t.Fatal("auto mode enabled without prerequisites")
			}
		})
	}
}

func TestRemoteFailureCannotEscalateVerifiedMode(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{error: errors.New("unavailable")}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	state, err := controller.Request(context.Background(), ModeAuto, 4, ModePrerequisites{DisclosureAcknowledged: true, AutomationAllowlistReady: true, AutomationIdentityReady: true, AutomationTargetReady: true})
	if err == nil {
		t.Fatal("remote failure was hidden")
	}
	if state.Requested != ModeAuto || state.Verified != ModeReadOnly || EffectiveMode(state.Requested, state.Verified, false) != ModeReadOnly {
		t.Fatalf("remote failure escalated authority: %+v", state)
	}
}

func TestEmergencyDisableCommitsLocallyBeforeRemoteAndStaysClosedOnOutage(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{error: errors.New("unavailable")}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	state, err := controller.EmergencyDisable(context.Background(), "admin-a")
	if err == nil {
		t.Fatal("pending remote confirmation was not reported")
	}
	if !state.EmergencyDisabled || state.Requested != ModeDisabled || store.emergencyCalls != 1 || bridge.lastMode != ModeDisabled {
		t.Fatalf("local emergency latch did not win: %+v", state)
	}
}

func TestEmergencyDisableRemainsAvailableWhenAuditStorageFails(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{state: activeModeState()}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{err: errors.New("database-SENTINEL")})

	state, err := controller.EmergencyDisable(context.Background(), "admin-a")
	if err != nil || !state.EmergencyDisabled || store.emergencyCalls != 1 || bridge.calls != 1 || bridge.lastMode != ModeDisabled {
		t.Fatalf("audit outage blocked emergency disable: state=%+v local=%d remote=%d mode=%s err=%v", state, store.emergencyCalls, bridge.calls, bridge.lastMode, err)
	}
}

func TestEmergencyDisableCancelsAndDrainsRegisteredWrite(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	fence := NewWriteFence()
	controller.SetWriteFence(fence)
	writeCtx, release, err := fence.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		<-writeCtx.Done()
		release()
		close(done)
	}()
	state, err := controller.EmergencyDisable(context.Background(), "admin-a")
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if !state.EmergencyDisabled || !fence.State().Drained || !fence.State().Closed {
		t.Fatalf("emergency disable did not close and drain: mode=%+v fence=%+v", state, fence.State())
	}
}

func TestEmergencyDisableReturnsPendingStateUntilNonCancellableWriteDrains(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	fence := NewWriteFence()
	controller.SetWriteFence(fence)
	_, release, err := fence.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	state, err := controller.EmergencyDisable(ctx, "admin-a")
	var pending *WriteDrainError
	if !errors.As(err, &pending) || state.EmergencyDisabled || store.emergencyCalls != 0 || pending.State.Active != 1 || bridge.calls != 0 {
		t.Fatalf("state=%+v pending=%+v bridge_calls=%d err=%v", state, pending, bridge.calls, err)
	}
	release()
}

func TestEmergencyDisableIsIdempotentAfterFeatureSuspension(t *testing.T) {
	state := activeModeState()
	state.Active = false
	state.EmergencyDisabled = true
	state.Requested = ModeDisabled
	state.Verified = ModeDisabled
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	got, err := controller.EmergencyDisable(context.Background(), "admin-a")
	if err != nil || !got.EmergencyDisabled || store.emergencyCalls != 0 || bridge.calls != 0 {
		t.Fatalf("idempotent disabled state=%+v store_calls=%d bridge_calls=%d err=%v", got, store.emergencyCalls, bridge.calls, err)
	}
}

func TestEmergencyClearReconcilesRemoteDisabledAndRestoresNoAuthority(t *testing.T) {
	state := activeModeState()
	state.EmergencyDisabled = true
	state.Requested = ModeDisabled
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{state: ModeState{Requested: ModeAuto, Verified: ModeAuto}}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	cleared, err := controller.ClearEmergencyDisable(context.Background(), "admin-a")
	if err != nil {
		t.Fatal(err)
	}
	if bridge.calls != 1 || bridge.lastMode != ModeDisabled || store.clearCalls != 1 || cleared.EmergencyDisabled || cleared.Requested != ModeDisabled || cleared.Verified != ModeDisabled {
		t.Fatalf("clear did not reconcile disabled without restoring authority: state=%+v bridge_calls=%d mode=%s", cleared, bridge.calls, bridge.lastMode)
	}
}

func TestEmergencyClearKeepsLatchWhenRemoteDisableRetryFails(t *testing.T) {
	state := activeModeState()
	state.EmergencyDisabled = true
	state.Requested = ModeDisabled
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{
		state:    ModeState{Requested: ModeAuto, Verified: ModeAuto},
		setError: errors.New("remote unavailable"),
	}
	controller, _ := NewModeController(store, bridge, &authorityAuditFake{})
	got, err := controller.ClearEmergencyDisable(context.Background(), "admin-a")
	if err == nil || !got.EmergencyDisabled || store.clearCalls != 0 || bridge.calls != 1 || bridge.lastMode != ModeDisabled {
		t.Fatalf("failed remote disable weakened the latch: state=%+v bridge_calls=%d mode=%s err=%v", got, bridge.calls, bridge.lastMode, err)
	}
}
