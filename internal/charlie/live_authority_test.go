package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeLiveAuthorityQueries struct {
	connection    sqlc.CharlieConnection
	session       sqlc.CharlieSession
	delegation    sqlc.CharlieDelegation
	approval      sqlc.CharlieActionApproval
	triggerEvent  sqlc.CharlieTriggerEvent
	triggerRule   sqlc.CharlieTriggerRule
	resources     [][]sqlc.CharlieSessionResource
	resourceReads int
	error         error
	consumes      int
}

func (f *fakeLiveAuthorityQueries) GetCharlieTriggerEvent(context.Context, uuid.UUID) (sqlc.CharlieTriggerEvent, error) {
	if f.triggerEvent.ID == uuid.Nil {
		return sqlc.CharlieTriggerEvent{}, errors.New("trigger event not found")
	}
	return f.triggerEvent, f.error
}
func (f *fakeLiveAuthorityQueries) GetCharlieTriggerRule(context.Context, uuid.UUID) (sqlc.CharlieTriggerRule, error) {
	if f.triggerRule.ID == uuid.Nil {
		return sqlc.CharlieTriggerRule{}, errors.New("trigger rule not found")
	}
	return f.triggerRule, f.error
}

func (f *fakeLiveAuthorityQueries) GetCharlieConnectionByDeploymentID(context.Context, string) (sqlc.CharlieConnection, error) {
	return f.connection, f.error
}
func (f *fakeLiveAuthorityQueries) GetCharlieSessionByCentralID(context.Context, string) (sqlc.CharlieSession, error) {
	return f.session, f.error
}
func (f *fakeLiveAuthorityQueries) ListCharlieSessionResources(context.Context, uuid.UUID) ([]sqlc.CharlieSessionResource, error) {
	if f.error != nil || len(f.resources) == 0 {
		return nil, f.error
	}
	index := f.resourceReads
	if index >= len(f.resources) {
		index = len(f.resources) - 1
	}
	f.resourceReads++
	return f.resources[index], nil
}
func (f *fakeLiveAuthorityQueries) GetActiveCharlieDelegationByHash(context.Context, string) (sqlc.CharlieDelegation, error) {
	return f.delegation, f.error
}
func (f *fakeLiveAuthorityQueries) GetActiveCharlieActionApproval(_ context.Context, arg sqlc.GetActiveCharlieActionApprovalParams) (sqlc.CharlieActionApproval, error) {
	if f.error != nil || f.approval.CharlieActionID != arg.CharlieActionID || f.approval.ApprovalID != arg.ApprovalID {
		return sqlc.CharlieActionApproval{}, errors.New("approval not found")
	}
	return f.approval, nil
}
func (f *fakeLiveAuthorityQueries) ConsumeCharlieActionApproval(_ context.Context, arg sqlc.ConsumeCharlieActionApprovalParams) (sqlc.CharlieActionApproval, error) {
	if f.error != nil || arg.ArgumentDigest != f.approval.ArgumentDigest || arg.DisclosureDigest != f.approval.DisclosureDigest || arg.ModeRevision != f.approval.ModeRevision || arg.PolicyRevision != f.approval.PolicyRevision || arg.FencingEpoch != f.approval.FencingEpoch || arg.ResourceID != f.approval.ResourceID {
		return sqlc.CharlieActionApproval{}, errors.New("approval CAS failed")
	}
	f.consumes++
	return f.approval, nil
}

type fakeBindings struct {
	values map[uuid.UUID][]rbac.RoleBinding
	active map[uuid.UUID]bool
}

func (f fakeBindings) CurrentBindings(_ context.Context, principal uuid.UUID) ([]rbac.RoleBinding, bool, error) {
	return f.values[principal], f.active[principal], nil
}

type fakeSafety struct {
	facts   SafetyFacts
	commits int
	error   error
}

func (f *fakeSafety) Evaluate(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) (SafetyFacts, error) {
	return f.facts, f.error
}
func (f *fakeSafety) ConsumeAutoBudget(context.Context, ActionEnvelope, CapabilityDescriptor, map[string]json.RawMessage) error {
	f.commits++
	return f.error
}

