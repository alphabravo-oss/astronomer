package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type approvalAccessStoreFake struct {
	connection       sqlc.CharlieConnection
	session          sqlc.CharlieSession
	resources        []sqlc.CharlieSessionResource
	approval         sqlc.CharlieActionApproval
	created          int
	finding          sqlc.CharlieFinding
	findingResources []sqlc.CharlieFindingResource
}

func (f *approvalAccessStoreFake) ListCharlieApprovalCandidateSessions(context.Context) ([]sqlc.CharlieSession, error) {
	return []sqlc.CharlieSession{f.session}, nil
}
func (f *approvalAccessStoreFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *approvalAccessStoreFake) GetCharlieSession(_ context.Context, id uuid.UUID) (sqlc.CharlieSession, error) {
	if id != f.session.ID {
		return sqlc.CharlieSession{}, errors.New("not found")
	}
	return f.session, nil
}
func (f *approvalAccessStoreFake) ListCharlieSessionResources(context.Context, uuid.UUID) ([]sqlc.CharlieSessionResource, error) {
	return f.resources, nil
}
func (f *approvalAccessStoreFake) ListCharlieSessionsForOwner(context.Context, sqlc.ListCharlieSessionsForOwnerParams) ([]sqlc.CharlieSession, error) {
	return []sqlc.CharlieSession{f.session}, nil
}
func (f *approvalAccessStoreFake) AbortCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error) {
	return f.session, nil
}
func (f *approvalAccessStoreFake) RevokeCharlieDelegationsForSession(context.Context, uuid.UUID) (int64, error) {
	return 1, nil
}
func (f *approvalAccessStoreFake) UpdateCharlieSessionCursor(context.Context, sqlc.UpdateCharlieSessionCursorParams) (sqlc.CharlieSession, error) {
	return f.session, nil
}
func (f *approvalAccessStoreFake) CreateCharlieDelegation(_ context.Context, arg sqlc.CreateCharlieDelegationParams) (sqlc.CharlieDelegation, error) {
	return sqlc.CharlieDelegation{ID: uuid.New(), SessionID: arg.SessionID, PrincipalID: arg.PrincipalID, PrincipalType: arg.PrincipalType, ExpiresAt: arg.ExpiresAt}, nil
}
func (f *approvalAccessStoreFake) GetActiveCharlieDelegationByHash(context.Context, string) (sqlc.CharlieDelegation, error) {
	return sqlc.CharlieDelegation{}, errors.New("unused")
}
func (f *approvalAccessStoreFake) GetCharlieActionApprovalByApprovalID(_ context.Context, id string) (sqlc.CharlieActionApproval, error) {
	if f.approval.ApprovalID == id {
		return f.approval, nil
	}
	return sqlc.CharlieActionApproval{}, errors.New("not found")
}
func (f *approvalAccessStoreFake) CreateCharlieActionApproval(_ context.Context, arg sqlc.CreateCharlieActionApprovalParams) (sqlc.CharlieActionApproval, error) {
	if f.approval.ID != uuid.Nil {
		return sqlc.CharlieActionApproval{}, errors.New("duplicate")
	}
	f.created++
	f.approval = sqlc.CharlieActionApproval{
		ID: uuid.New(), ConnectionID: arg.ConnectionID, SessionID: arg.SessionID, ApprovalID: arg.ApprovalID,
		CharlieActionID: arg.CharlieActionID, TurnID: arg.TurnID, Capability: arg.Capability,
		ArgumentDigest: arg.ArgumentDigest, DisclosureDigest: arg.DisclosureDigest,
		ModeRevision: arg.ModeRevision, PolicyRevision: arg.PolicyRevision, FencingEpoch: arg.FencingEpoch,
		ManifestDigest: arg.ManifestDigest, ApproverID: arg.ApproverID, RationaleDigest: arg.RationaleDigest,
		ResourceType: arg.ResourceType, ResourceID: arg.ResourceID,
		State: "pending", ExpiresAt: arg.ExpiresAt,
	}
	return f.approval, nil
}
func (f *approvalAccessStoreFake) ApproveCharlieActionApproval(_ context.Context, id uuid.UUID) (sqlc.CharlieActionApproval, error) {
	if id != f.approval.ID || f.approval.State != "pending" {
		return sqlc.CharlieActionApproval{}, errors.New("CAS")
	}
	f.approval.State = "approved"
	return f.approval, nil
}
func (f *approvalAccessStoreFake) TransitionCharlieActionApproval(_ context.Context, arg sqlc.TransitionCharlieActionApprovalParams) (sqlc.CharlieActionApproval, error) {
	if arg.ID != f.approval.ID || (f.approval.State != "pending" && f.approval.State != "approved") {
		return sqlc.CharlieActionApproval{}, errors.New("CAS")
	}
	f.approval.State = arg.NextState
	return f.approval, nil
}
func (f *approvalAccessStoreFake) GetCharlieFindingByApprovalID(_ context.Context, id pgtype.Text) (sqlc.CharlieFinding, error) {
	if f.finding.ID != uuid.Nil && f.finding.ApprovalID.Valid && f.finding.ApprovalID.String == id.String {
		return f.finding, nil
	}
	return sqlc.CharlieFinding{}, pgx.ErrNoRows
}
func (f *approvalAccessStoreFake) UpsertCharlieApprovalFinding(_ context.Context, arg sqlc.UpsertCharlieApprovalFindingParams) (sqlc.CharlieFinding, error) {
	if f.finding.ID == uuid.Nil {
		f.finding = sqlc.CharlieFinding{
			ID: uuid.New(), ConnectionID: arg.ConnectionID, CharlieFindingID: arg.CharlieFindingID,
			ApprovalID: arg.ApprovalID, SessionID: arg.SessionID, Source: "user", Severity: "warning",
			Status: "open", EffectiveMode: "approval", ExecutionBlockCode: "approval_required",
			DedupeFingerprint: arg.DedupeFingerprint, Title: arg.Title, Summary: arg.Summary,
			RecommendedActionLabel: arg.RecommendedActionLabel, RiskImpact: arg.RiskImpact,
			VerificationSummary: arg.VerificationSummary, RepeatCount: 1, ExpiresAt: arg.ExpiresAt,
		}
	}
	return f.finding, nil
}
func (f *approvalAccessStoreFake) TransitionCharlieFindingForApproval(_ context.Context, arg sqlc.TransitionCharlieFindingForApprovalParams) (sqlc.CharlieFinding, error) {
	if f.finding.ID == uuid.Nil || !f.finding.ApprovalID.Valid || f.finding.ApprovalID.String != arg.ApprovalID.String || (f.finding.Status != "open" && f.finding.Status != "acknowledged") {
		return sqlc.CharlieFinding{}, pgx.ErrNoRows
	}
	switch arg.ApprovalState {
	case "rejected":
		f.finding.Status, f.finding.ExecutionBlockCode = "resolved", "approval_rejected"
	case "expired":
		f.finding.Status, f.finding.ExecutionBlockCode = "expired", "approval_expired"
	}
	return f.finding, nil
}
func (f *approvalAccessStoreFake) AddCharlieFindingResource(_ context.Context, arg sqlc.AddCharlieFindingResourceParams) error {
	f.findingResources = append(f.findingResources, sqlc.CharlieFindingResource{FindingID: arg.FindingID, ResourceType: arg.ResourceType, ResourceID: arg.ResourceID, RequiredVerb: arg.RequiredVerb})
	return nil
}
func (f *approvalAccessStoreFake) ListCharlieFindingResources(context.Context, uuid.UUID) ([]sqlc.CharlieFindingResource, error) {
	return f.findingResources, nil
}

