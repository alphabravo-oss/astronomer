package charlie

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	adapterConformanceSchema = "charlie.product-adapter-conformance/v1"
	adapterResultSchema      = "charlie.product-adapter-result/v1"
	adapterCorpusPath        = "contract/pinned/conformance/product-adapter-conformance-v1.json"
)

var adapterConformanceCode = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

type adapterConformanceManifest struct {
	Schema          string `json:"schema"`
	OutcomeContract struct {
		StageNames    []string `json:"stage_names"`
		StageOutcomes []string `json:"stage_outcomes"`
		FinalOutcomes []string `json:"final_outcomes"`
	} `json:"outcome_contract"`
	Matrix struct {
		Modes      []string `json:"modes"`
		Identities []string `json:"identities"`
	} `json:"matrix"`
	Cases             []adapterConformanceCase   `json:"cases"`
	AdversarialCorpus []adapterConformanceAttack `json:"adversarial_corpus"`
}

type adapterConformanceInput struct {
	FeatureState      string `json:"feature_state"`
	ConnectionState   string `json:"connection_state"`
	EmergencyState    string `json:"emergency_state"`
	Mode              string `json:"mode"`
	Identity          string `json:"identity"`
	Effect            string `json:"effect"`
	ActorAuthorized   bool   `json:"actor_authorized"`
	TargetAuthorized  bool   `json:"target_authorized"`
	DisclosureState   string `json:"disclosure_state"`
	RevisionState     string `json:"revision_state"`
	ApprovalState     string `json:"approval_state"`
	AutomationState   string `json:"automation_state"`
	SafetyState       string `json:"safety_state"`
	PriorAttempt      string `json:"prior_attempt"`
	VerificationState string `json:"verification_state"`
}

type adapterConformanceExpected struct {
	Outcome       string `json:"outcome"`
	ReasonCode    string `json:"reason_code"`
	FailedStage   string `json:"failed_stage,omitempty"`
	DispatchCalls int    `json:"dispatch_calls"`
}

type adapterConformanceCase struct {
	ID       string                     `json:"id"`
	Input    adapterConformanceInput    `json:"input"`
	Expected adapterConformanceExpected `json:"expected"`
}

type adapterConformanceAttack struct {
	ID          string `json:"id"`
	Class       string `json:"class"`
	Surface     string `json:"surface"`
	Attempt     string `json:"attempt"`
	Outcome     string `json:"outcome"`
	ReasonCode  string `json:"reason_code"`
	FailedStage string `json:"failed_stage"`
}

type adapterConformanceStages struct {
	ModePermitted        string `json:"mode_permitted"`
	ActorAuthorized      string `json:"actor_authorized"`
	TargetAuthorized     string `json:"target_authorized"`
	ApprovalValid        string `json:"approval_valid"`
	AutomationAuthorized string `json:"automation_authorized"`
	SafetyAdmitted       string `json:"safety_admitted"`
	DispatchCommitted    string `json:"dispatch_committed"`
	PostVerified         string `json:"post_verified"`
}

type adapterConformanceResult struct {
	Schema     string                   `json:"schema"`
	Outcome    string                   `json:"outcome"`
	ReasonCode string                   `json:"reason_code"`
	Stages     adapterConformanceStages `json:"stages"`
}

type adapterConformanceAuthority struct {
	facts       AuthorityInput
	evaluateErr error
	commits     int
}

func (a *adapterConformanceAuthority) Evaluate(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) (AuthorityInput, error) {
	if a.evaluateErr != nil {
		return AuthorityInput{}, a.evaluateErr
	}
	return a.facts, nil
}

func (a *adapterConformanceAuthority) Commit(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage, AuthorityInput) error {
	a.commits++
	return nil
}

type adapterConformanceReceipts struct {
	disposition ReceiptDisposition
}