func liveAuthorityFixture(mode Mode) (*fakeLiveAuthorityQueries, fakeBindings, *fakeSafety, ActionEnvelope, CapabilityDescriptor, uuid.UUID, uuid.UUID) {
	connectionID, sessionID := uuid.New(), uuid.New()
	principalID, approverID, automationID := uuid.New(), uuid.New(), uuid.New()
	connection := sqlc.CharlieConnection{
		ID: connectionID, ProductID: "product-a", ProductSlug: "astronomer", DeploymentID: "deployment-a", Active: true,
		RequestedMode: string(mode), VerifiedMode: string(mode), VerifiedModeRevision: 2,
		DisclosureDigest: "disclosure-a", FencingEpoch: 7,
	}
	session := sqlc.CharlieSession{ID: sessionID, ConnectionID: connectionID, CharlieSessionID: "session-a", State: "active"}
	delegation := sqlc.CharlieDelegation{SessionID: sessionID, PrincipalID: principalID, PrincipalType: "user", ExpiresAt: time.Now().Add(time.Minute)}
	action := ActionEnvelope{
		DeploymentID: "deployment-a", SessionID: "session-a", TurnID: "turn-a", ActionID: "action-a",
		ArgumentDigest: "arguments-a", AuthorizationRef: "opaque-a",
		DisclosureDigest: "disclosure-a", ModeRevision: 2, PolicyRevision: 2, FencingEpoch: 7, IdempotencyKey: "action-a",
	}
	if mode == ModeApproval {
		action.ApprovalID = "approval-a"
	}
	capability, _ := capabilityByName("astronomer.queue.retry_task")
	queries := &fakeLiveAuthorityQueries{connection: connection, session: session, delegation: delegation, resources: [][]sqlc.CharlieSessionResource{{{SessionID: sessionID, ResourceType: "management_component", ResourceID: "resource-a", RequiredVerb: "read"}}}}
	queries.approval = sqlc.CharlieActionApproval{
		ConnectionID: connectionID, SessionID: sessionID, ApprovalID: action.ApprovalID,
		CharlieActionID: action.ActionID, TurnID: action.TurnID, Capability: capability.Name,
		ArgumentDigest: action.ArgumentDigest, DisclosureDigest: action.DisclosureDigest,
		ModeRevision: action.ModeRevision, PolicyRevision: action.PolicyRevision, FencingEpoch: action.FencingEpoch,
		ManifestDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ResourceType: "management_component", ResourceID: "resource-a",
		ApproverID: approverID, State: "approved", ExpiresAt: time.Now().Add(time.Minute),
	}
	grant := func(resource string, verbs ...string) rbac.RoleBinding {
		return rbac.RoleBinding{RoleRules: []rbac.Rule{{Resource: resource, Verbs: verbs}}}
	}
	bindings := fakeBindings{
		values: map[uuid.UUID][]rbac.RoleBinding{
			principalID:  {grant("charlie", "create", "read"), grant("monitoring", "update")},
			approverID:   {grant("charlie", "approve"), grant("monitoring", "update")},
			automationID: {grant("charlie", "create", "read"), grant("monitoring", "update")},
		},
		active: map[uuid.UUID]bool{principalID: true, approverID: true, automationID: true},
	}
	safety := &fakeSafety{facts: SafetyFacts{Allowlisted: true, ScopeAllowed: true, BudgetAvailable: true, CooldownClear: true, CircuitClosed: true, PreconditionsMet: true, MaintenanceClear: true}}
	return queries, bindings, safety, action, capability, automationID, approverID
}

func liveWriteArguments(resourceID string) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"resource_id":  json.RawMessage(`"` + resourceID + `"`),
		"task_id":      json.RawMessage(`"task-a"`),
		"operation_id": json.RawMessage(`"action-a"`),
	}
}

func TestProductLiveAuthorityRejectsAbortedSessionEvenWithLiveDelegation(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeApproval)
	queries.session.State = "aborted"
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)
	if _, err := authority.Evaluate(context.Background(), action, capability, liveWriteArguments("resource-a")); err == nil {
		t.Fatal("aborted local session retained MCP authority")
	}
}

func TestProductLiveAuthorityAcceptsEquivalentPrefixedDisclosureDigest(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeApproval)
	queries.connection.DisclosureDigest = "sha256:" + action.DisclosureDigest
	authority, err := NewProductLiveAuthority(queries, bindings, safety, automationID)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := authority.Evaluate(t.Context(), action, capability, liveWriteArguments("resource-a"))
	if err != nil || !facts.DisclosureCurrent {
		t.Fatalf("equivalent disclosure digest encodings drifted: facts=%+v err=%v", facts, err)
	}
}

