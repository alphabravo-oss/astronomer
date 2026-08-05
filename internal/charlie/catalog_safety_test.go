package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type gameDayReceiptStore struct {
	claimed     bool
	transitions []string
}

func (s *gameDayReceiptStore) Claim(context.Context, ActionEnvelope, CapabilityDescriptor) (ReceiptClaim, error) {
	if s.claimed {
		return ReceiptClaim{Disposition: ReceiptAmbiguous}, nil
	}
	s.claimed = true
	return ReceiptClaim{Disposition: ReceiptClaimed}, nil
}

func (s *gameDayReceiptStore) Transition(_ context.Context, _ ActionEnvelope, state string, _ ActionResult) error {
	s.transitions = append(s.transitions, state)
	return nil
}

func validReadArguments(name string) map[string]any {
	const id = "11111111-1111-4111-8111-111111111111"
	switch name {
	case "astronomer.installation.configuration":
		return map[string]any{"keys": []string{"feature.charlie"}}
	case "astronomer.management.workloads":
		return map[string]any{"page": 2, "page_size": 10}
	case "astronomer.management.workload_get":
		return map[string]any{"workload": "deployment/astronomer-server"}
	case "astronomer.management.events":
		return map[string]any{"component": "astronomer-server", "since": "1h", "limit": 10}
	case "astronomer.management.pod_logs":
		return map[string]any{"pod": "astronomer-server", "container": "server", "lines": 10}
	case "astronomer.queue.failed_tasks":
		return map[string]any{"page": 2, "page_size": 10, "task_type": "catalog:sync"}
	case "astronomer.observability.health":
		return map[string]any{"query_template": "availability", "range": "15m"}
	case "astronomer.alert.list":
		return map[string]any{"status": "active", "severity": "critical", "page": 2, "page_size": 10}
	case "astronomer.alert.get":
		return map[string]any{"alert_id": id}
	case "astronomer.audit.recent_changes":
		return map[string]any{"resource_type": "platform_setting", "resource_id": "feature.charlie", "since": "1h", "limit": 10}
	case "astronomer.agent_fleet.summary":
		return map[string]any{"stale_after_seconds": 300}
	case "astronomer.agent_fleet.list":
		return map[string]any{"environment": "prod", "region": "us-east", "state": "connected", "page": 2, "page_size": 10}
	case "astronomer.agent_fleet.get", "astronomer.agent_fleet.upgrade_status", "astronomer.agent_fleet.ingestion_health":
		return map[string]any{"cluster_id": id}
	case "astronomer.agent_fleet.connection_history":
		return map[string]any{"cluster_id": id, "since": "1h", "limit": 10}
	case "astronomer.tunnel.recent_errors":
		return map[string]any{"since": "1h", "limit": 10, "connection_id": "connection-a"}
	default:
		return map[string]any{}
	}
}

func allowedReadFacts() AuthorityInput {
	facts := allowedWriteFacts(ModeReadOnly)
	facts.Effect = EffectRead
	return facts
}

func executeReadFixture(t *testing.T, descriptor CapabilityDescriptor, result json.RawMessage, executor *fakeCapabilityExecutor, ctx context.Context) ActionResult {
	t.Helper()
	facts := allowedReadFacts()
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
	receipts := &fakeReceipts{}
	if executor == nil {
		executor = &fakeCapabilityExecutor{result: result, verified: true}
	}
	guard, key := newTestActionGuard(t, authority, receipts, executor)
	return guard.Execute(ctx, signedTestAction(t, key, descriptor.Name, validReadArguments(descriptor.Name)))
}