type approvalBridgeFake struct {
	approval contract.Approval
	fail     bool
	decides  int
}

func (f *approvalBridgeFake) ListApprovals(context.Context, string) ([]contract.Approval, error) {
	return []contract.Approval{f.approval}, nil
}
func (f *approvalBridgeFake) DecideApproval(_ context.Context, _ string, _ string, _ uuid.UUID, decision, _ string) (contract.Approval, error) {
	f.decides++
	if f.fail {
		return contract.Approval{}, errors.New("bridge")
	}
	result := f.approval
	if decision == "approve" {
		result.State = "approved"
	} else {
		result.State = "rejected"
	}
	return result, nil
}
func (*approvalBridgeFake) GetSession(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}
func (*approvalBridgeFake) GetHistory(context.Context, string, string, string, int) (json.RawMessage, error) {
	return nil, nil
}
func (*approvalBridgeFake) CreateMessage(context.Context, string, string, uuid.UUID, string) (json.RawMessage, error) {
	return nil, nil
}
func (*approvalBridgeFake) AbortSession(context.Context, string, string, uuid.UUID) error { return nil }
func (*approvalBridgeFake) StreamSessionEvents(context.Context, string, string, string, func(contract.Event) error) error {
	return nil
}

type approvalBindingsFake struct {
	actor   uuid.UUID
	target  bool
	approve bool
}