func TestProductLiveAuthorityApprovalRequiresApproverAndTargetPermission(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, approverID := liveAuthorityFixture(ModeApproval)
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)
	arguments := liveWriteArguments("resource-a")
	facts, err := authority.Evaluate(context.Background(), action, capability, arguments)
	if err != nil || !facts.LiveAuthorized || !facts.ApprovalPresent || !facts.ApprovalExact || facts.FindingResourceType != "management_component" || facts.FindingResourceID != "resource-a" {
		t.Fatalf("exact approval rejected: facts=%+v err=%v", facts, err)
	}
	bindings.values[approverID] = []rbac.RoleBinding{{RoleRules: []rbac.Rule{{Resource: "charlie", Verbs: []string{"approve"}}}}}
	facts, err = authority.Evaluate(context.Background(), action, capability, arguments)
	if err != nil || !facts.ApprovalPresent || facts.ApprovalExact {
		t.Fatalf("approver without target permission accepted: %+v err=%v", facts, err)
	}
}

func TestProductLiveAuthorityAutoRequiresExactAutomationIdentity(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeAuto)
	queries.delegation.PrincipalID = automationID
	queries.delegation.PrincipalType = "service"
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)
	arguments := liveWriteArguments("resource-a")
	facts, err := authority.Evaluate(context.Background(), action, capability, arguments)
	if err != nil || !facts.LiveAuthorized || !facts.AutoEligible || facts.InteractiveApprovalRequired {
		t.Fatalf("automation identity denied: %+v err=%v", facts, err)
	}
	if decision := DecideAuthority(facts, time.Now()); !decision.Allowed {
		t.Fatalf("automation identity should permit unattended auto: %+v", decision)
	}
	// A different service principal with the same grants keeps live RBAC but
	// must request exact approval instead of unattended auto.
	otherService := uuid.New()
	bindings.values[otherService] = bindings.values[automationID]
	bindings.active[otherService] = true
	queries.delegation.PrincipalID = otherService
	queries.delegation.PrincipalType = "service"
	facts, err = authority.Evaluate(context.Background(), action, capability, arguments)
	if err != nil || !facts.LiveAuthorized || !facts.InteractiveApprovalRequired {
		t.Fatalf("non-automation principal facts=%+v err=%v", facts, err)
	}
	if decision := DecideAuthority(facts, time.Now()); decision.Allowed || decision.Code != DeniedApprovalRequired {
		t.Fatalf("non-automation auto write should require approval, got %+v", decision)
	}
}

func TestProductLiveAuthorityEventSessionCannotExceedTriggerModeCeiling(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeAuto)
	queries.delegation.PrincipalID = automationID
	queries.delegation.PrincipalType = "service"
	queries.session.Source = "event"
	queries.session.ClientSessionID = uuid.New()
	queries.triggerRule = sqlc.CharlieTriggerRule{
		ID: uuid.New(), ConnectionID: queries.connection.ID, Enabled: true,
		ServiceIdentityID: automationID, ModeCeiling: string(ModeReadOnly),
	}
	queries.triggerEvent = sqlc.CharlieTriggerEvent{
		ID: queries.session.ClientSessionID, RuleID: queries.triggerRule.ID, State: "dispatched",
		SessionID: pgtype.UUID{Bytes: queries.session.ID, Valid: true},
	}
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)
	facts, err := authority.Evaluate(context.Background(), action, capability, liveWriteArguments("resource-a"))
	if err != nil || facts.Mode != ModeReadOnly {
		t.Fatalf("read-only trigger ceiling was not applied: facts=%+v err=%v", facts, err)
	}
	if decision := DecideAuthority(facts, time.Now()); decision.Allowed || decision.Code != DeniedReadOnlyWrite {
		t.Fatalf("read-only trigger authorized unattended write: %+v", decision)
	}

	queries.triggerRule.ModeCeiling = string(ModeAuto)
	facts, err = authority.Evaluate(context.Background(), action, capability, liveWriteArguments("resource-a"))
	if err != nil || facts.Mode != ModeAuto || facts.InteractiveApprovalRequired {
		t.Fatalf("explicit auto trigger ceiling did not admit service identity facts: %+v err=%v", facts, err)
	}
	if decision := DecideAuthority(facts, time.Now()); !decision.Allowed {
		t.Fatalf("explicit auto trigger ceiling denied bounded automatic action: %+v", decision)
	}
}

