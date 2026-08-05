package charlie

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeFindingStore struct {
	result DurableFinding
	error  error
	calls  int
}

func (f *fakeFindingStore) UpsertBlockedFinding(context.Context, FindingInput, FindingRecommendation, string) (DurableFinding, error) {
	f.calls++
	return f.result, f.error
}

type fakeFindingPublisher struct {
	alerts []FindingAlert
	error  error
}

func (f *fakeFindingPublisher) PublishCharlieFinding(_ context.Context, alert FindingAlert) error {
	f.alerts = append(f.alerts, alert)
	return f.error
}

func validFindingInputForTest(code DenialCode) FindingInput {
	return FindingInput{
		InstallationID: "installation-a", ResourceType: "agent_connection", ResourceID: "cluster-a",
		NormalizedDiagnosis: "heartbeat_stale", RecommendedCapability: "astronomer.tunnel.refresh_locator_state",
		Severity: "warning", Mode: ModeReadOnly, Decision: AuthorityDecision{Code: code},
		Title: "Agent connection needs attention", Summary: "The heartbeat is stale.",
		RecommendedAction: "Review management-plane tunnel state", Verification: "Re-read the agent connection record",
	}
}

func TestFindingServicePersistsBeforePublishingActionableAlert(t *testing.T) {
	store := &fakeFindingStore{result: DurableFinding{ID: "finding-a", Status: "open", RepeatCount: 3, UpdatedAt: time.Now(), Notify: true}}
	publisher := &fakeFindingPublisher{}
	service, err := NewFindingService(store, publisher)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RecordBlocked(context.Background(), validFindingInputForTest(DeniedReadOnlyWrite))
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "finding-a" || store.calls != 1 || len(publisher.alerts) != 1 {
		t.Fatalf("finding was not durably published: result=%+v store=%d alerts=%v", result, store.calls, publisher.alerts)
	}
	alert := publisher.alerts[0]
	if alert.BlockCode != string(DeniedReadOnlyWrite) || alert.RepeatCount != 3 || alert.ResourceID != "cluster-a" {
		t.Fatalf("alert is not actionable/bounded: %+v", alert)
	}
}

func TestFindingServiceDisabledStatesStayInert(t *testing.T) {
	for _, code := range []DenialCode{DeniedFeatureDisabled, DeniedConnectionInactive, DeniedEmergencyDisabled, DeniedModeDisabled} {
		store := &fakeFindingStore{}
		publisher := &fakeFindingPublisher{}
		service, _ := NewFindingService(store, publisher)
		if _, err := service.RecordBlocked(context.Background(), validFindingInputForTest(code)); err != nil {
			t.Fatal(err)
		}
		if store.calls != 0 || len(publisher.alerts) != 0 {
			t.Fatalf("inert decision %s produced durable work", code)
		}
	}
}

func TestFindingServiceDoesNotPublishBeforeCommit(t *testing.T) {
	store := &fakeFindingStore{error: errors.New("database unavailable")}
	publisher := &fakeFindingPublisher{}
	service, _ := NewFindingService(store, publisher)
	if _, err := service.RecordBlocked(context.Background(), validFindingInputForTest(DeniedApprovalRequired)); err == nil {
		t.Fatal("storage failure was ignored")
	}
	if len(publisher.alerts) != 0 {
		t.Fatal("alert published before durable finding commit")
	}
}

func TestFindingServiceCoalescedRepeatCanSuppressAlertStorm(t *testing.T) {
	store := &fakeFindingStore{result: DurableFinding{ID: "finding-a", Status: "open", RepeatCount: 42, Notify: false}}
	publisher := &fakeFindingPublisher{}
	service, _ := NewFindingService(store, publisher)
	if _, err := service.RecordBlocked(context.Background(), validFindingInputForTest(DeniedCooldown)); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || len(publisher.alerts) != 0 {
		t.Fatal("coalesced repeat created an alert storm")
	}
}
