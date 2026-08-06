package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeLiveAuthority struct {
	facts       []AuthorityInput
	calls       int
	error       error
	commitError error
	commitCalls int
}

func (f *fakeLiveAuthority) Evaluate(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) (AuthorityInput, error) {
	if f.error != nil {
		return AuthorityInput{}, f.error
	}
	index := f.calls
	f.calls++
	if index >= len(f.facts) {
		index = len(f.facts) - 1
	}
	return f.facts[index], nil
}

func (f *fakeLiveAuthority) Commit(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage, AuthorityInput) error {
	f.commitCalls++
	return f.commitError
}

type fakeReceipts struct {
	claim       ReceiptClaim
	claimCalls  int
	transitions []string
	error       error
}

func (f *fakeReceipts) Claim(context.Context, ActionEnvelope, CapabilityDescriptor) (ReceiptClaim, error) {
	f.claimCalls++
	if f.error != nil {
		return ReceiptClaim{}, f.error
	}
	if f.claim.Disposition == "" {
		return ReceiptClaim{Disposition: ReceiptClaimed}, nil
	}
	return f.claim, nil
}

func (f *fakeReceipts) Transition(_ context.Context, _ ActionEnvelope, state string, _ ActionResult) error {
	f.transitions = append(f.transitions, state)
	return f.error
}

type fakeCapabilityExecutor struct {
	calls          int
	verifyCalls    int
	result         json.RawMessage
	error          error
	verified       bool
	waitForContext bool
}

type fakeActionAuditor struct {
	phases []string
	error  error
	failAt string
}

type actionFindingRecorder struct {
	inputs []FindingInput
	err    error
}

func (f *actionFindingRecorder) RecordBlocked(_ context.Context, input FindingInput) (DurableFinding, error) {
	f.inputs = append(f.inputs, input)
	return DurableFinding{ID: "finding-a", Status: "open"}, f.err
}

type concurrentAuthority struct{ facts AuthorityInput }

func (a concurrentAuthority) Evaluate(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) (AuthorityInput, error) {
	return a.facts, nil
}

func (concurrentAuthority) Commit(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage, AuthorityInput) error {
	return nil
}

type concurrentReceipts struct {
	mu      sync.Mutex
	claimed bool
	result  ActionResult
}

func (s *concurrentReceipts) Claim(context.Context, ActionEnvelope, CapabilityDescriptor) (ReceiptClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.claimed {
		s.claimed = true
		return ReceiptClaim{Disposition: ReceiptClaimed}, nil
	}
	if s.result.State == "succeeded" {
		return ReceiptClaim{Disposition: ReceiptReplay, Result: s.result}, nil
	}
	return ReceiptClaim{Disposition: ReceiptAmbiguous}, nil
}

func (s *concurrentReceipts) Transition(_ context.Context, _ ActionEnvelope, state string, result ActionResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state == "succeeded" {
		s.result = result
	}
	return nil
}

type concurrentExecutor struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (e *concurrentExecutor) Execute(context.Context, CapabilityDescriptor, map[string]json.RawMessage) (json.RawMessage, error) {
	if e.calls.Add(1) == 1 {
		close(e.started)
	}
	<-e.release
	return json.RawMessage(`{"ok":true}`), nil
}

func (*concurrentExecutor) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return true, nil
}

type discardActionAuditor struct{}

func (discardActionAuditor) Record(context.Context, string, ActionEnvelope, CapabilityDescriptor, ActionResult) error {
	return nil
}

func (f *fakeActionAuditor) Record(_ context.Context, phase string, _ ActionEnvelope, _ CapabilityDescriptor, _ ActionResult) error {
	f.phases = append(f.phases, phase)
	if phase == f.failAt {
		return errors.New("audit unavailable")
	}
	return f.error
}

func (f *fakeCapabilityExecutor) Execute(ctx context.Context, _ CapabilityDescriptor, _ map[string]json.RawMessage) (json.RawMessage, error) {
	f.calls++
	if f.waitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.error != nil {
		return nil, f.error
	}
	if f.result == nil {
		return json.RawMessage(`{"ok":true}`), nil
	}
	return f.result, nil
}

func (f *fakeCapabilityExecutor) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	f.verifyCalls++
	return f.verified, f.error
}

