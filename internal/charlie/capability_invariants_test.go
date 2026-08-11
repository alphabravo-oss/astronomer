package charlie

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestV1DescriptorInvariantRejectsDestructiveAndSpoofedSafetyMetadata(t *testing.T) {
	base := WriteCapabilityCatalog()[0]
	tests := map[string]func(*CapabilityDescriptor){
		"destructive despite automation": func(value *CapabilityDescriptor) { value.Destructive, value.AutoEligible = true, true },
		"spoofed effect":                 func(value *CapabilityDescriptor) { value.Effect = EffectRead },
		"spoofed risk":                   func(value *CapabilityDescriptor) { value.Risk = "low" },
		"spoofed reversibility":          func(value *CapabilityDescriptor) { value.Reversibility = "fully_reversible" },
		"spoofed rollback":               func(value *CapabilityDescriptor) { value.Rollback = "rollback_available" },
		"missing verification":           func(value *CapabilityDescriptor) { value.RequiresVerification = false },
		"missing precondition":           func(value *CapabilityDescriptor) { value.RequiresPrecondition = false },
		"missing idempotency":            func(value *CapabilityDescriptor) { value.Idempotent = false },
		"managed target":                 func(value *CapabilityDescriptor) { value.ManagedTargetAccess = true },
		"caller supplied approval": func(value *CapabilityDescriptor) {
			value.AcceptedFields = append(value.AcceptedFields, "approval_id")
		},
		"caller supplied safety facts": func(value *CapabilityDescriptor) {
			value.AcceptedFields = append(value.AcceptedFields, "risk", "rollback", "auto_eligible")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			descriptor := base
			descriptor.AcceptedFields = append([]string(nil), base.AcceptedFields...)
			mutate(&descriptor)
			if err := validateV1CapabilityDescriptor(descriptor); err == nil {
				t.Fatalf("unsafe descriptor was accepted: %+v", descriptor)
			}
		})
	}
}

func TestV1DescriptorTimeoutMatchesCharlieContractMaximum(t *testing.T) {
	descriptor, ok := capabilityByName("astronomer.management.workload_rollout")
	if !ok || descriptor.TimeoutSeconds != 120 || validateV1CapabilityDescriptor(descriptor) != nil {
		t.Fatalf("120-second contract maximum was not accepted: %+v", descriptor)
	}
	descriptor.TimeoutSeconds++
	if validateV1CapabilityDescriptor(descriptor) == nil {
		t.Fatal("descriptor above the 120-second contract maximum was accepted")
	}
}

func TestUnsafeDescriptorFailsRegistrationAndDiscovery(t *testing.T) {
	unsafe := WriteCapabilityCatalog()[0]
	unsafe.Destructive = true
	unsafe.AutoEligible = true
	unsafe.Risk = "low"
	unsafe.Rollback = "fully_reversible"
	adapter := &countingCapabilityAdapter{}
	if _, err := newCatalogExecutor(map[string]CapabilityExecutor{unsafe.Name: adapter}, []CapabilityDescriptor{unsafe}); err == nil {
		t.Fatal("destructive descriptor registered")
	}
	if tools := mcpToolsFromCatalog(context.Background(), []CapabilityDescriptor{unsafe}, nil); len(tools) != 0 {
		t.Fatalf("destructive descriptor was discoverable: %+v", tools)
	}
}

func TestDestructiveDescriptorWinsOverApprovalAndAutomationFacts(t *testing.T) {
	decision := DecideAuthority(AuthorityInput{
		FeatureEnabled: true, ConnectionActive: true, Mode: ModeAuto, Effect: EffectWrite,
		Destructive: true, DisclosureCurrent: true, LiveAuthorized: true,
		ApprovalRequested: true, ApprovalPresent: true, ApprovalExact: true, ApprovalExpiresAt: time.Now().Add(time.Hour),
		AutoEligible: true, Allowlisted: true, ScopeAllowed: true, BudgetAvailable: true, CooldownClear: true, CircuitClosed: true,
		PreconditionsMet: true, MaintenanceClear: true, IdempotencyKeyPresent: true, VerificationDeclared: true, FencingEpoch: 1, CurrentFencingEpoch: 1,
	}, time.Now())
	if decision.Allowed || decision.Code != DeniedDestructive {
		t.Fatalf("approval or automation metadata admitted destructive action: %+v", decision)
	}
}

func TestCatalogExecutorRejectsSpoofedDescriptorAtFinalAdapterBoundary(t *testing.T) {
	canonical := WriteCapabilityCatalog()[0]
	adapter := &countingCapabilityAdapter{}
	executor, err := newCatalogExecutor(map[string]CapabilityExecutor{canonical.Name: adapter}, []CapabilityDescriptor{canonical})
	if err != nil {
		t.Fatal(err)
	}
	unsafe := canonical
	unsafe.Destructive = true
	unsafe.AutoEligible = true
	unsafe.RequiresVerification = false
	if _, err := executor.Execute(context.Background(), unsafe, map[string]json.RawMessage{}); err == nil || adapter.executeCalls != 0 {
		t.Fatalf("spoofed descriptor reached adapter: calls=%d err=%v", adapter.executeCalls, err)
	}
	if verified, err := executor.Verify(context.Background(), unsafe, nil, json.RawMessage(`{"ok":true}`)); err == nil || verified || adapter.verifyCalls != 0 {
		t.Fatalf("spoofed descriptor reached verifier: calls=%d verified=%t err=%v", adapter.verifyCalls, verified, err)
	}
}

func TestActionGuardFinalDispatchRejectsUnsafeDescriptorWithoutSideEffect(t *testing.T) {
	authority := &fakeLiveAuthority{facts: []AuthorityInput{allowedWriteFacts(ModeAuto)}}
	executor := &fakeCapabilityExecutor{verified: true}
	guard, _ := newTestActionGuard(t, authority, &fakeReceipts{}, executor)
	descriptor := WriteCapabilityCatalog()[0]
	descriptor.Destructive = true
	descriptor.AutoEligible = true
	descriptor.Risk = "low"
	descriptor.Rollback = "fully_reversible"
	descriptor.RequiresVerification = false
	if safe, err := guard.commitAuthority(context.Background(), ActionEnvelope{ApprovalID: "approved-a"}, descriptor, nil, allowedWriteFacts(ModeAuto)); safe || err != nil || authority.commitCalls != 0 {
		t.Fatalf("unsafe pre-dispatch descriptor consumed authority: safe=%t commits=%d err=%v", safe, authority.commitCalls, err)
	}
	result := guard.executeAndVerify(context.Background(), ActionEnvelope{ActionID: "action-a"}, descriptor, map[string]json.RawMessage{}, false, AuthorityInput{})
	if result.Code != DeniedDestructive || result.Allowed || executor.calls != 0 || executor.verifyCalls != 0 {
		t.Fatalf("unsafe final dispatch escaped: result=%+v execute=%d verify=%d", result, executor.calls, executor.verifyCalls)
	}
}

type countingCapabilityAdapter struct {
	executeCalls int
	verifyCalls  int
}

func (a *countingCapabilityAdapter) Execute(context.Context, CapabilityDescriptor, map[string]json.RawMessage) (json.RawMessage, error) {
	a.executeCalls++
	return json.RawMessage(`{"ok":true}`), nil
}

func (a *countingCapabilityAdapter) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	a.verifyCalls++
	return true, nil
}