func (r *adapterConformanceReceipts) Claim(context.Context, ActionEnvelope, CapabilityDescriptor) (ReceiptClaim, error) {
	disposition := r.disposition
	if disposition == "" {
		disposition = ReceiptClaimed
	}
	return ReceiptClaim{Disposition: disposition}, nil
}

func (*adapterConformanceReceipts) Transition(context.Context, ActionEnvelope, string, ActionResult) error {
	return nil
}

type adapterConformanceExecutor struct {
	calls      int
	verify     bool
	verifyRuns int
}

func (e *adapterConformanceExecutor) Execute(context.Context, CapabilityDescriptor, map[string]json.RawMessage) (json.RawMessage, error) {
	e.calls++
	return json.RawMessage(`{"ok":true}`), nil
}

func (e *adapterConformanceExecutor) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	e.verifyRuns++
	return e.verify, nil
}

type adapterConformanceAuditor struct{}

func (adapterConformanceAuditor) Record(context.Context, string, ActionEnvelope, CapabilityDescriptor, ActionResult) error {
	return nil
}

type adapterConformanceVector struct {
	ID     string
	Input  adapterConformanceInput
	Attack *adapterConformanceAttack
	Want   adapterConformanceExpected
}

func TestAstronomerProductAdapterPassesPinnedCharlieConformance(t *testing.T) {
	manifest := loadAdapterConformanceManifest(t)
	vectors := make([]adapterConformanceVector, 0, len(manifest.Cases)+len(manifest.AdversarialCorpus)*len(manifest.Matrix.Modes)*len(manifest.Matrix.Identities))
	for _, test := range manifest.Cases {
		vectors = append(vectors, adapterConformanceVector{ID: test.ID, Input: test.Input, Want: test.Expected})
	}
	for i := range manifest.AdversarialCorpus {
		attack := &manifest.AdversarialCorpus[i]
		for _, mode := range manifest.Matrix.Modes {
			for _, identity := range manifest.Matrix.Identities {
				vectors = append(vectors, adapterConformanceVector{
					ID: attack.ID + "." + mode + "." + identity,
					Input: adapterConformanceInput{
						FeatureState: "enabled", ConnectionState: "enabled", EmergencyState: "armed",
						Mode: mode, Identity: identity, Effect: "write", ActorAuthorized: true, TargetAuthorized: true,
						DisclosureState: "current", RevisionState: "current", ApprovalState: "valid",
						AutomationState: "authorized", SafetyState: "admitted", PriorAttempt: "none", VerificationState: "not_run",
					},
					Attack: attack,
					Want:   adapterConformanceExpected{Outcome: attack.Outcome, ReasonCode: attack.ReasonCode, FailedStage: attack.FailedStage},
				})
			}
		}
	}

	for _, vector := range vectors {
		vector := vector
		t.Run(vector.ID, func(t *testing.T) {
			got, dispatches := runAstronomerConformanceVector(t, vector)
			if err := validateAdapterResultContract(got); err != nil {
				t.Fatalf("invalid typed adapter result: %v", err)
			}
			want := adapterExpectedResult(vector.Want, vector.Attack != nil)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("typed adapter result mismatch\n got: %+v\nwant: %+v", got, want)
			}
			if dispatches != vector.Want.DispatchCalls {
				t.Fatalf("product dispatches = %d, want %d", dispatches, vector.Want.DispatchCalls)
			}
		})
	}
}

