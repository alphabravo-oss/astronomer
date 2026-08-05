package charlie

import (
	"testing"
	"time"
)

func permittedWrite(_ time.Time) AuthorityInput {
	return AuthorityInput{
		FeatureEnabled: true, ConnectionActive: true, Mode: ModeAuto,
		Effect: EffectWrite, DisclosureCurrent: true, LiveAuthorized: true,
		AutoEligible: true, Allowlisted: true, ScopeAllowed: true,
		BudgetAvailable: true, CooldownClear: true, CircuitClosed: true,
		PreconditionsMet: true, IdempotencyKeyPresent: true,
		VerificationDeclared: true, FencingEpoch: 7, CurrentFencingEpoch: 7,
	}
}

func exactApprovedWrite(now time.Time, mode Mode) AuthorityInput {
	in := permittedWrite(now)
	in.Mode = mode
	in.ApprovalRequested = true
	in.ApprovalPresent = true
	in.ApprovalExact = true
	in.ApprovalExpiresAt = now.Add(time.Minute)
	return in
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
	approval := exactApprovedWrite(now, ModeApproval)
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

func TestDecideAuthorityExhaustiveDenyPrecedenceByMode(t *testing.T) {
	now := time.Now()
	type gate struct {
		name string
		want DenialCode
		set  func(*AuthorityInput)
	}
	common := []gate{
		{"feature", DeniedFeatureDisabled, func(v *AuthorityInput) { v.FeatureEnabled = false }},
		{"connection", DeniedConnectionInactive, func(v *AuthorityInput) { v.ConnectionActive = false }},
		{"emergency", DeniedEmergencyDisabled, func(v *AuthorityInput) { v.EmergencyDisabled = true }},
		{"mode", DeniedModeDisabled, func(v *AuthorityInput) { v.Mode = ModeDisabled }},
		{"destructive", DeniedDestructive, func(v *AuthorityInput) { v.Destructive = true }},
		{"disclosure", DeniedDisclosureChanged, func(v *AuthorityInput) { v.DisclosureCurrent = false }},
		{"authorization", DeniedAuthorization, func(v *AuthorityInput) { v.LiveAuthorized = false }},
	}
	write := []gate{
		{"idempotency", DeniedIdempotency, func(v *AuthorityInput) { v.IdempotencyKeyPresent = false }},
		{"verification", DeniedVerification, func(v *AuthorityInput) { v.VerificationDeclared = false }},
		{"fencing", DeniedStaleFencing, func(v *AuthorityInput) { v.CurrentFencingEpoch++ }},
		{"ambiguous", DeniedAmbiguousPriorAttempt, func(v *AuthorityInput) { v.AmbiguousPriorAttempt = true }},
		{"precondition", DeniedPrecondition, func(v *AuthorityInput) { v.PreconditionsMet = false }},
	}
	matrices := []struct {
		name  string
		mode  Mode
		gates []gate
	}{
		{
			name: "read_only", mode: ModeReadOnly,
			gates: append(append([]gate{}, common...), gate{"read_only_write", DeniedReadOnlyWrite, func(*AuthorityInput) {}}),
		},
		{
			name: "approval", mode: ModeApproval,
			gates: append(append(append([]gate{}, common...), write...),
				gate{"approval_required", DeniedApprovalRequired, func(v *AuthorityInput) { v.ApprovalPresent = false }},
				gate{"approval_exact", DeniedApprovalInvalid, func(v *AuthorityInput) { v.ApprovalExact = false }},
				gate{"approval_expired", DeniedApprovalInvalid, func(v *AuthorityInput) { v.ApprovalExpiresAt = now }},
			),
		},
		{
			name: "auto", mode: ModeAuto,
			gates: append(append(append([]gate{}, common...), write...),
				gate{"eligibility", DeniedNotAutoEligible, func(v *AuthorityInput) { v.AutoEligible = false }},
				gate{"allowlist", DeniedNotAllowlisted, func(v *AuthorityInput) { v.Allowlisted = false }},
				gate{"scope", DeniedScope, func(v *AuthorityInput) { v.ScopeAllowed = false }},
				gate{"budget", DeniedBudget, func(v *AuthorityInput) { v.BudgetAvailable = false }},
				gate{"cooldown", DeniedCooldown, func(v *AuthorityInput) { v.CooldownClear = false }},
				gate{"circuit", DeniedCircuitOpen, func(v *AuthorityInput) { v.CircuitClosed = false }},
			),
		},
	}

	for _, matrix := range matrices {
		matrix := matrix
		t.Run(matrix.name, func(t *testing.T) {
			for index, current := range matrix.gates {
				index, current := index, current
				t.Run(current.name, func(t *testing.T) {
					in := permittedWrite(now)
					in.Mode = matrix.mode
					if matrix.mode == ModeApproval {
						in = exactApprovedWrite(now, ModeApproval)
					}
					// Fail the selected gate and every lower-priority gate at once.
					// The selected gate must remain the externally visible denial.
					for _, lower := range matrix.gates[index:] {
						lower.set(&in)
					}
					got := DecideAuthority(in, now)
					if got.Allowed || got.Code != current.want {
						t.Fatalf("decision=%+v want=%s with lower-priority denials also active", got, current.want)
					}
				})
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
		in := exactApprovedWrite(now, ModeApproval)
		mutate(&in)
		if got := DecideAuthority(in, now); got.Allowed || (got.Code != DeniedApprovalRequired && got.Code != DeniedApprovalInvalid) {
			t.Fatalf("invalid approval accepted: %+v", got)
		}
	}
}

func TestDecideAuthorityCumulativeModeCeilings(t *testing.T) {
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	read := permittedWrite(now)
	read.Effect = EffectRead
	read.AutoEligible = false
	read.Allowlisted = false

	tests := []struct {
		name string
		in   AuthorityInput
		want DenialCode
	}{
		{name: "disabled denies read", in: func() AuthorityInput { v := read; v.Mode = ModeDisabled; return v }(), want: DeniedModeDisabled},
		{name: "read_only permits read", in: func() AuthorityInput { v := read; v.Mode = ModeReadOnly; return v }()},
		{name: "read_only denies write", in: func() AuthorityInput { v := permittedWrite(now); v.Mode = ModeReadOnly; return v }(), want: DeniedReadOnlyWrite},
		{name: "approval_required retains read", in: func() AuthorityInput { v := read; v.Mode = ModeApproval; return v }()},
		{name: "approval_required requires approval", in: func() AuthorityInput { v := permittedWrite(now); v.Mode = ModeApproval; return v }(), want: DeniedApprovalRequired},
		{name: "approval_required permits exact approval", in: exactApprovedWrite(now, ModeApproval)},
		{name: "automation retains read", in: read},
		{name: "automation permits exact approval without auto eligibility", in: func() AuthorityInput {
			v := exactApprovedWrite(now, ModeAuto)
			v.AutoEligible, v.Allowlisted, v.ScopeAllowed = false, false, false
			v.BudgetAvailable, v.CooldownClear, v.CircuitClosed = false, false, false
			return v
		}()},
		{name: "automation invalid requested approval cannot fall back", in: func() AuthorityInput {
			v := exactApprovedWrite(now, ModeAuto)
			v.ApprovalExact = false
			return v
		}(), want: DeniedApprovalInvalid},
		{name: "automation missing requested approval cannot fall back", in: func() AuthorityInput {
			v := exactApprovedWrite(now, ModeAuto)
			v.ApprovalPresent = false
			return v
		}(), want: DeniedApprovalInvalid},
		{name: "automation expired requested approval cannot fall back", in: func() AuthorityInput {
			v := exactApprovedWrite(now, ModeAuto)
			v.ApprovalExpiresAt = now
			return v
		}(), want: DeniedApprovalInvalid},
		{name: "automation permits separately eligible automatic path", in: permittedWrite(now)},
		{name: "automation automatic path requires eligibility", in: func() AuthorityInput {
			v := permittedWrite(now)
			v.AutoEligible = false
			return v
		}(), want: DeniedNotAutoEligible},
		{name: "unknown mode fails closed", in: func() AuthorityInput {
			v := permittedWrite(now)
			v.Mode = Mode("unknown")
			return v
		}(), want: DeniedModeDisabled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DecideAuthority(test.in, now)
			if test.want == "" && !got.Allowed {
				t.Fatalf("decision=%+v, want allowed", got)
			}
			if test.want != "" && (got.Allowed || got.Code != test.want) {
				t.Fatalf("decision=%+v, want denial %s", got, test.want)
			}
		})
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