// Adapter-specific tests pin the safe fields for Kubernetes, queue, Argo,
// operational, and fleet data. This matrix pins the common non-mutating
// boundary around every disclosed read so a newly added tool cannot silently
// omit timeout, response, scope, RBAC, or exact-schema enforcement.
func TestEveryReadCapabilityUsesCompleteBoundedEnvelope(t *testing.T) {
	for _, descriptor := range ReadCapabilityCatalog() {
		descriptor := descriptor
		t.Run(descriptor.Name, func(t *testing.T) {
			if descriptor.Effect != EffectRead || descriptor.ManagedTargetAccess || descriptor.MaxResponseBytes <= 0 || descriptor.TimeoutSeconds <= 0 {
				t.Fatalf("unsafe read descriptor: %+v", descriptor)
			}
			arguments := validReadArguments(descriptor.Name)
			if err := validateCapabilityArguments(descriptor, rawArguments(t, arguments)); err != nil {
				t.Fatalf("valid read fixture drifted from schema: %v", err)
			}

			for name, payload := range map[string]json.RawMessage{
				"positive": json.RawMessage(`{"items":[{"state":"ready"}]}`),
				"empty":    json.RawMessage(`{"items":[]}`),
				"partial":  json.RawMessage(`{"items":[],"partial":true,"unavailable":["optional_source"]}`),
			} {
				t.Run(name, func(t *testing.T) {
					result := executeReadFixture(t, descriptor, payload, nil, context.Background())
					if !result.Allowed || result.State != "succeeded" || !json.Valid(result.Result) {
						t.Fatalf("bounded %s result failed: %+v", name, result)
					}
				})
			}

			t.Run("timeout", func(t *testing.T) {
				executor := &fakeCapabilityExecutor{waitForContext: true}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				result := executeReadFixture(t, descriptor, nil, executor, ctx)
				if !result.Allowed || result.State != "failed" || executor.calls != 1 || len(result.Result) != 0 {
					t.Fatalf("timeout returned usable evidence: result=%+v calls=%d", result, executor.calls)
				}
			})

			t.Run("size_limit", func(t *testing.T) {
				oversized := json.RawMessage(`{"data":"` + strings.Repeat("x", descriptor.MaxResponseBytes) + `"}`)
				result := executeReadFixture(t, descriptor, oversized, nil, context.Background())
				if !result.Allowed || result.State != "failed" || len(result.Result) != 0 {
					t.Fatalf("oversized evidence escaped: %+v", result)
				}
			})

			t.Run("resource_scope", func(t *testing.T) {
				facts := allowedReadFacts()
				facts.LiveAuthorized = false
				authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
				executor := &fakeCapabilityExecutor{}
				guard, key := newTestActionGuard(t, authority, &fakeReceipts{}, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if result.Code != DeniedAuthorization || executor.calls != 0 {
					t.Fatalf("resource authorization was bypassed: result=%+v calls=%d", result, executor.calls)
				}
			})

			t.Run("forbidden_input", func(t *testing.T) {
				arguments := validReadArguments(descriptor.Name)
				arguments["url"] = "https://downstream.invalid/SENTINEL"
				authority := &fakeLiveAuthority{facts: []AuthorityInput{allowedReadFacts()}}
				executor := &fakeCapabilityExecutor{}
				guard, key := newTestActionGuard(t, authority, &fakeReceipts{}, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if result.Code != DeniedScope || authority.calls != 0 || executor.calls != 0 {
					t.Fatalf("forbidden input reached live services: result=%+v authority=%d adapter=%d", result, authority.calls, executor.calls)
				}
			})
		})
	}
}

func TestEveryWriteCapabilityComplementsCompleteSafetyEnvelope(t *testing.T) {
	for _, descriptor := range WriteCapabilityCatalog() {
		descriptor := descriptor
		arguments := validWriteArguments(descriptor.Name)
		for _, denial := range []struct {
			name string
			want DenialCode
			set  func(*AuthorityInput)
		}{
			{"approval_expired", DeniedApprovalInvalid, func(v *AuthorityInput) { v.ApprovalExpiresAt = time.Now().Add(-time.Second) }},
			{"verification_missing", DeniedVerification, func(v *AuthorityInput) { v.VerificationDeclared = false }},
			{"ambiguous_prior_attempt", DeniedAmbiguousPriorAttempt, func(v *AuthorityInput) { v.AmbiguousPriorAttempt = true }},
			{"destructive", DeniedDestructive, func(v *AuthorityInput) { v.Destructive = true }},
		} {
			denial := denial
			t.Run(descriptor.Name+"/"+denial.name, func(t *testing.T) {
				facts := allowedWriteFacts(ModeApproval)
				denial.set(&facts)
				executor := &fakeCapabilityExecutor{}
				guard, key := newTestActionGuard(t, &fakeLiveAuthority{facts: []AuthorityInput{facts}}, &fakeReceipts{}, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if result.Code != denial.want || executor.calls != 0 {
					t.Fatalf("deny-only control failed: result=%+v calls=%d", result, executor.calls)
				}
			})
		}

		for _, denial := range []struct {
			name string
			want DenialCode
			set  func(*AuthorityInput)
		}{
			{"quota", DeniedBudget, func(v *AuthorityInput) { v.BudgetAvailable = false }},
			{"cooldown", DeniedCooldown, func(v *AuthorityInput) { v.CooldownClear = false }},
			{"circuit", DeniedCircuitOpen, func(v *AuthorityInput) { v.CircuitClosed = false }},
			{"scope", DeniedScope, func(v *AuthorityInput) { v.ScopeAllowed = false }},
		} {
			denial := denial
			t.Run(descriptor.Name+"/auto_"+denial.name, func(t *testing.T) {
				facts := allowedWriteFacts(ModeAuto)
				facts.AutoEligible = true
				denial.set(&facts)
				executor := &fakeCapabilityExecutor{}
				guard, key := newTestActionGuard(t, &fakeLiveAuthority{facts: []AuthorityInput{facts}}, &fakeReceipts{}, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if result.Code != denial.want || executor.calls != 0 {
					t.Fatalf("automatic safety control failed: result=%+v calls=%d", result, executor.calls)
				}
			})
		}

		t.Run(descriptor.Name+"/validation", func(t *testing.T) {
			invalid := validWriteArguments(descriptor.Name)
			invalid["raw_request"] = "SENTINEL"
			authority := &fakeLiveAuthority{facts: []AuthorityInput{allowedWriteFacts(ModeApproval)}}
			executor := &fakeCapabilityExecutor{}
			guard, key := newTestActionGuard(t, authority, &fakeReceipts{}, executor)
			result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, invalid))
			if result.Code != DeniedScope || authority.calls != 0 || executor.calls != 0 {
				t.Fatalf("invalid write reached authority or adapter: result=%+v authority=%d calls=%d", result, authority.calls, executor.calls)
			}
		})
	}
}

func TestGameDayDisabledEmergencyAndAuthorityOutageIsolateEveryCapability(t *testing.T) {
	for _, descriptor := range append(ReadCapabilityCatalog(), WriteCapabilityCatalog()...) {
		descriptor := descriptor
		arguments := validReadArguments(descriptor.Name)
		if descriptor.Effect == EffectWrite {
			arguments = validWriteArguments(descriptor.Name)
		}
		for _, scenario := range []struct {
			name  string
			facts *AuthorityInput
			err   error
			want  DenialCode
		}{
			{name: "feature_disabled", facts: func() *AuthorityInput {
				v := allowedWriteFacts(ModeApproval)
				v.Effect = descriptor.Effect
				v.FeatureEnabled = false
				return &v
			}(), want: DeniedFeatureDisabled},
			{name: "enabled_without_activation", facts: func() *AuthorityInput {
				v := allowedWriteFacts(ModeApproval)
				v.Effect = descriptor.Effect
				v.ConnectionActive = false
				return &v
			}(), want: DeniedConnectionInactive},
			{name: "central_disabled", facts: func() *AuthorityInput {
				v := allowedWriteFacts(ModeDisabled)
				v.Effect = descriptor.Effect
				return &v
			}(), want: DeniedModeDisabled},
			{name: "emergency_stop", facts: func() *AuthorityInput {
				v := allowedWriteFacts(ModeApproval)
				v.Effect = descriptor.Effect
				v.EmergencyDisabled = true
				return &v
			}(), want: DeniedEmergencyDisabled},
			{name: "authority_outage", err: errors.New("live authority unavailable"), want: DeniedAuthorization},
		} {
			scenario := scenario
			t.Run(descriptor.Name+"/"+scenario.name, func(t *testing.T) {
				authority := &fakeLiveAuthority{error: scenario.err}
				if scenario.facts != nil {
					authority.facts = []AuthorityInput{*scenario.facts}
				}
				executor := &fakeCapabilityExecutor{}
				guard, key := newTestActionGuard(t, authority, &fakeReceipts{}, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if result.Code != scenario.want || executor.calls != 0 {
					t.Fatalf("game-day isolation failed: result=%+v calls=%d", result, executor.calls)
				}
			})
		}
	}
}

func TestGameDayAgentOutageAndFailoverNeverBlindlyReplayWrites(t *testing.T) {
	for _, descriptor := range WriteCapabilityCatalog() {
		descriptor := descriptor
		t.Run(descriptor.Name, func(t *testing.T) {
			publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			facts := allowedWriteFacts(ModeApproval)
			receipts := &gameDayReceiptStore{}
			executor := &fakeCapabilityExecutor{error: errors.New("local agent adapter unavailable")}
			guard, err := NewActionGuard(publicKey, concurrentAuthority{facts: facts}, receipts, executor, &fakeActionAuditor{})
			if err != nil {
				t.Fatal(err)
			}
			action := signedTestAction(t, privateKey, descriptor.Name, validWriteArguments(descriptor.Name))

			first := guard.Execute(context.Background(), action)
			second := guard.Execute(context.Background(), action)
			if first.State != "failed" || second.Code != DeniedAmbiguousPriorAttempt || executor.calls != 1 {
				t.Fatalf("outage replay was unsafe: first=%+v second=%+v calls=%d", first, second, executor.calls)
			}
			if stringSlice(receipts.transitions) != stringSlice([]string{"dispatched", "ambiguous"}) {
				t.Fatalf("outage receipt transitions=%v", receipts.transitions)
			}
		})
	}
}