func (f approvalBindingsFake) CurrentBindings(_ context.Context, actor uuid.UUID) ([]rbac.RoleBinding, bool, error) {
	if actor != f.actor {
		return nil, false, nil
	}
	rules := []rbac.Rule{{Resource: "charlie", Verbs: []string{"read", "create"}}}
	if f.approve {
		rules = append(rules, rbac.Rule{Resource: "charlie", Verbs: []string{"approve"}})
	}
	if f.target {
		rules = append(rules, rbac.Rule{Resource: "monitoring", Verbs: []string{"update"}})
	}
	return []rbac.RoleBinding{{RoleRules: rules}}, true, nil
}
func (f approvalBindingsFake) CanUseCharlie(ctx context.Context, actor uuid.UUID) (bool, error) {
	_, active, err := f.CurrentBindings(ctx, actor)
	return active, err
}
func (f approvalBindingsFake) CanReadIncidentResources(context.Context, uuid.UUID, []sqlc.CharlieSessionResource) (bool, error) {
	return true, nil
}

type approvalAuditFake struct {
	events   []ApprovalLifecycleAudit
	findings []FindingAlert
}

func (f *approvalAuditFake) RecordCharlieApprovalLifecycle(_ context.Context, event ApprovalLifecycleAudit) {
	f.events = append(f.events, event)
}
func (*approvalAuditFake) RecordCharlieSessionLifecycle(context.Context, SessionLifecycleAudit) {}
func (f *approvalAuditFake) PublishCharlieFindingLifecycle(_ context.Context, alert FindingAlert) {
	f.findings = append(f.findings, alert)
}