func allowedWriteFacts(mode Mode) AuthorityInput {
	return AuthorityInput{
		FeatureEnabled: true, ConnectionActive: true, Mode: mode,
		Effect: EffectWrite, DisclosureCurrent: true, LiveAuthorized: true,
		FindingResourceType: "management_component", FindingResourceID: "resource-a",
		ApprovalPresent: true, ApprovalExact: true, ApprovalExpiresAt: time.Now().Add(time.Minute),
		AutoEligible: true, Allowlisted: true, ScopeAllowed: true, BudgetAvailable: true,
		CooldownClear: true, CircuitClosed: true, PreconditionsMet: true, MaintenanceClear: true,
		IdempotencyKeyPresent: true, VerificationDeclared: true, FencingEpoch: 7, CurrentFencingEpoch: 7,
	}
}

func validWriteArguments(name string) map[string]any {
	switch name {
	case "astronomer.management.workload_restart", "astronomer.management.workload_rollout":
		return map[string]any{"resource_id": "resource-a", "workload": "deployment/astronomer-server", "operation_id": "action-a"}
	case "astronomer.management.workload_scale":
		return map[string]any{"resource_id": "resource-a", "workload": "deployment/astronomer-server", "replicas": 3, "operation_id": "action-a"}
	case "astronomer.argocd.self_management_sync":
		return map[string]any{"resource_id": "resource-a", "application": "astronomer-self-manage", "operation_id": "action-a"}
	case "astronomer.queue.retry_task":
		return map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"}
	case "astronomer.management.run_job":
		return map[string]any{"resource_id": "resource-a", "job": "restore-drill", "operation_id": "action-a"}
	case "astronomer.tunnel.restart_component":
		return map[string]any{"resource_id": "resource-a", "component": "server", "operation_id": "action-a"}
	default:
		return nil
	}
}

func signedTestAction(t *testing.T, privateKey ed25519.PrivateKey, capability string, arguments map[string]any) ActionEnvelope {
	t.Helper()
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicalArguments(raw)
	if err != nil {
		t.Fatal(err)
	}
	action := ActionEnvelope{
		Version: "charlie-action/v1", DeploymentID: "deployment-a", SessionID: "session-a",
		TurnID: "turn-a", ActionID: "action-a", Capability: capability,
		Arguments: canonical, ArgumentDigest: capabilityArgumentDigest(capability, canonical), AuthorizationRef: "opaque-reference",
		DisclosureDigest: "disclosure-a", ModeRevision: 2, PolicyRevision: 2,
		FencingEpoch: 7, ExpiresAt: time.Now().Add(time.Minute).UTC(), IdempotencyKey: "action-a",
	}
	payload, err := json.Marshal(action.signed())
	if err != nil {
		t.Fatal(err)
	}
	action.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return action
}

func newTestActionGuard(t *testing.T, authority *fakeLiveAuthority, receipts *fakeReceipts, executor *fakeCapabilityExecutor) (*ActionGuard, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := NewActionGuard(publicKey, authority, receipts, executor, &fakeActionAuditor{})
	if err != nil {
		t.Fatal(err)
	}
	guard.SetFindingRecorder(&actionFindingRecorder{}, "installation-a")
	return guard, privateKey
}

func TestActionGuardExecutesBoundedWriteOnceAndVerifies(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts, facts}}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{verified: true}
	guard, privateKey := newTestActionGuard(t, authority, receipts, executor)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})

	result := guard.Execute(context.Background(), action)
	if !result.Allowed || result.State != "succeeded" || !result.Verified {
		t.Fatalf("unexpected result: %+v", result)
	}
	if authority.calls != 2 || authority.commitCalls != 1 || receipts.claimCalls != 1 || executor.calls != 1 || executor.verifyCalls != 1 {
		t.Fatalf("authority=%d commits=%d claims=%d execute=%d verify=%d", authority.calls, authority.commitCalls, receipts.claimCalls, executor.calls, executor.verifyCalls)
	}
	want := []string{"dispatched", "succeeded"}
	if stringSlice(receipts.transitions) != stringSlice(want) {
		t.Fatalf("transitions=%v", receipts.transitions)
	}
}

type fencePhaseAuthority struct {
	phase   string
	entered chan struct{}
	once    sync.Once
	facts   AuthorityInput
}

func (a *fencePhaseAuthority) block(ctx context.Context, phase string) error {
	if a.phase != phase {
		return nil
	}
	a.once.Do(func() { close(a.entered) })
	<-ctx.Done()
	return ctx.Err()
}

