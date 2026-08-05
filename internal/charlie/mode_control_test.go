package charlie

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeModeStore struct {
	state          ModeState
	error          error
	emergencyCalls int
	clearCalls     int
}

func (f *fakeModeStore) LoadModeState(context.Context) (ModeState, error) { return f.state, f.error }
func (f *fakeModeStore) SetRequestedMode(_ context.Context, connectionID string, mode Mode, expected int64) (ModeState, error) {
	if f.error != nil || connectionID != f.state.ConnectionID || expected != f.state.Revision {
		return ModeState{}, errors.New("CAS failed")
	}
	f.state.Requested = mode
	f.state.Revision++
	return f.state, nil
}
func (f *fakeModeStore) SetVerifiedMode(_ context.Context, connectionID string, mode Mode, expected, next int64, digest string) (ModeState, error) {
	if f.error != nil || connectionID != f.state.ConnectionID || expected != f.state.Revision || next <= expected {
		return ModeState{}, errors.New("verify CAS failed")
	}
	f.state.Verified = mode
	f.state.Revision = next
	f.state.DisclosureDigest = digest
	return f.state, nil
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
	state    ModeState
	error    error
	calls    int
	lastMode Mode
}

func (f *fakeModeBridge) SetMode(_ context.Context, mode Mode, revision int64) (ModeState, error) {
	f.calls++
	f.lastMode = mode
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
func (f *fakeModeBridge) Status(context.Context) (ModeState, error) { return f.state, f.error }

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
	controller, _ := NewModeController(store, bridge)
	state, err := controller.Request(context.Background(), ModeApproval, 4, ModePrerequisites{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Verified != ModeApproval || state.Requested != ModeApproval || state.Revision != 6 || bridge.calls != 1 {
		t.Fatalf("mode not authoritatively verified: %+v bridge=%d", state, bridge.calls)
	}
}

func TestAutoModeRequiresAllReviewedPrerequisites(t *testing.T) {
	for name, prerequisites := range map[string]ModePrerequisites{
		"none":         {},
		"no_allowlist": {DisclosureAcknowledged: true, AutomationIdentityReady: true},
		"no_identity":  {DisclosureAcknowledged: true, AutomationAllowlistReady: true},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeModeStore{state: activeModeState()}
			bridge := &fakeModeBridge{}
			controller, _ := NewModeController(store, bridge)
			if _, err := controller.Request(context.Background(), ModeAuto, 4, prerequisites); err == nil || bridge.calls != 0 {
				t.Fatal("auto mode enabled without prerequisites")
			}
		})
	}
}

func TestRemoteFailureCannotEscalateVerifiedMode(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{error: errors.New("unavailable")}
	controller, _ := NewModeController(store, bridge)
	state, err := controller.Request(context.Background(), ModeAuto, 4, ModePrerequisites{true, true, true})
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
	controller, _ := NewModeController(store, bridge)
	state, err := controller.EmergencyDisable(context.Background(), "admin-a")
	if err == nil {
		t.Fatal("pending remote confirmation was not reported")
	}
	if !state.EmergencyDisabled || state.Requested != ModeDisabled || store.emergencyCalls != 1 || bridge.lastMode != ModeDisabled {
		t.Fatalf("local emergency latch did not win: %+v", state)
	}
}

func TestEmergencyDisableCancelsAndDrainsRegisteredWrite(t *testing.T) {
	store := &fakeModeStore{state: activeModeState()}
	bridge := &fakeModeBridge{}
	controller, _ := NewModeController(store, bridge)
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
	controller, _ := NewModeController(store, bridge)
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
	controller, _ := NewModeController(store, bridge)
	got, err := controller.EmergencyDisable(context.Background(), "admin-a")
	if err != nil || !got.EmergencyDisabled || store.emergencyCalls != 0 || bridge.calls != 0 {
		t.Fatalf("idempotent disabled state=%+v store_calls=%d bridge_calls=%d err=%v", got, store.emergencyCalls, bridge.calls, err)
	}
}

func TestEmergencyClearRequiresRemoteDisabledAndRestoresNoAuthority(t *testing.T) {
	state := activeModeState()
	state.EmergencyDisabled = true
	state.Requested = ModeDisabled
	store := &fakeModeStore{state: state}
	bridge := &fakeModeBridge{state: ModeState{Requested: ModeAuto, Verified: ModeAuto}}
	controller, _ := NewModeController(store, bridge)
	if _, err := controller.ClearEmergencyDisable(context.Background(), "admin-a"); err == nil || store.clearCalls != 0 {
		t.Fatal("emergency latch cleared before remote disabled")
	}
	bridge.state = ModeState{Requested: ModeDisabled, Verified: ModeDisabled}
	cleared, err := controller.ClearEmergencyDisable(context.Background(), "admin-a")
	if err != nil {
		t.Fatal(err)
	}
	if cleared.EmergencyDisabled || cleared.Requested != ModeDisabled || cleared.Verified != ModeDisabled {
		t.Fatalf("clear restored prior authority: %+v", cleared)
	}
}