func loadAdapterConformanceManifest(t *testing.T) adapterConformanceManifest {
	t.Helper()
	raw, err := os.ReadFile(adapterCorpusPath)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest adapterConformanceManifest
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("strictly decode pinned Charlie conformance manifest: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatal("pinned Charlie conformance manifest has trailing data")
	}
	wantStages := []string{"mode_permitted", "actor_authorized", "target_authorized", "approval_valid", "automation_authorized", "safety_admitted", "dispatch_committed", "post_verified"}
	wantStageOutcomes := []string{"allowed", "ask", "deny", "inactive", "expired", "stale", "ambiguous", "failed_verification", "not_reached"}
	wantFinalOutcomes := []string{"allowed", "ask", "deny", "inactive", "expired", "stale", "ambiguous", "failed_verification"}
	if manifest.Schema != adapterConformanceSchema || !reflect.DeepEqual(manifest.OutcomeContract.StageNames, wantStages) ||
		!reflect.DeepEqual(manifest.OutcomeContract.StageOutcomes, wantStageOutcomes) ||
		!reflect.DeepEqual(manifest.OutcomeContract.FinalOutcomes, wantFinalOutcomes) ||
		!reflect.DeepEqual(manifest.Matrix.Modes, []string{"disabled", "read_only", "approval", "auto"}) ||
		!reflect.DeepEqual(manifest.Matrix.Identities, []string{"interactive_user", "reader", "approver", "administrator", "automation"}) {
		t.Fatal("pinned Charlie conformance manifest changed its closed v1 vocabulary")
	}
	seen := map[string]bool{}
	for _, test := range manifest.Cases {
		validateAdapterConformanceID(t, seen, test.ID)
		validateAdapterConformanceExpected(t, test.Expected)
	}
	for _, attack := range manifest.AdversarialCorpus {
		validateAdapterConformanceID(t, seen, attack.ID)
		if !adapterConformanceCode.MatchString(attack.Class) || !adapterConformanceCode.MatchString(attack.Surface) ||
			!adapterConformanceCode.MatchString(attack.Attempt) || !adapterConformanceCode.MatchString(attack.ReasonCode) ||
			(attack.Outcome != "deny" && attack.Outcome != "ambiguous") || !containsAdapterStage(wantStages, attack.FailedStage) {
			t.Fatalf("invalid adversarial contract entry %q", attack.ID)
		}
	}
	if len(manifest.Cases) == 0 || len(manifest.AdversarialCorpus) == 0 {
		t.Fatal("pinned Charlie conformance corpus is empty")
	}
	return manifest
}

func validateAdapterConformanceID(t *testing.T, seen map[string]bool, id string) {
	t.Helper()
	if seen[id] || !adapterConformanceCode.MatchString(id) {
		t.Fatalf("invalid or duplicate conformance id %q", id)
	}
	seen[id] = true
}

func validateAdapterConformanceExpected(t *testing.T, expected adapterConformanceExpected) {
	t.Helper()
	final := map[string]bool{"allowed": true, "ask": true, "deny": true, "inactive": true, "expired": true, "stale": true, "ambiguous": true, "failed_verification": true}
	if !final[expected.Outcome] || !adapterConformanceCode.MatchString(expected.ReasonCode) || expected.DispatchCalls < 0 || expected.DispatchCalls > 1 {
		t.Fatalf("invalid expected result %+v", expected)
	}
	if expected.Outcome == "allowed" {
		if expected.FailedStage != "" || expected.DispatchCalls != 1 {
			t.Fatalf("conflicting allowed result %+v", expected)
		}
		return
	}
	if !containsAdapterStage([]string{"mode_permitted", "actor_authorized", "target_authorized", "approval_valid", "automation_authorized", "safety_admitted", "dispatch_committed", "post_verified"}, expected.FailedStage) ||
		(expected.Outcome == "failed_verification") != (expected.DispatchCalls == 1) {
		t.Fatalf("conflicting blocked result %+v", expected)
	}
}

func containsAdapterStage(stages []string, wanted string) bool {
	for _, stage := range stages {
		if stage == wanted {
			return true
		}
	}
	return false
}

func runAstronomerConformanceVector(t *testing.T, vector adapterConformanceVector) (adapterConformanceResult, int) {
	t.Helper()
	if vector.Attack != nil {
		return runAstronomerConformanceAttack(t, vector)
	}
	facts := adapterInputAuthority(vector.Input)
	authority := &adapterConformanceAuthority{facts: facts}
	receipts := &adapterConformanceReceipts{}
	executor := &adapterConformanceExecutor{verify: vector.Input.VerificationState != "failed"}
	guard, privateKey := newAdapterConformanceGuard(t, authority, receipts, executor)
	action := adapterSignedAction(t, privateKey, vector.Input.Effect, vector.Input.ApprovalState)
	raw := guard.Execute(context.Background(), action)
	return normalizeAstronomerAdapterResult(t, vector.Input, raw), executor.calls
}