func (a *fencePhaseAuthority) Evaluate(ctx context.Context, _ ActionEnvelope, _ CapabilityDescriptor, _ map[string]json.RawMessage) (AuthorityInput, error) {
	if err := a.block(ctx, "evaluate"); err != nil {
		return AuthorityInput{}, err
	}
	return a.facts, nil
}

func (a *fencePhaseAuthority) Commit(ctx context.Context, _ ActionEnvelope, _ CapabilityDescriptor, _ map[string]json.RawMessage, _ AuthorityInput) error {
	return a.block(ctx, "commit")
}

type fencePhaseAuditor struct {
	phase   string
	entered chan struct{}
	once    sync.Once
	mu      sync.Mutex
	phases  []string
}

func (a *fencePhaseAuditor) Record(ctx context.Context, phase string, _ ActionEnvelope, _ CapabilityDescriptor, _ ActionResult) error {
	a.mu.Lock()
	a.phases = append(a.phases, phase)
	a.mu.Unlock()
	if a.phase != "pre_dispatch" || phase != "approved" {
		return nil
	}
	a.once.Do(func() { close(a.entered) })
	<-ctx.Done()
	return ctx.Err()
}

func TestActionGuardDisableFencesEvaluateCommitAndPreDispatchRaces(t *testing.T) {
	for _, phase := range []string{"evaluate", "commit", "pre_dispatch"} {
		t.Run(phase, func(t *testing.T) {
			publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			authority := &fencePhaseAuthority{phase: phase, entered: entered, facts: allowedWriteFacts(ModeAuto)}
			auditor := &fencePhaseAuditor{phase: phase, entered: entered}
			receipts := &fakeReceipts{}
			executor := &fakeCapabilityExecutor{verified: true}
			guard, err := NewActionGuard(publicKey, authority, receipts, executor, auditor)
			if err != nil {
				t.Fatal(err)
			}
			fence := NewWriteFence()
			guard.SetWriteFence(fence)
			action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})
			resultCh := make(chan ActionResult, 1)
			go func() { resultCh <- guard.Execute(context.Background(), action) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("write did not reach controlled phase")
			}
			drainCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			state, drainErr := fence.CloseAndWait(drainCtx)
			cancel()
			if drainErr != nil || !state.Closed || !state.Drained {
				t.Fatalf("disable did not drain: state=%+v err=%v", state, drainErr)
			}
			result := <-resultCh
			if result.Code != DeniedEmergencyDisabled || executor.calls != 0 {
				t.Fatalf("phase=%s result=%+v executor_calls=%d", phase, result, executor.calls)
			}
		})
	}
}

func TestWriteFenceReturnsExplicitDrainStateForNonCancellableExecutor(t *testing.T) {
	fence := NewWriteFence()
	_, release, err := fence.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	state, err := fence.CloseAndWait(ctx)
	var pending *WriteDrainError
	if !errors.As(err, &pending) || !state.Closed || state.Drained || state.Active != 1 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if _, _, beginErr := fence.Begin(context.Background()); !errors.Is(beginErr, ErrWriteFenceClosed) {
		t.Fatalf("new write admitted during drain: %v", beginErr)
	}
	release()
}

func TestActionGuardRejectsWriteWithoutRequiredProductResource(t *testing.T) {
	facts := allowedWriteFacts(ModeApproval)
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
	executor := &fakeCapabilityExecutor{}
	guard, privateKey := newTestActionGuard(t, authority, &fakeReceipts{}, executor)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"task_id": "task-a", "operation_id": "action-a"})

	result := guard.Execute(context.Background(), action)
	if result.Code != DeniedScope || authority.calls != 0 || executor.calls != 0 {
		t.Fatalf("write without ProductContext resource reached authority or adapter: result=%+v authority=%d adapter=%d", result, authority.calls, executor.calls)
	}
}