func TestProductLiveAuthorityHumanAutoWriteRequiresApprovalNotRBACDenial(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeAuto)
	// Fixture defaults to human user principal with live target RBAC.
	queries.delegation.PrincipalType = "user"
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)
	facts, err := authority.Evaluate(context.Background(), action, capability, liveWriteArguments("resource-a"))
	if err != nil {
		t.Fatal(err)
	}
	if !facts.LiveAuthorized {
		t.Fatalf("human with target RBAC should stay LiveAuthorized: %+v", facts)
	}
	if !facts.InteractiveApprovalRequired {
		t.Fatalf("human auto write must require interactive approval: %+v", facts)
	}
	decision := DecideAuthority(facts, time.Now())
	if decision.Allowed || decision.Code != DeniedApprovalRequired {
		t.Fatalf("want approval_required for human auto write, got %+v", decision)
	}
}

func TestProductLiveAuthorityAutomationCeilingPreservesExactApproval(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeAuto)
	action.ApprovalID = "approval-a"
	queries.approval.ApprovalID = action.ApprovalID
	// The user approval path is deliberately not auto-eligible by policy facts;
	// its authority comes only from the exact approval and live user/approver RBAC.
	safety.facts.Allowlisted = false
	safety.facts.ScopeAllowed = false
	safety.facts.BudgetAvailable = false
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)
	arguments := liveWriteArguments("resource-a")
	facts, err := authority.Evaluate(context.Background(), action, capability, arguments)
	if err != nil || !facts.LiveAuthorized || !facts.ApprovalRequested || !facts.ApprovalPresent || !facts.ApprovalExact {
		t.Fatalf("automation ceiling lost exact approval path: facts=%+v err=%v", facts, err)
	}
	if decision := DecideAuthority(facts, time.Now()); !decision.Allowed {
		t.Fatalf("exact approval under automation ceiling denied: %+v", decision)
	}
	if err := authority.Commit(context.Background(), action, capability, arguments, facts); err != nil || queries.consumes != 1 || safety.commits != 0 {
		t.Fatalf("automation-ceiling approval commit consumed wrong authority: approvals=%d auto=%d err=%v", queries.consumes, safety.commits, err)
	}
}

func TestProductLiveAuthorityAutomationInvalidApprovalCannotFallBackToAuto(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeAuto)
	action.ApprovalID = "approval-missing"
	queries.delegation.PrincipalID = automationID
	queries.delegation.PrincipalType = "service"
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)
	facts, err := authority.Evaluate(context.Background(), action, capability, liveWriteArguments("resource-a"))
	if err != nil {
		t.Fatal(err)
	}
	decision := DecideAuthority(facts, time.Now())
	if decision.Allowed || decision.Code != DeniedApprovalInvalid {
		t.Fatalf("invalid requested approval fell back to automation: facts=%+v decision=%+v", facts, decision)
	}
}

func TestProductLiveAuthorityDriftAndEmergencyCanOnlyDeny(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeApproval)
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)
	queries.connection.EmergencyDisabled = true
	queries.connection.VerifiedMode = string(ModeAuto)
	arguments := liveWriteArguments("resource-a")
	facts, err := authority.Evaluate(context.Background(), action, capability, arguments)
	if err != nil || !facts.EmergencyDisabled || facts.Mode != ModeDisabled {
		t.Fatalf("emergency did not reduce authority: %+v err=%v", facts, err)
	}
	queries.connection.EmergencyDisabled = false
	queries.connection.DisclosureDigest = "changed"
	facts, _ = authority.Evaluate(context.Background(), action, capability, arguments)
	if facts.DisclosureCurrent {
		t.Fatal("disclosure drift accepted")
	}
	queries.connection.DisclosureDigest = action.DisclosureDigest
	action.PolicyRevision++
	facts, _ = authority.Evaluate(context.Background(), action, capability, arguments)
	if facts.DisclosureCurrent {
		t.Fatal("automation policy revision drift accepted")
	}
}

func TestProductLiveAuthorityCommitConsumesApprovalOrAutoBudgetOnce(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeApproval)
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)
	arguments := liveWriteArguments("resource-a")
	facts, _ := authority.Evaluate(context.Background(), action, capability, arguments)
	if err := authority.Commit(context.Background(), action, capability, arguments, facts); err != nil || queries.consumes != 1 || safety.commits != 0 {
		t.Fatalf("approval commit failed: consumes=%d auto=%d err=%v", queries.consumes, safety.commits, err)
	}
	queries.connection.RequestedMode = string(ModeAuto)
	queries.connection.VerifiedMode = string(ModeAuto)
	facts.Mode = ModeAuto
	facts.ApprovalRequested = false
	action.ApprovalID = ""
	if err := authority.Commit(context.Background(), action, capability, arguments, facts); err != nil || safety.commits != 1 {
		t.Fatalf("auto budget commit failed: %v", err)
	}
}