func adapterInputAuthority(input adapterConformanceInput) AuthorityInput {
	effect := Effect(input.Effect)
	mode := Mode(input.Mode)
	now := time.Now().UTC()
	facts := AuthorityInput{
		FeatureEnabled: input.FeatureState == "enabled", ConnectionActive: input.ConnectionState == "enabled",
		EmergencyDisabled: input.EmergencyState != "armed", Mode: mode, Effect: effect,
		DisclosureCurrent: input.DisclosureState == "current", LiveAuthorized: input.ActorAuthorized,
		FindingResourceType: "management_component", FindingResourceID: "resource-a",
		AutoEligible: true, Allowlisted: input.AutomationState != "denied", ScopeAllowed: input.TargetAuthorized,
		BudgetAvailable: true, CooldownClear: true, CircuitClosed: true, PreconditionsMet: input.SafetyState == "admitted",
		MaintenanceClear: true, IdempotencyKeyPresent: true, VerificationDeclared: true, FencingEpoch: 7, CurrentFencingEpoch: 7,
		AmbiguousPriorAttempt: input.PriorAttempt == "ambiguous",
	}
	if input.RevisionState != "current" {
		facts.CurrentFencingEpoch++
	}
	switch input.ApprovalState {
	case "valid":
		facts.ApprovalPresent, facts.ApprovalExact, facts.ApprovalExpiresAt = true, true, now.Add(time.Minute)
	case "expired":
		facts.ApprovalPresent, facts.ApprovalExact, facts.ApprovalExpiresAt = true, true, now.Add(-time.Minute)
	}
	facts.ApprovalRequested = mode == ModeApproval
	return facts
}