// This matrix pins the common product-owned authority envelope around every
// disclosed write adapter. Adapter-specific tests cover target validation and
// side effects; this test ensures no newly added write can omit mode, RBAC,
// approval, fencing, maintenance, replay, verification, or auto-policy gates.
func TestEveryWriteCapabilityUsesCompleteSafetyEnvelope(t *testing.T) {
	for _, descriptor := range WriteCapabilityCatalog() {
		descriptor := descriptor
		arguments := validWriteArguments(descriptor.Name)
		if arguments == nil {
			t.Fatalf("%s has no safety-matrix fixture", descriptor.Name)
		}
		t.Run(descriptor.Name, func(t *testing.T) {
			t.Run("approval_success", func(t *testing.T) {
				facts := allowedWriteFacts(ModeApproval)
				authority := &fakeLiveAuthority{facts: []AuthorityInput{facts, facts}}
				receipts := &fakeReceipts{}
				executor := &fakeCapabilityExecutor{verified: true}
				guard, key := newTestActionGuard(t, authority, receipts, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if !result.Allowed || result.State != "succeeded" || !result.Verified || executor.calls != 1 || executor.verifyCalls != 1 || authority.commitCalls != 1 {
					t.Fatalf("complete approved action failed: result=%+v execute=%d verify=%d commit=%d", result, executor.calls, executor.verifyCalls, authority.commitCalls)
				}
			})

			for _, denial := range []struct {
				name string
				want DenialCode
				set  func(*AuthorityInput)
			}{
				{"read_only", DeniedReadOnlyWrite, func(v *AuthorityInput) { v.Mode = ModeReadOnly }},
				{"approval_missing", DeniedApprovalRequired, func(v *AuthorityInput) { v.ApprovalPresent = false }},
				{"rbac_denied", DeniedAuthorization, func(v *AuthorityInput) { v.LiveAuthorized = false }},
				{"stale_epoch", DeniedStaleFencing, func(v *AuthorityInput) { v.CurrentFencingEpoch++ }},
				{"maintenance", DeniedMaintenance, func(v *AuthorityInput) { v.MaintenanceClear = false }},
				{"precondition", DeniedPrecondition, func(v *AuthorityInput) { v.PreconditionsMet = false }},
			} {
				denial := denial
				t.Run(denial.name, func(t *testing.T) {
					facts := allowedWriteFacts(ModeApproval)
					denial.set(&facts)
					authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
					receipts := &fakeReceipts{}
					executor := &fakeCapabilityExecutor{}
					guard, key := newTestActionGuard(t, authority, receipts, executor)
					result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
					if result.Allowed || result.Code != denial.want || executor.calls != 0 || receipts.claimCalls != 0 || result.Finding == nil {
						t.Fatalf("gate was bypassed: result=%+v execute=%d claims=%d", result, executor.calls, receipts.claimCalls)
					}
				})
			}

			t.Run("rbac_revoked_after_claim", func(t *testing.T) {
				first := allowedWriteFacts(ModeApproval)
				second := first
				second.LiveAuthorized = false
				authority := &fakeLiveAuthority{facts: []AuthorityInput{first, second}}
				receipts := &fakeReceipts{}
				executor := &fakeCapabilityExecutor{}
				guard, key := newTestActionGuard(t, authority, receipts, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if result.Code != DeniedAuthorization || executor.calls != 0 || receipts.claimCalls != 1 || stringSlice(receipts.transitions) != stringSlice([]string{"blocked"}) {
					t.Fatalf("live revocation did not fence dispatch: result=%+v execute=%d claims=%d transitions=%v", result, executor.calls, receipts.claimCalls, receipts.transitions)
				}
			})

			t.Run("leader_epoch_changed_after_claim", func(t *testing.T) {
				first := allowedWriteFacts(ModeApproval)
				second := first
				second.CurrentFencingEpoch++
				authority := &fakeLiveAuthority{facts: []AuthorityInput{first, second}}
				receipts := &fakeReceipts{}
				executor := &fakeCapabilityExecutor{}
				guard, key := newTestActionGuard(t, authority, receipts, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if result.Code != DeniedStaleFencing || executor.calls != 0 || receipts.claimCalls != 1 || stringSlice(receipts.transitions) != stringSlice([]string{"blocked"}) {
					t.Fatalf("leader change did not fence dispatch: result=%+v execute=%d claims=%d transitions=%v", result, executor.calls, receipts.claimCalls, receipts.transitions)
				}
			})

			t.Run("maintenance_deferred", func(t *testing.T) {
				facts := allowedWriteFacts(ModeApproval)
				authority := &fakeLiveAuthority{facts: []AuthorityInput{facts, facts}, commitError: &ActionDeferredError{OperationID: "action-a", DeferredUntil: time.Now().Add(time.Hour), ExpiresAt: time.Now().Add(25 * time.Hour)}}
				receipts := &fakeReceipts{}
				executor := &fakeCapabilityExecutor{}
				guard, key := newTestActionGuard(t, authority, receipts, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if !result.Allowed || result.State != "deferred" || executor.calls != 0 || stringSlice(receipts.transitions) != stringSlice([]string{"deferred"}) {
					t.Fatalf("maintenance deferral dispatched: result=%+v execute=%d transitions=%v", result, executor.calls, receipts.transitions)
				}
			})

			t.Run("replay", func(t *testing.T) {
				facts := allowedWriteFacts(ModeApproval)
				authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
				receipts := &fakeReceipts{claim: ReceiptClaim{Disposition: ReceiptReplay, Result: ActionResult{Allowed: true, State: "succeeded", Verified: true}}}
				executor := &fakeCapabilityExecutor{}
				guard, key := newTestActionGuard(t, authority, receipts, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if !result.Replay || executor.calls != 0 {
					t.Fatalf("replay executed adapter: result=%+v execute=%d", result, executor.calls)
				}
			})

			t.Run("post_verification_failed", func(t *testing.T) {
				facts := allowedWriteFacts(ModeApproval)
				authority := &fakeLiveAuthority{facts: []AuthorityInput{facts, facts}}
				receipts := &fakeReceipts{}
				executor := &fakeCapabilityExecutor{verified: false}
				guard, key := newTestActionGuard(t, authority, receipts, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				if !result.Allowed || result.State != "failed" || result.Verified || executor.calls != 1 || executor.verifyCalls != 1 {
					t.Fatalf("failed verification was not stopped: result=%+v execute=%d verify=%d", result, executor.calls, executor.verifyCalls)
				}
			})

			t.Run("auto_policy", func(t *testing.T) {
				facts := allowedWriteFacts(ModeAuto)
				facts.AutoEligible = descriptor.AutoEligible
				facts.Allowlisted = false
				authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
				receipts := &fakeReceipts{}
				executor := &fakeCapabilityExecutor{}
				guard, key := newTestActionGuard(t, authority, receipts, executor)
				result := guard.Execute(context.Background(), signedTestAction(t, key, descriptor.Name, arguments))
				want := DeniedNotAllowlisted
				if !descriptor.AutoEligible {
					want = DeniedNotAutoEligible
				}
				if result.Code != want || executor.calls != 0 || result.Finding == nil {
					t.Fatalf("auto gate=%+v want=%s execute=%d", result, want, executor.calls)
				}
			})
		})
	}
}

func TestActionGuardConcurrentDuplicateWriteExecutesUnderlyingOperationOnce(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	receipts := &concurrentReceipts{}
	executor := &concurrentExecutor{started: make(chan struct{}), release: make(chan struct{})}
	guard, err := NewActionGuard(publicKey, concurrentAuthority{facts: allowedWriteFacts(ModeAuto)}, receipts, executor, discardActionAuditor{})
	if err != nil {
		t.Fatal(err)
	}
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})

	const callers = 16
	results := make(chan ActionResult, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- guard.Execute(context.Background(), action)
		}()
	}
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("claimed action did not reach its bounded executor")
	}
	close(executor.release)
	wait.Wait()
	close(results)
	if executor.calls.Load() != 1 {
		t.Fatalf("underlying operation executed %d times, want exactly once", executor.calls.Load())
	}
	for result := range results {
		if result.Allowed || result.Code == DeniedAmbiguousPriorAttempt {
			continue
		}
		t.Fatalf("duplicate returned an unexpected result: %+v", result)
	}
}

func TestActionGuardDisableOrRevocationImmediatelyBeforeSideEffectWins(t *testing.T) {
	first := allowedWriteFacts(ModeAuto)
	second := first
	second.EmergencyDisabled = true
	authority := &fakeLiveAuthority{facts: []AuthorityInput{first, second}}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{verified: true}
	guard, privateKey := newTestActionGuard(t, authority, receipts, executor)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})

	result := guard.Execute(context.Background(), action)
	if result.Code != DeniedEmergencyDisabled || executor.calls != 0 || stringSlice(receipts.transitions) != stringSlice([]string{"blocked"}) {
		t.Fatalf("unsafe dispatch: result=%+v calls=%d transitions=%v", result, executor.calls, receipts.transitions)
	}
}