func TestProductLiveAuthorityReadsFeatureGateForEveryCall(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeReadOnly)
	authority, err := NewProductLiveAuthorityWithFeatures(queries, bindings, safety, automationID, gateFeature(false))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := authority.Evaluate(context.Background(), action, capability, liveWriteArguments("resource-a"))
	if err != nil {
		t.Fatal(err)
	}
	if facts.FeatureEnabled || DecideAuthority(facts, time.Now()).Code != DeniedFeatureDisabled {
		t.Fatalf("disabled feature retained MCP authority: %+v", facts)
	}
}

func TestProductLiveAuthorityRequiresExactSessionResourceAtEvaluateAndCommit(t *testing.T) {
	queries, bindings, safety, action, capability, automationID, _ := liveAuthorityFixture(ModeApproval)
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)

	if _, err := authority.Evaluate(context.Background(), action, capability, map[string]json.RawMessage{}); err == nil {
		t.Fatal("write without resource_id retained authority")
	}
	if _, err := authority.Evaluate(context.Background(), action, capability, liveWriteArguments("resource-other")); err == nil {
		t.Fatal("write for undisclosed resource retained authority")
	}
	queries.resources = [][]sqlc.CharlieSessionResource{{{SessionID: queries.session.ID, ResourceType: "management_component", ResourceID: "resource-a", RequiredVerb: "update"}}}
	queries.resourceReads = 0
	if _, err := authority.Evaluate(context.Background(), action, capability, liveWriteArguments("resource-a")); err == nil {
		t.Fatal("non-read ProductContext row retained write authority")
	}

	queries.resources = [][]sqlc.CharlieSessionResource{{{SessionID: queries.session.ID, ResourceType: "management_component", ResourceID: "resource-a", RequiredVerb: "read"}}}
	queries.resourceReads = 0
	arguments := liveWriteArguments("resource-a")
	facts, err := authority.Evaluate(context.Background(), action, capability, arguments)
	if err != nil || !facts.LiveAuthorized || !facts.ApprovalExact {
		t.Fatalf("exact session resource was denied: facts=%+v err=%v", facts, err)
	}

	queries.resourceReads = 0
	queries.resources = [][]sqlc.CharlieSessionResource{
		{{SessionID: queries.session.ID, ResourceType: "management_component", ResourceID: "resource-a", RequiredVerb: "read"}},
		{{SessionID: queries.session.ID, ResourceType: "management_component", ResourceID: "resource-a", RequiredVerb: "update"}},
	}
	facts, err = authority.Evaluate(context.Background(), action, capability, arguments)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Commit(context.Background(), action, capability, arguments, facts); err == nil || queries.consumes != 0 || safety.commits != 0 {
		t.Fatalf("resource removed before commit was not fenced: consumes=%d safety_commits=%d err=%v", queries.consumes, safety.commits, err)
	}
}

func TestProductLiveAuthorityAllowsClusterAgentTriggerForAstronomerRemediation(t *testing.T) {
	queries, bindings, safety, action, _, automationID, approverID := liveAuthorityFixture(ModeApproval)
	capability, _ := capabilityByName("astronomer.tunnel.restart_component")
	queries.resources = [][]sqlc.CharlieSessionResource{{{
		SessionID: queries.session.ID, ResourceType: "agent_connection_record", ResourceID: "connection-1", RequiredVerb: "read",
	}}}
	queries.approval.Capability = capability.Name
	queries.approval.ResourceType = "agent_connection_record"
	queries.approval.ResourceID = "connection-1"
	principalID := queries.delegation.PrincipalID
	grant := rbac.RoleBinding{RoleRules: []rbac.Rule{{Resource: "agents", Verbs: []string{"update"}}}}
	bindings.values[principalID] = append(bindings.values[principalID], grant)
	bindings.values[approverID] = append(bindings.values[approverID], grant)
	authority, _ := NewProductLiveAuthority(queries, bindings, safety, automationID)
	arguments := map[string]json.RawMessage{
		"resource_id":  json.RawMessage(`"connection-1"`),
		"component":    json.RawMessage(`"server"`),
		"operation_id": json.RawMessage(`"action-a"`),
	}
	facts, err := authority.Evaluate(context.Background(), action, capability, arguments)
	if err != nil || !facts.LiveAuthorized || !facts.ApprovalExact || capability.ManagedTargetAccess {
		t.Fatalf("Astronomer-owned cluster-agent remediation was denied or widened downstream: capability=%+v facts=%+v err=%v", capability, facts, err)
	}
}