func newAdapterConformanceGuard(t *testing.T, authority *adapterConformanceAuthority, receipts *adapterConformanceReceipts, executor *adapterConformanceExecutor) (*ActionGuard, ed25519.PrivateKey) {
	t.Helper()
	seed := sha256.Sum256([]byte("astronomer-product-adapter-conformance-key; not a credential"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	guard, err := NewActionGuard(privateKey.Public().(ed25519.PublicKey), authority, receipts, executor, adapterConformanceAuditor{})
	if err != nil {
		t.Fatal(err)
	}
	guard.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return guard, privateKey
}

func adapterSignedAction(t *testing.T, privateKey ed25519.PrivateKey, effect, approvalState string) ActionEnvelope {
	t.Helper()
	capability := "astronomer.installation.summary"
	arguments := map[string]any{}
	if effect == "write" {
		capability = "astronomer.queue.retry_task"
		arguments = map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "conformance-action"}
	}
	canonical, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	action := ActionEnvelope{
		Version: "charlie-action/v1", DeploymentID: "deployment-a", SessionID: "session-a", TurnID: "turn-a",
		ActionID: "conformance-action", Capability: capability, Arguments: canonical,
		ArgumentDigest: capabilityArgumentDigest(capability, canonical), AuthorizationRef: "opaque-reference",
		DisclosureDigest: "disclosure-a", ModeRevision: 2, PolicyRevision: 2, FencingEpoch: 7,
		ExpiresAt: time.Now().UTC().Add(time.Minute), IdempotencyKey: "conformance-action",
	}
	if approvalState == "valid" || approvalState == "expired" {
		action.ApprovalID = "approval-a"
	}
	adapterSignAction(t, privateKey, &action)
	return action
}

func adapterSignAction(t *testing.T, privateKey ed25519.PrivateKey, action *ActionEnvelope) {
	t.Helper()
	payload, err := actionEnvelopeSigningBytes(*action)
	if err != nil {
		t.Fatal(err)
	}
	action.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
}

func normalizeAstronomerAdapterResult(t *testing.T, input adapterConformanceInput, raw ActionResult) adapterConformanceResult {
	t.Helper()
	if raw.Allowed {
		switch raw.State {
		case "succeeded":
			return adapterResult("allowed", "adapter.allowed", "", false)
		case "failed":
			return adapterResult("failed_verification", "adapter.verification_failed", "post_verified", false)
		default:
			t.Fatalf("unrecognized allowed ActionGuard state %q", raw.State)
		}
	}
	switch raw.Code {
	case DeniedFeatureDisabled:
		return adapterResult("inactive", "adapter.feature_inactive", "mode_permitted", false)
	case DeniedConnectionInactive:
		return adapterResult("inactive", "adapter.connection_inactive", "mode_permitted", false)
	case DeniedEmergencyDisabled:
		return adapterResult("inactive", "adapter.emergency_disabled", "mode_permitted", false)
	case DeniedModeDisabled:
		return adapterResult("inactive", "adapter.mode_inactive", "mode_permitted", false)
	case DeniedReadOnlyWrite:
		return adapterResult("deny", "adapter.read_only", "mode_permitted", false)
	case DeniedAuthorization:
		return adapterResult("deny", "adapter.actor_denied", "actor_authorized", false)
	case DeniedScope:
		return adapterResult("deny", "adapter.target_denied", "target_authorized", false)
	case DeniedApprovalRequired:
		return adapterResult("ask", "adapter.approval_required", "approval_valid", false)
	case DeniedApprovalInvalid:
		if input.ApprovalState == "expired" {
			return adapterResult("expired", "adapter.approval_expired", "approval_valid", false)
		}
		return adapterResult("deny", "adapter.approval_invalid", "approval_valid", false)
	case DeniedNotAutoEligible, DeniedNotAllowlisted:
		return adapterResult("ask", "adapter.approval_required", "automation_authorized", false)
	case DeniedDisclosureChanged:
		return adapterResult("stale", "adapter.disclosure_stale", "safety_admitted", false)
	case DeniedStaleFencing:
		return adapterResult("stale", "adapter.revision_stale", "safety_admitted", false)
	case DeniedAmbiguousPriorAttempt:
		return adapterResult("ambiguous", "adapter.ambiguous_prior_attempt", "dispatch_committed", false)
	case DeniedPrecondition, DeniedDestructive, DeniedMaintenance, DeniedBudget, DeniedCooldown, DeniedCircuitOpen, DeniedIdempotency, DeniedVerification:
		return adapterResult("deny", "adapter.safety_denied", "safety_admitted", false)
	default:
		t.Fatalf("unrecognized ActionGuard denial %q", raw.Code)
	}
	return adapterConformanceResult{}
}

func runAstronomerConformanceAttack(t *testing.T, vector adapterConformanceVector) (adapterConformanceResult, int) {
	t.Helper()
	facts := adapterInputAuthority(vector.Input)
	authority := &adapterConformanceAuthority{facts: facts}
	receipts := &adapterConformanceReceipts{}
	executor := &adapterConformanceExecutor{verify: true}
	guard, privateKey := newAdapterConformanceGuard(t, authority, receipts, executor)
	action := adapterSignedAction(t, privateKey, "write", "valid")

	switch vector.Attack.Class {
	case "shell", "exec", "raw_sql", "generic_http", "proxy", "secret_access", "credential_access", "delete", "downstream_transport":
		action.Capability = "astronomer.forbidden." + vector.Attack.Attempt
		action.Arguments = json.RawMessage(`{}`)
		action.ArgumentDigest = capabilityArgumentDigest(action.Capability, action.Arguments)
		adapterSignAction(t, privateKey, &action)
		if _, exists := capabilityByName(action.Capability); exists {
			t.Fatalf("forbidden capability %q entered the product catalog", action.Capability)
		}
	case "forged_authority":
		action.ApprovalID = "forged-after-signature"
	case "prompt_injection", "model_generated_approval":
		action.Signature = base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	case "actor_substitution":
		action.AuthorizationRef = "substituted-after-signature"
	case "target_substitution":
		action.Arguments = json.RawMessage(`{"resource_id":"resource-b","task_id":"task-a","operation_id":"conformance-action"}`)
	case "injected_authority_headers":
		runInjectedAuthorityHeaderAttack(t, guard, executor)
		if authority.commits != 0 {
			t.Fatalf("authority header consumed product authority: commits=%d", authority.commits)
		}
		return adapterResult(vector.Attack.Outcome, vector.Attack.ReasonCode, vector.Attack.FailedStage, true), executor.calls
	case "stale_action":
		action.ExpiresAt = time.Now().UTC().Add(-time.Minute)
		adapterSignAction(t, privateKey, &action)
	case "replay":
		receipts.disposition = ReceiptConflict
	case "ambiguous_result":
		receipts.disposition = ReceiptAmbiguous
	default:
		t.Fatalf("unimplemented Charlie adversarial class %q", vector.Attack.Class)
	}

	raw := guard.Execute(context.Background(), action)
	wantCode := expectedAstronomerAttackCode(vector.Attack.Class, Mode(vector.Input.Mode))
	if raw.Allowed || raw.Code != wantCode || executor.calls != 0 || authority.commits != 0 {
		t.Fatalf("adversarial vector escaped product adapter: raw=%+v want_code=%s dispatches=%d commits=%d", raw, wantCode, executor.calls, authority.commits)
	}
	return adapterResult(vector.Attack.Outcome, vector.Attack.ReasonCode, vector.Attack.FailedStage, true), executor.calls
}

func expectedAstronomerAttackCode(class string, mode Mode) DenialCode {
	switch class {
	case "shell", "exec", "raw_sql", "generic_http", "proxy", "secret_access", "credential_access", "delete", "downstream_transport":
		return DeniedScope
	case "forged_authority", "prompt_injection", "model_generated_approval", "actor_substitution", "stale_action":
		return DeniedAuthorization
	case "target_substitution":
		return DeniedIdempotency
	case "replay", "ambiguous_result":
		switch mode {
		case ModeDisabled:
			return DeniedModeDisabled
		case ModeReadOnly:
			return DeniedReadOnlyWrite
		case ModeApproval, ModeAuto:
			if class == "replay" {
				return DeniedIdempotency
			}
			return DeniedAmbiguousPriorAttempt
		}
	}
	return ""
}

func runInjectedAuthorityHeaderAttack(t *testing.T, guard *ActionGuard, executor *adapterConformanceExecutor) {
	t.Helper()
	const identity = "spiffe://astronomer.local/installations/installation-a/charlie-agent-mcp"
	handler, err := NewMCPHandler(guard, func(context.Context) bool { return true }, identity)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://astronomer-charlie-mcp.astronomer.svc:7444/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"one","method":"tools/list","params":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer forbidden-conformance-canary")
	uri, err := url.Parse(identity)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{Raw: []byte{1, 2, 3}, URIs: []*url.URL{uri}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || executor.calls != 0 || !strings.Contains(response.Body.String(), "unsupported_credential") || strings.Contains(response.Body.String(), "forbidden-conformance-canary") {
		t.Fatalf("authority header was not rejected safely: status=%d dispatches=%d body=%s", response.Code, executor.calls, response.Body.String())
	}
}

func adapterExpectedResult(expected adapterConformanceExpected, preflight bool) adapterConformanceResult {
	return adapterResult(expected.Outcome, expected.ReasonCode, expected.FailedStage, preflight)
}

func adapterResult(outcome, reason, failedStage string, preflight bool) adapterConformanceResult {
	order := []string{"mode_permitted", "actor_authorized", "target_authorized", "approval_valid", "automation_authorized", "safety_admitted", "dispatch_committed", "post_verified"}
	values := map[string]string{}
	failed := false
	for _, stage := range order {
		switch {
		case failedStage == "":
			values[stage] = "allowed"
		case preflight && stage == failedStage:
			values[stage] = outcome
		case preflight:
			values[stage] = "not_reached"
		case stage == failedStage:
			values[stage] = outcome
			failed = true
		case failed:
			values[stage] = "not_reached"
		default:
			values[stage] = "allowed"
		}
	}
	return adapterConformanceResult{Schema: adapterResultSchema, Outcome: outcome, ReasonCode: reason, Stages: adapterConformanceStages{
		ModePermitted: values["mode_permitted"], ActorAuthorized: values["actor_authorized"], TargetAuthorized: values["target_authorized"],
		ApprovalValid: values["approval_valid"], AutomationAuthorized: values["automation_authorized"], SafetyAdmitted: values["safety_admitted"],
		DispatchCommitted: values["dispatch_committed"], PostVerified: values["post_verified"],
	}}
}

func TestPinnedCharlieConformanceResultRejectsUnknownOrConflictingOutcomes(t *testing.T) {
	for name, result := range map[string]adapterConformanceResult{
		"unknown final":        adapterResult("permitted", "adapter.allowed", "", false),
		"missing code":         adapterResult("deny", "", "safety_admitted", false),
		"allowed with failure": adapterResult("allowed", "adapter.allowed", "safety_admitted", false),
		"unknown stage outcome": func() adapterConformanceResult {
			value := adapterResult("deny", "adapter.safety_denied", "safety_admitted", false)
			value.Stages.SafetyAdmitted = "maybe"
			return value
		}(),
		"conflicting stage outcome": func() adapterConformanceResult {
			value := adapterResult("deny", "adapter.safety_denied", "safety_admitted", false)
			value.Stages.SafetyAdmitted = "ask"
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if validateAdapterResultContract(result) == nil {
				t.Fatalf("invalid typed outcome was accepted: %+v", result)
			}
		})
	}
}

func validateAdapterResultContract(result adapterConformanceResult) error {
	final := map[string]bool{"allowed": true, "ask": true, "deny": true, "inactive": true, "expired": true, "stale": true, "ambiguous": true, "failed_verification": true}
	stage := map[string]bool{"allowed": true, "ask": true, "deny": true, "inactive": true, "expired": true, "stale": true, "ambiguous": true, "failed_verification": true, "not_reached": true}
	if result.Schema != adapterResultSchema || !final[result.Outcome] || !adapterConformanceCode.MatchString(result.ReasonCode) {
		return fmt.Errorf("invalid product adapter result envelope")
	}
	values := []string{result.Stages.ModePermitted, result.Stages.ActorAuthorized, result.Stages.TargetAuthorized, result.Stages.ApprovalValid,
		result.Stages.AutomationAuthorized, result.Stages.SafetyAdmitted, result.Stages.DispatchCommitted, result.Stages.PostVerified}
	failureIndex := -1
	for index, value := range values {
		if !stage[value] {
			return fmt.Errorf("invalid product adapter stage outcome")
		}
		if result.Outcome == "allowed" && value != "allowed" {
			return fmt.Errorf("conflicting allowed product adapter stage outcome")
		}
		if value != "allowed" && value != "not_reached" {
			if failureIndex >= 0 || value != result.Outcome {
				return fmt.Errorf("conflicting product adapter stage outcome")
			}
			failureIndex = index
		}
	}
	if result.Outcome == "allowed" {
		return nil
	}
	if failureIndex < 0 {
		return fmt.Errorf("conflicting product adapter stage outcome")
	}
	preflight := values[0] == "not_reached"
	for index, value := range values {
		if index == failureIndex {
			continue
		}
		if (index < failureIndex && !preflight && value != "allowed") || (index < failureIndex && preflight && value != "not_reached") || (index > failureIndex && value != "not_reached") {
			return fmt.Errorf("invalid product adapter stage ordering")
		}
	}
	return nil
}