func TestActionGuardRequiresAtomicApprovalOrBudgetConsumption(t *testing.T) {
	facts := allowedWriteFacts(ModeApproval)
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts, facts}, commitError: errors.New("already consumed")}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{verified: true}
	guard, privateKey := newTestActionGuard(t, authority, receipts, executor)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})
	action.ApprovalID = "approval-a"
	payload, _ := json.Marshal(action.signed())
	action.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	result := guard.Execute(context.Background(), action)
	if result.Code != DeniedApprovalInvalid || authority.commitCalls != 1 || executor.calls != 0 || stringSlice(receipts.transitions) != stringSlice([]string{"blocked"}) {
		t.Fatalf("consumed approval reached side effect: result=%+v commits=%d execute=%d transitions=%v", result, authority.commitCalls, executor.calls, receipts.transitions)
	}
}

func TestActionGuardPersistsDeferralWithoutDispatch(t *testing.T) {
	facts := allowedWriteFacts(ModeApproval)
	deferredUntil := time.Now().UTC().Add(time.Hour)
	authority := &fakeLiveAuthority{
		facts: []AuthorityInput{facts, facts},
		commitError: &ActionDeferredError{
			OperationID: "action-a", DeferredUntil: deferredUntil, ExpiresAt: deferredUntil.Add(24 * time.Hour),
		},
	}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{verified: true}
	guard, privateKey := newTestActionGuard(t, authority, receipts, executor)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})
	action.ApprovalID = "approval-a"
	payload, _ := json.Marshal(action.signed())
	action.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))

	result := guard.Execute(context.Background(), action)
	if !result.Allowed || result.State != "deferred" || executor.calls != 0 || stringSlice(receipts.transitions) != stringSlice([]string{"deferred"}) {
		t.Fatalf("deferred action reached dispatch: result=%+v calls=%d transitions=%v", result, executor.calls, receipts.transitions)
	}
	if !json.Valid(result.Result) || !strings.Contains(string(result.Result), "/api/v1/charlie/operations/action-a") {
		t.Fatalf("deferred result is not bounded operation metadata: %s", result.Result)
	}
}

