package charlie

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

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
	state       ModeState
	error       error
	setError    error
	calls       int
	statusCalls int
	lastMode    Mode
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
	f.statusCalls++
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
	bridge := &fakeModeBridge{state: ModeState{ConnectionID: "connection-a"}}
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
	if !errors.As(err, &pending) || !state.EmergencyDisabled || pending.State.Active != 1 || bridge.calls != 0 {
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