func approvalAccessFixture(t *testing.T) (*ApprovalAccessService, *approvalAccessStoreFake, *approvalBridgeFake, uuid.UUID) {
	t.Helper()
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "action-signing-public-key")
	if err := os.WriteFile(keyPath, publicKey, 0o600); err != nil {
		t.Fatal(err)
	}
	keyDigest := sha256.Sum256(publicKey)
	connectionID, sessionID, actorID := uuid.New(), uuid.New(), uuid.New()
	disclosure := strings.Repeat("b", 64)
	connection := sqlc.CharlieConnection{
		ID: connectionID, InstallationID: uuid.New(), ProductID: "product-a", ProductSlug: "astronomer", DeploymentID: "deployment-a", Active: true,
		RequestedMode: "approval", VerifiedMode: "approval", VerifiedModeRevision: 2,
		DisclosureDigest: disclosure, FencingEpoch: 7, SigningKeyFingerprint: hex.EncodeToString(keyDigest[:]),
	}
	session := sqlc.CharlieSession{
		ID: sessionID, ConnectionID: connectionID, CharlieSessionID: "session-a", State: "waiting_approval",
		Source: "user", Visibility: "private", OwnerUserID: pgtype.UUID{Bytes: actorID, Valid: true},
	}
	manifest := contract.ApprovalManifest{
		Version: contract.ApprovalManifestVersionV1, DeploymentId: "deployment-a", SessionId: "session-a",
		TurnId: "turn-a", ApprovalId: "approval-a", ActionId: "action-a",
		Capability: "astronomer.queue.retry_task", Effect: "write",
		ArgumentDigest: strings.Repeat("a", 64), DisclosureDigest: disclosure,
		ModeRevision: 2, PolicyRevision: 2, FencingEpoch: 7, ExpiresAt: now.Add(5 * time.Minute),
		Resources: []contract.ApprovalManifestResource{{Kind: "management_component", Id: "task-a", RequiredVerb: "astronomer.queue.retry_task"}},
	}
	payload, err := contract.ApprovalManifestSigningBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	approval := contract.Approval{ApprovalId: "approval-a", ActionId: "action-a", State: "pending", ExpiresAt: manifest.ExpiresAt, Manifest: manifest}
	store := &approvalAccessStoreFake{connection: connection, session: session, resources: []sqlc.CharlieSessionResource{{SessionID: sessionID, ResourceType: "management_component", ResourceID: "task-a", RequiredVerb: "read"}}}
	bridge := &approvalBridgeFake{approval: approval}
	bindings := approvalBindingsFake{actor: actorID, approve: true, target: true}
	audit := &approvalAuditFake{}
	sessionAccess, err := NewSessionAccessService(store, bindings, bridge, audit, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewApprovalAccessService(store, sessionAccess, bindings, bridge, audit, audit, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	return service, store, bridge, actorID
}

func TestApprovalAccessPersistsExactFindingAndUpdatesItOnRejection(t *testing.T) {
	service, store, bridge, actorID := approvalAccessFixture(t)
	items, err := service.List(context.Background(), actorID)
	if err != nil || len(items) != 1 {
		t.Fatalf("approval list = %#v err=%v", items, err)
	}
	if store.finding.ApprovalID.String != "approval-a" || store.finding.SessionID.Bytes != store.session.ID || store.finding.ExpiresAt.Time != bridge.approval.Manifest.ExpiresAt || store.finding.ExecutionBlockCode != "approval_required" {
		t.Fatalf("approval finding is not exact: %#v", store.finding)
	}
	if len(store.findingResources) != 1 || store.findingResources[0].ResourceType != "management_component" || store.findingResources[0].RequiredVerb != "read" {
		t.Fatalf("approval finding scope = %#v", store.findingResources)
	}
	view, err := service.Decide(context.Background(), actorID, "approval-a", uuid.New(), "reject", "not now")
	if err != nil || view.State != "denied" || store.finding.Status != "resolved" || store.finding.ExecutionBlockCode != "approval_rejected" {
		t.Fatalf("rejection lifecycle view=%#v finding=%#v err=%v", view, store.finding, err)
	}
}

func TestApprovalAccessApprovesExactSignedActionOnce(t *testing.T) {
	service, store, bridge, actorID := approvalAccessFixture(t)
	view, err := service.Decide(context.Background(), actorID, "approval-a", uuid.New(), "approve", "bounded retry")
	if err != nil || view.State != "approved" || store.approval.State != "approved" || bridge.decides != 1 {
		t.Fatalf("decision = %+v, local=%s calls=%d err=%v", view, store.approval.State, bridge.decides, err)
	}
	if store.approval.ResourceType != "management_component" || store.approval.ResourceID != "task-a" {
		t.Fatalf("local approval did not retain the exact signed resource: %+v", store.approval)
	}
	if _, err := service.Decide(context.Background(), actorID, "approval-a", uuid.New(), "approve", "retry"); err != nil || bridge.decides != 1 {
		t.Fatalf("safe replay reached central bridge: calls=%d err=%v", bridge.decides, err)
	}
}

func TestApprovalAccessFailsClosedOnBridgeFailureAndMutation(t *testing.T) {
	service, store, bridge, actorID := approvalAccessFixture(t)
	bridge.fail = true
	if _, err := service.Decide(context.Background(), actorID, "approval-a", uuid.New(), "approve", ""); err == nil || store.approval.State != "rejected" {
		t.Fatalf("bridge failure retained authority: state=%s err=%v", store.approval.State, err)
	}

	service, store, bridge, actorID = approvalAccessFixture(t)
	bridge.approval.Manifest.Capability = "astronomer.queue.failed_tasks"
	if _, err := service.Decide(context.Background(), actorID, "approval-a", uuid.New(), "approve", ""); err == nil || store.created != 0 {
		t.Fatalf("mutated signed action reserved authority: created=%d err=%v", store.created, err)
	}
}

func TestApprovalAccessRequiresUnderlyingPermissionAndExactProductResource(t *testing.T) {
	service, store, _, actorID := approvalAccessFixture(t)
	service.bindings = approvalBindingsFake{actor: actorID, approve: true, target: false}
	if _, err := service.Decide(context.Background(), actorID, "approval-a", uuid.New(), "approve", ""); err == nil || store.created != 0 {
		t.Fatalf("approver without target permission reserved authority: created=%d err=%v", store.created, err)
	}

	_, resourceStore, resourceBridge, _ := approvalAccessFixture(t)
	resourceBridge.approval.Manifest.Resources[0].Id = "other-resource"
	if _, err := validateApprovalResources(resourceBridge.approval.Manifest, resourceStore.resources); err == nil {
		t.Fatal("undisclosed product resource accepted as write scope")
	}
	resourceBridge.approval.Manifest.Resources[0].Id = "task-a"
	resourceBridge.approval.Manifest.Resources = append(resourceBridge.approval.Manifest.Resources, resourceBridge.approval.Manifest.Resources[0])
	if _, err := validateApprovalResources(resourceBridge.approval.Manifest, resourceStore.resources); err == nil {
		t.Fatal("multi-resource approval accepted as one exact write scope")
	}
}

func TestApprovalAccessAllowsExactAgentFleetRecordForProductRemediation(t *testing.T) {
	_, store, bridge, _ := approvalAccessFixture(t)
	bridge.approval.Manifest.Resources[0].Kind = "agent_connection_record"
	bridge.approval.Manifest.Resources[0].Id = "connection-1"
	store.resources[0].ResourceType = "agent_connection_record"
	store.resources[0].ResourceID = "connection-1"
	resource, err := validateApprovalResources(bridge.approval.Manifest, store.resources)
	if err != nil || resource.Kind != "agent_connection_record" || string(resource.Id) != "connection-1" {
		t.Fatalf("Astronomer-owned agent fleet record was not accepted as exact ProductContext: resource=%+v err=%v", resource, err)
	}
}