func TestActionGuardReadOnlyWriteCreatesActionableFindingWithoutDispatch(t *testing.T) {
	facts := allowedWriteFacts(ModeReadOnly)
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{}
	guard, privateKey := newTestActionGuard(t, authority, receipts, executor)
	findings := &actionFindingRecorder{}
	guard.SetFindingRecorder(findings, "installation-a")
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})

	result := guard.Execute(context.Background(), action)
	if result.Code != DeniedReadOnlyWrite || result.Finding == nil || !result.Finding.Actionable || receipts.claimCalls != 0 || executor.calls != 0 {
		t.Fatalf("read-only boundary failed: %+v", result)
	}
	if len(findings.inputs) != 1 {
		t.Fatalf("read-only denial did not create one durable finding: %+v", findings.inputs)
	}
	input := findings.inputs[0]
	if input.InstallationID != "installation-a" || input.ResourceType != "management_component" || input.ResourceID != "resource-a" || input.Mode != ModeReadOnly || input.Decision.Code != DeniedReadOnlyWrite || input.RecommendedCapability != "astronomer.queue.retry_task" {
		t.Fatalf("durable finding metadata is not exact and content-free: %+v", input)
	}
}

func TestActionGuardDoesNotClaimFindingWhenPersistenceFails(t *testing.T) {
	facts := allowedWriteFacts(ModeReadOnly)
	guard, privateKey := newTestActionGuard(t, &fakeLiveAuthority{facts: []AuthorityInput{facts}}, &fakeReceipts{}, &fakeCapabilityExecutor{})
	findings := &actionFindingRecorder{err: errors.New("database unavailable")}
	guard.SetFindingRecorder(findings, "installation-a")
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})

	result := guard.Execute(context.Background(), action)
	if result.Code != DeniedReadOnlyWrite || result.Finding != nil || len(findings.inputs) != 1 {
		t.Fatalf("non-durable finding was presented as actionable: result=%+v inputs=%+v", result, findings.inputs)
	}
}

