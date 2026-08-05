package charlie

import (
	"testing"
	"time"
)

func permittedWrite(now time.Time) AuthorityInput {
	return AuthorityInput{
		FeatureEnabled: true, ConnectionActive: true, Mode: ModeAuto,
		Effect: EffectWrite, DisclosureCurrent: true, LiveAuthorized: true,
		AutoEligible: true, Allowlisted: true, ScopeAllowed: true,
		BudgetAvailable: true, CooldownClear: true, CircuitClosed: true,
		PreconditionsMet: true, IdempotencyKeyPresent: true,
		VerificationDeclared: true, FencingEpoch: 7, CurrentFencingEpoch: 7,
		ApprovalPresent: true, ApprovalExact: true, ApprovalExpiresAt: now.Add(time.Minute),
	}
}

func TestDecideAuthorityPermitsOnlyCompleteBoundedCases(t *testing.T) {
	now := time.Now()
	write := permittedWrite(now)
	if got := DecideAuthority(write, now); !got.Allowed {
		t.Fatalf("bounded auto write denied: %+v", got)
	}
	read := write
	read.Mode = ModeReadOnly
	read.Effect = EffectRead
	if got := DecideAuthority(read, now); !got.Allowed {
		t.Fatalf("authorized read denied: %+v", got)
	}
	approval := write
	approval.Mode = ModeApproval
	if got := DecideAuthority(approval, now); !got.Allowed {
		t.Fatalf("exact approval denied: %+v", got)
	}
}

func TestDecideAuthorityDenialPrecedence(t *testing.T) {
	now := time.Now()
	base := permittedWrite(now)
	cases := []struct {
		name string
		want DenialCode
		set  func(*AuthorityInput)
	}{
		{"feature", DeniedFeatureDisabled, func(v *AuthorityInput) { v.FeatureEnabled = false; v.ConnectionActive = false }},
		{"connection", DeniedConnectionInactive, func(v *AuthorityInput) { v.ConnectionActive = false; v.EmergencyDisabled = true }},
		{"emergency", DeniedEmergencyDisabled, func(v *AuthorityInput) { v.EmergencyDisabled = true; v.Destructive = true }},
		{"mode", DeniedModeDisabled, func(v *AuthorityInput) { v.Mode = ModeDisabled; v.Destructive = true }},
		{"destructive", DeniedDestructive, func(v *AuthorityInput) { v.Destructive = true; v.LiveAuthorized = false }},
		{"disclosure", DeniedDisclosureChanged, func(v *AuthorityInput) { v.DisclosureCurrent = false; v.LiveAuthorized = false }},
		{"authorization", DeniedAuthorization, func(v *AuthorityInput) { v.LiveAuthorized = false; v.IdempotencyKeyPresent = false }},
		{"idempotency", DeniedIdempotency, func(v *AuthorityInput) { v.IdempotencyKeyPresent = false; v.FencingEpoch = 0 }},
		{"verification", DeniedVerification, func(v *AuthorityInput) { v.VerificationDeclared = false; v.FencingEpoch = 0 }},
		{"fencing", DeniedStaleFencing, func(v *AuthorityInput) { v.FencingEpoch = 6; v.AmbiguousPriorAttempt = true }},
		{"ambiguous", DeniedAmbiguousPriorAttempt, func(v *AuthorityInput) { v.AmbiguousPriorAttempt = true; v.PreconditionsMet = false }},
		{"precondition", DeniedPrecondition, func(v *AuthorityInput) { v.PreconditionsMet = false; v.AutoEligible = false }},
		{"eligibility", DeniedNotAutoEligible, func(v *AuthorityInput) { v.AutoEligible = false; v.Allowlisted = false }},
		{"allowlist", DeniedNotAllowlisted, func(v *AuthorityInput) { v.Allowlisted = false; v.ScopeAllowed = false }},
		{"scope", DeniedScope, func(v *AuthorityInput) { v.ScopeAllowed = false; v.BudgetAvailable = false }},
		{"budget", DeniedBudget, func(v *AuthorityInput) { v.BudgetAvailable = false; v.CooldownClear = false }},
		{"cooldown", DeniedCooldown, func(v *AuthorityInput) { v.CooldownClear = false; v.CircuitClosed = false }},
		{"circuit", DeniedCircuitOpen, func(v *AuthorityInput) { v.CircuitClosed = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.set(&in)
			got := DecideAuthority(in, now)
			if got.Allowed || got.Code != tc.want {
				t.Fatalf("decision=%+v, want denial %s", got, tc.want)
			}
		})
	}
}

func TestDecideAuthorityApprovalIsExactAndExpiring(t *testing.T) {
	now := time.Now()
	for _, mutate := range []func(*AuthorityInput){
		func(v *AuthorityInput) { v.ApprovalPresent = false },
		func(v *AuthorityInput) { v.ApprovalExact = false },
		func(v *AuthorityInput) { v.ApprovalExpiresAt = now },
	} {
		in := permittedWrite(now)
		in.Mode = ModeApproval
		mutate(&in)
		if got := DecideAuthority(in, now); got.Allowed || (got.Code != DeniedApprovalRequired && got.Code != DeniedApprovalInvalid) {
			t.Fatalf("invalid approval accepted: %+v", got)
		}
	}
}

func TestBlockedFindingIsActionableOnlyForActiveDiagnosis(t *testing.T) {
	for _, code := range []DenialCode{DeniedFeatureDisabled, DeniedConnectionInactive, DeniedEmergencyDisabled, DeniedModeDisabled} {
		if got := BlockedFinding(AuthorityDecision{Code: code}, "issue", "summary", "fix", "verify"); got.Actionable {
			t.Fatalf("inert state %s created a finding", code)
		}
	}
	got := BlockedFinding(AuthorityDecision{Code: DeniedReadOnlyWrite}, "issue", "summary", "fix", "verify")
	if !got.Actionable || got.ExecutionBlockCode != DeniedReadOnlyWrite || got.RecommendedAction != "fix" {
		t.Fatalf("blocked diagnosis did not produce actionable finding: %+v", got)
	}
}