func TestActionGuardRejectsTamperingUnknownCapabilityAndArguments(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{}
	guard, privateKey := newTestActionGuard(t, authority, receipts, executor)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})

	for name, mutate := range map[string]func(*ActionEnvelope){
		"argument": func(action *ActionEnvelope) {
			action.Arguments = json.RawMessage(`{"task_id":"other","operation_id":"action-a"}`)
		},
		"capability":  func(action *ActionEnvelope) { action.Capability = "astronomer.downstream.pods.delete" },
		"idempotency": func(action *ActionEnvelope) { action.IdempotencyKey = "different" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := action
			mutate(&candidate)
			result := guard.Execute(context.Background(), candidate)
			if result.Allowed || executor.calls != 0 {
				t.Fatalf("tampered action executed: %+v", result)
			}
		})
	}
}

func TestActionGuardRejectsSignedUnboundedAndRawInputsBeforeAuthority(t *testing.T) {
	authority := &fakeLiveAuthority{facts: []AuthorityInput{allowedWriteFacts(ModeApproval)}}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{}
	guard, privateKey := newTestActionGuard(t, authority, receipts, executor)
	cases := []ActionEnvelope{
		signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a", "url": "https://internal.invalid"}),
		signedTestAction(t, privateKey, "astronomer.management.workload_get", map[string]any{"workload": "deployment/../../secret"}),
		signedTestAction(t, privateKey, "astronomer.observability.health", map[string]any{"query_template": `up{token="secret"}`, "range": "1h"}),
		signedTestAction(t, privateKey, "astronomer.installation.configuration", map[string]any{"keys": []string{"database.password"}}),
	}
	for _, action := range cases {
		result := guard.Execute(context.Background(), action)
		if result.Code != DeniedScope || result.Allowed {
			t.Fatalf("unsafe signed arguments were not scope-denied: capability=%s result=%+v", action.Capability, result)
		}
	}
	if authority.calls != 0 || executor.calls != 0 {
		t.Fatalf("unsafe inputs reached authority/adapter: authority=%d adapter=%d", authority.calls, executor.calls)
	}
}

func TestActionGuardReturnsReceiptReplayWithoutExecuting(t *testing.T) {
	facts := allowedWriteFacts(ModeApproval)
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
	receipts := &fakeReceipts{claim: ReceiptClaim{Disposition: ReceiptReplay, Result: ActionResult{Allowed: true, State: "succeeded", Verified: true}}}
	executor := &fakeCapabilityExecutor{}
	guard, privateKey := newTestActionGuard(t, authority, receipts, executor)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})

	result := guard.Execute(context.Background(), action)
	if !result.Replay || executor.calls != 0 {
		t.Fatalf("replay re-executed: %+v", result)
	}
}

func TestActionGuardFailsClosedWhenLiveAuthorityUnavailable(t *testing.T) {
	authority := &fakeLiveAuthority{error: errors.New("db unavailable")}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{}
	guard, privateKey := newTestActionGuard(t, authority, receipts, executor)
	action := signedTestAction(t, privateKey, "astronomer.installation.summary", map[string]any{})
	result := guard.Execute(context.Background(), action)
	if result.Code != DeniedAuthorization || executor.calls != 0 {
		t.Fatalf("authority failure did not fail closed: %+v", result)
	}
}

func TestActionGuardDoesNotDispatchWhenDurableAuditFails(t *testing.T) {
	for _, phase := range []string{"approved", "dispatched"} {
		t.Run(phase, func(t *testing.T) {
			facts := allowedWriteFacts(ModeApproval)
			authority := &fakeLiveAuthority{facts: []AuthorityInput{facts, facts}}
			receipts := &fakeReceipts{}
			executor := &fakeCapabilityExecutor{verified: true}
			publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			auditor := &fakeActionAuditor{failAt: phase}
			guard, err := NewActionGuard(publicKey, authority, receipts, executor, auditor)
			if err != nil {
				t.Fatal(err)
			}
			action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})
			action.ApprovalID = "approval-a"
			payload, _ := json.Marshal(action.signed())
			action.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
			result := guard.Execute(context.Background(), action)
			if result.Allowed || authority.commitCalls != 0 || executor.calls != 0 || stringSlice(receipts.transitions) != stringSlice([]string{"blocked"}) {
				t.Fatalf("audit failure reached consumption/dispatch: phase=%s result=%+v commits=%d calls=%d transitions=%v", phase, result, authority.commitCalls, executor.calls, receipts.transitions)
			}
		})
	}
}

func stringSlice(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}
