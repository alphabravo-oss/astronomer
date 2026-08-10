package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const maxApprovalCandidates = 200

type approvalAccessQueries interface {
	ListCharlieApprovalCandidateSessions(context.Context) ([]sqlc.CharlieSession, error)
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	GetCharlieActionApprovalByApprovalID(context.Context, string) (sqlc.CharlieActionApproval, error)
	CreateCharlieActionApproval(context.Context, sqlc.CreateCharlieActionApprovalParams) (sqlc.CharlieActionApproval, error)
	ApproveCharlieActionApproval(context.Context, uuid.UUID) (sqlc.CharlieActionApproval, error)
	TransitionCharlieActionApproval(context.Context, sqlc.TransitionCharlieActionApprovalParams) (sqlc.CharlieActionApproval, error)
	GetCharlieFindingByApprovalID(context.Context, pgtype.Text) (sqlc.CharlieFinding, error)
	UpsertCharlieApprovalFinding(context.Context, sqlc.UpsertCharlieApprovalFindingParams) (sqlc.CharlieFinding, error)
	TransitionCharlieFindingForApproval(context.Context, sqlc.TransitionCharlieFindingForApprovalParams) (sqlc.CharlieFinding, error)
	AddCharlieFindingResource(context.Context, sqlc.AddCharlieFindingResourceParams) error
	ListCharlieFindingResources(context.Context, uuid.UUID) ([]sqlc.CharlieFindingResource, error)
}

type ApprovalBridge interface {
	ListApprovals(context.Context, string) ([]contract.Approval, error)
	DecideApproval(context.Context, string, string, BridgeApprovalDecision) (contract.Approval, error)
}

// BridgeApprovalDecision is product-authenticated decision metadata sent only
// to the local Product Bridge. The actor reference is derived from the active
// Astronomer user rather than accepted from a browser request.
type BridgeApprovalDecision struct {
	RequestID      uuid.UUID
	Decision       string
	DecidedBy      string
	Rationale      string
	ManifestDigest string
}

type ApprovalLifecycleAudit struct {
	ApprovalID     string
	ActionID       string
	SessionID      uuid.UUID
	ActorID        uuid.UUID
	Capability     string
	Decision       string
	OutcomeCode    string
	ManifestDigest string
}

type ApprovalLifecycleAuditor interface {
	RecordCharlieApprovalLifecycle(context.Context, ApprovalLifecycleAudit) error
}

// ApprovalView contains only bounded, non-authoritative display metadata. The
// signed manifest, signature, argument digest, disclosure digest, and
// authorization reference never cross Astronomer's browser API boundary.
type ApprovalView struct {
	ID                 string              `json:"id"`
	Title              string              `json:"title"`
	State              string              `json:"state"`
	Eligible           bool                `json:"eligible"`
	Capability         string              `json:"capability"`
	Target             string              `json:"target"`
	Risk               string              `json:"risk"`
	Effect             string              `json:"effect"`
	RequiredPermission string              `json:"requiredPermission"`
	ExpiresAt          time.Time           `json:"expiresAt"`
	Reason             string              `json:"reason,omitempty"`
	Review             *ApprovalReviewView `json:"review,omitempty"`
}

// ApprovalReviewView is the complete review content allowed across
// Astronomer's browser API. It intentionally excludes raw arguments,
// authorization references, signed manifests, and all other authority data.
type ApprovalReviewView struct {
	Description       string `json:"description,omitempty"`
	ExpectedImpact    string `json:"expectedImpact,omitempty"`
	Reversible        *bool  `json:"reversible,omitempty"`
	Rollback          string `json:"rollback,omitempty"`
	Destructive       *bool  `json:"destructive,omitempty"`
	ArgumentsWithheld bool   `json:"argumentsWithheld"`
}

type verifiedApproval struct {
	approval       contract.Approval
	manifestDigest string
	connection     sqlc.CharlieConnection
	session        sqlc.CharlieSession
	authorization  string
	descriptor     CapabilityDescriptor
	resource       contract.ApprovalManifestResource
	eligible       bool
	reason         string
	auditReason    string
}

// ApprovalAccessService is the product authority boundary for human approval.
// Charlie proposes and signs an exact action, but Astronomer independently
// verifies it and re-evaluates feature state, mode, session scope, user status,
// charlie:approve, and the underlying target permission on every list and
// decision request.
type ApprovalAccessService struct {
	queries       approvalAccessQueries
	sessions      *SessionAccessService
	bindings      LiveBindingResolver
	bridge        ApprovalBridge
	auditor       ApprovalLifecycleAuditor
	findings      FindingLifecyclePublisher
	engine        *rbac.Engine
	publicKeyFile string
	now           func() time.Time
}

func NewApprovalAccessService(queries approvalAccessQueries, sessions *SessionAccessService, bindings LiveBindingResolver, bridge ApprovalBridge, auditor ApprovalLifecycleAuditor, findings FindingLifecyclePublisher, publicKeyFile string) (*ApprovalAccessService, error) {
	if queries == nil || sessions == nil || bindings == nil || bridge == nil || auditor == nil || findings == nil || strings.TrimSpace(publicKeyFile) == "" {
		return nil, fmt.Errorf("Charlie approval access requires product state, live authorization, bridge, audit, findings, and signing trust")
	}
	return &ApprovalAccessService{queries: queries, sessions: sessions, bindings: bindings, bridge: bridge, auditor: auditor, findings: findings, engine: rbac.NewEngine(), publicKeyFile: publicKeyFile, now: time.Now}, nil
}

func (s *ApprovalAccessService) List(ctx context.Context, actorID uuid.UUID) ([]ApprovalView, error) {
	if actorID == uuid.Nil || s == nil {
		return nil, fmt.Errorf("Charlie approval access is invalid")
	}
	rows, err := s.queries.ListCharlieApprovalCandidateSessions(ctx)
	if err != nil || len(rows) > maxApprovalCandidates {
		return nil, fmt.Errorf("Charlie approval candidates are unavailable")
	}
	views := make([]ApprovalView, 0)
	seen := make(map[string]struct{})
	for _, local := range rows {
		verified, listErr := s.listForSession(ctx, actorID, local.ID)
		if listErr != nil {
			continue
		}
		for _, candidate := range verified {
			id := string(candidate.approval.ApprovalId)
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			views = append(views, approvalView(candidate))
		}
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].ExpiresAt.Equal(views[j].ExpiresAt) {
			return views[i].ID < views[j].ID
		}
		return views[i].ExpiresAt.Before(views[j].ExpiresAt)
	})
	return views, nil
}

func (s *ApprovalAccessService) Decide(ctx context.Context, actorID uuid.UUID, approvalID string, requestID uuid.UUID, decision, rationale string) (ApprovalView, error) {
	trimmedRationale := strings.TrimSpace(rationale)
	if actorID == uuid.Nil || requestID == uuid.Nil || strings.TrimSpace(approvalID) == "" || len(approvalID) > 128 || len(trimmedRationale) > 512 || (decision != "approve" && decision != "reject") {
		return ApprovalView{}, fmt.Errorf("Charlie approval decision is invalid")
	}
	rationaleDigest := digestBytes([]byte(trimmedRationale))
	// A response lost after a successful commit is safe to replay only for the
	// same product approver, exact idempotency request, decision, rationale, and
	// final state. No central content is needed, and a new request can never
	// adopt authority established by an earlier operator decision.
	if existing, err := s.queries.GetCharlieActionApprovalByApprovalID(ctx, approvalID); err == nil {
		exactReplay := existing.ApproverID == actorID && existing.DecisionRequestID == requestID &&
			existing.Decision == decision && existing.RationaleDigest == rationaleDigest
		terminalMatch := (decision == "approve" && (existing.State == "approved" || existing.State == "dispatched")) ||
			(decision == "reject" && existing.State == "rejected")
		if exactReplay && terminalMatch {
			if decision == "reject" {
				if err := s.transitionApprovalFinding(ctx, approvalID, "rejected"); err != nil {
					return ApprovalView{}, err
				}
			}
			if err := s.auditor.RecordCharlieApprovalLifecycle(ctx, ApprovalLifecycleAudit{
				ApprovalID: existing.ApprovalID, ActionID: existing.CharlieActionID, SessionID: existing.SessionID,
				ActorID: actorID, Capability: existing.Capability, Decision: decision,
				OutcomeCode: "replayed", ManifestDigest: existing.ManifestDigest,
			}); err != nil {
				return ApprovalView{}, fmt.Errorf("Charlie approval replay audit could not be persisted")
			}
			return localApprovalView(existing), nil
		}
		return ApprovalView{}, fmt.Errorf("Charlie approval has already been decided")
	}

	candidate, err := s.findForActor(ctx, actorID, approvalID)
	if err != nil {
		// Do not manufacture an approval lifecycle record from untrusted request
		// data when no signed, product-bound candidate can be recovered. The HTTP
		// mutation audit still records the bounded denied request; emitting a
		// lifecycle event with empty action/session/digest fields would violate
		// the audit contract and previously produced a misleading encode failure.
		return ApprovalView{}, fmt.Errorf("Charlie approval is not eligible")
	}
	if !candidate.eligible {
		reason := "authorization_denied"
		if candidate.auditReason != "" {
			reason = candidate.auditReason
		}
		s.audit(ctx, candidate, actorID, decision, reason)
		return ApprovalView{}, fmt.Errorf("Charlie approval is not eligible")
	}
	// Persist the exact, content-free decision intent before reserving local
	// authority or asking Charlie to consume the approval. A missing audit trail
	// must never leave either side believing that authority was granted.
	if err := s.requireAudit(ctx, candidate, actorID, decision, "authorized"); err != nil {
		return ApprovalView{}, fmt.Errorf("Charlie approval audit could not be persisted")
	}
	manifest := candidate.approval.Manifest
	row, err := s.queries.CreateCharlieActionApproval(ctx, sqlc.CreateCharlieActionApprovalParams{
		ConnectionID: candidate.connection.ID, SessionID: candidate.session.ID,
		ApprovalID: approvalID, CharlieActionID: string(manifest.ActionId), TurnID: string(manifest.TurnId),
		Capability: manifest.Capability, ArgumentDigest: manifest.ArgumentDigest,
		DisclosureDigest: manifest.DisclosureDigest, ModeRevision: manifest.ModeRevision,
		PolicyRevision: manifest.PolicyRevision, FencingEpoch: manifest.FencingEpoch,
		ManifestDigest: candidate.manifestDigest, ResourceType: strings.TrimSpace(candidate.resource.Kind), ResourceID: string(candidate.resource.Id), ApproverID: actorID,
		RationaleDigest: rationaleDigest, DecisionRequestID: requestID, Decision: decision, ExpiresAt: manifest.ExpiresAt.UTC(),
	})
	if err != nil {
		return ApprovalView{}, fmt.Errorf("Charlie approval authority could not be reserved")
	}
	reservationID := row.ID

	response, bridgeErr := s.bridge.DecideApproval(ctx, approvalID, candidate.authorization, BridgeApprovalDecision{
		RequestID: requestID, Decision: decision, DecidedBy: "user:" + actorID.String(),
		Rationale: trimmedRationale, ManifestDigest: candidate.manifestDigest,
	})
	if bridgeErr != nil {
		s.rejectLocal(context.WithoutCancel(ctx), reservationID)
		s.audit(context.WithoutCancel(ctx), candidate, actorID, decision, "bridge_failed_closed")
		return ApprovalView{}, fmt.Errorf("Charlie approval decision was not confirmed")
	}
	responseDigest, verifyErr := s.verifyManifest(candidate.connection, candidate.session, response)
	expectedState := "rejected"
	if decision == "approve" {
		expectedState = "approved"
	}
	if verifyErr != nil || responseDigest != candidate.manifestDigest || string(response.State) != expectedState {
		s.rejectLocal(context.WithoutCancel(ctx), reservationID)
		s.audit(context.WithoutCancel(ctx), candidate, actorID, decision, "response_mismatch_closed")
		return ApprovalView{}, fmt.Errorf("Charlie approval confirmation is invalid")
	}
	if decision == "approve" {
		row, err = s.queries.ApproveCharlieActionApproval(ctx, reservationID)
	} else {
		row, err = s.queries.TransitionCharlieActionApproval(ctx, sqlc.TransitionCharlieActionApprovalParams{ID: reservationID, NextState: "rejected"})
	}
	if err != nil {
		s.rejectLocal(context.WithoutCancel(ctx), reservationID)
		s.audit(context.WithoutCancel(ctx), candidate, actorID, decision, "local_commit_failed_closed")
		return ApprovalView{}, fmt.Errorf("Charlie approval did not establish local authority")
	}
	if decision == "reject" {
		if err := s.transitionApprovalFinding(ctx, approvalID, "rejected"); err != nil {
			return ApprovalView{}, err
		}
	}
	s.audit(ctx, candidate, actorID, decision, row.State)
	view := approvalView(candidate)
	view.State = mapApprovalState(row.State)
	view.Eligible = false
	view.Reason = "This exact action has been " + view.State + "."
	return view, nil
}

func (s *ApprovalAccessService) findForActor(ctx context.Context, actorID uuid.UUID, approvalID string) (verifiedApproval, error) {
	rows, err := s.queries.ListCharlieApprovalCandidateSessions(ctx)
	if err != nil || len(rows) > maxApprovalCandidates {
		return verifiedApproval{}, fmt.Errorf("Charlie approval candidates are unavailable")
	}
	for _, local := range rows {
		items, listErr := s.listForSession(ctx, actorID, local.ID)
		if listErr != nil {
			continue
		}
		for _, item := range items {
			if string(item.approval.ApprovalId) == approvalID {
				return item, nil
			}
		}
	}
	return verifiedApproval{}, fmt.Errorf("Charlie approval is unavailable")
}

func (s *ApprovalAccessService) listForSession(ctx context.Context, actorID, sessionID uuid.UUID) ([]verifiedApproval, error) {
	local, resources, authorizationRef, err := s.sessions.authorize(ctx, actorID, sessionID)
	if err != nil {
		return nil, err
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil || connection.ID != local.ConnectionID {
		return nil, fmt.Errorf("Charlie connection is inactive")
	}
	approvals, err := s.bridge.ListApprovals(ctx, authorizationRef)
	if err != nil {
		return nil, err
	}
	result := make([]verifiedApproval, 0, len(approvals))
	for _, approval := range approvals {
		if string(approval.Manifest.SessionId) != local.CharlieSessionID {
			continue
		}
		if state := string(approval.State); state == "rejected" || state == "expired" {
			_ = s.transitionApprovalFinding(ctx, string(approval.ApprovalId), state)
			continue
		}
		item, verifyErr := s.verifyCandidate(ctx, actorID, connection, local, resources, authorizationRef, approval)
		if verifyErr == nil && s.ensureApprovalFinding(ctx, item) == nil {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *ApprovalAccessService) ensureApprovalFinding(ctx context.Context, item verifiedApproval) error {
	manifest := item.approval.Manifest
	approvalID := string(item.approval.ApprovalId)
	if approvalID == "" || len(manifest.Resources) == 0 {
		return fmt.Errorf("Charlie approval finding scope is invalid")
	}
	approvalArg := pgtype.Text{String: approvalID, Valid: true}
	_, existingErr := s.queries.GetCharlieFindingByApprovalID(ctx, approvalArg)
	if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
		return fmt.Errorf("Charlie approval finding is unavailable")
	}
	fingerprint := stableFingerprint(item.connection.InstallationID.String(), "approval_required", manifest.Capability, approvalID)
	row, err := s.queries.UpsertCharlieApprovalFinding(ctx, sqlc.UpsertCharlieApprovalFindingParams{
		ConnectionID: item.connection.ID, CharlieFindingID: "local-approval-" + fingerprint[:32], ApprovalID: approvalArg,
		SessionID: pgtype.UUID{Bytes: item.session.ID, Valid: true}, DedupeFingerprint: fingerprint,
		Title:                  "Approval required for " + item.descriptor.Name,
		Summary:                "Charlie proposed one exact, expiring action that requires product approval.",
		RecommendedActionLabel: "Review exact approval", RiskImpact: "No action runs unless the exact approval and live product authorization both remain valid.",
		VerificationSummary: "After approval, Astronomer rechecks scope, mode, policy, fencing, and preconditions before one dispatch.",
		ExpiresAt:           pgtype.Timestamptz{Time: manifest.ExpiresAt.UTC(), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("Charlie approval finding could not be persisted")
	}
	for _, resource := range manifest.Resources {
		if err := s.queries.AddCharlieFindingResource(ctx, sqlc.AddCharlieFindingResourceParams{
			FindingID: row.ID, ResourceType: strings.TrimSpace(resource.Kind), ResourceID: string(resource.Id), RequiredVerb: "read",
		}); err != nil {
			return fmt.Errorf("Charlie approval finding scope could not be persisted")
		}
	}
	if errors.Is(existingErr, pgx.ErrNoRows) {
		s.publishApprovalFinding(ctx, row)
	}
	return nil
}

func (s *ApprovalAccessService) transitionApprovalFinding(ctx context.Context, approvalID, state string) error {
	row, err := s.queries.TransitionCharlieFindingForApproval(ctx, sqlc.TransitionCharlieFindingForApprovalParams{
		ApprovalState: state, ApprovalID: pgtype.Text{String: approvalID, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := s.queries.GetCharlieFindingByApprovalID(ctx, pgtype.Text{String: approvalID, Valid: true})
		if loadErr == nil && (existing.Status == "open" || existing.Status == "acknowledged") &&
			existing.WorkflowState == string(FindingWorkflowManualRemediationRequired) &&
			((state == "rejected" && existing.ExecutionBlockCode == string(ReasonApprovalRejected)) ||
				(state == "expired" && existing.ExecutionBlockCode == string(ReasonApprovalExpired))) {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("Charlie approval finding lifecycle could not be committed")
	}
	s.publishApprovalFinding(ctx, row)
	return nil
}

func (s *ApprovalAccessService) publishApprovalFinding(ctx context.Context, row sqlc.CharlieFinding) {
	resources, err := s.queries.ListCharlieFindingResources(ctx, row.ID)
	if err != nil || len(resources) == 0 {
		return
	}
	s.findings.PublishCharlieFindingLifecycle(ctx, FindingAlert{
		FindingID: row.ID.String(), Severity: NormalizeFindingSeverity(row.Severity), Status: row.Status,
		ResourceType: resources[0].ResourceType, ResourceID: resources[0].ResourceID,
		BlockCode: row.ExecutionBlockCode, RepeatCount: int(row.RepeatCount),
	})
}

func (s *ApprovalAccessService) verifyCandidate(ctx context.Context, actorID uuid.UUID, connection sqlc.CharlieConnection, session sqlc.CharlieSession, resources []sqlc.CharlieSessionResource, authorizationRef string, approval contract.Approval) (verifiedApproval, error) {
	if string(approval.State) != "pending" {
		return verifiedApproval{}, fmt.Errorf("Charlie approval is not pending")
	}
	digest, err := s.verifyManifest(connection, session, approval)
	if err != nil {
		return verifiedApproval{}, err
	}
	manifest := approval.Manifest
	descriptor, found := capabilityByName(manifest.Capability)
	if !found || descriptor.Effect != EffectWrite || descriptor.ManagedTargetAccess || descriptor.Risk == "destructive" || !descriptor.RequiresPrecondition || !descriptor.RequiresVerification {
		return verifiedApproval{}, fmt.Errorf("Charlie approval capability is not permitted")
	}
	if _, err := approvalReviewView(approval.Review, manifest, descriptor); err != nil {
		return verifiedApproval{}, err
	}
	resource, err := validateApprovalResources(manifest, resources)
	if err != nil {
		return verifiedApproval{}, err
	}
	item := verifiedApproval{approval: approval, manifestDigest: digest, connection: connection, session: session, authorization: authorizationRef, descriptor: descriptor, resource: resource}
	bindings, active, err := s.bindings.CurrentBindings(ctx, actorID)
	if err != nil || !active {
		item.reason = "Your Astronomer account is inactive."
		item.auditReason = "actor_inactive"
		return item, nil
	}
	approves := s.engine.CheckPermission(bindings, rbac.ResourceCharlie, rbac.VerbApprove, uuid.Nil, uuid.Nil)
	target := s.engine.CheckPermission(bindings, rbac.Resource(descriptor.RBACResource), rbac.Verb(descriptor.RBACVerb), uuid.Nil, uuid.Nil)
	item.eligible = approves && target
	if !approves {
		item.reason = "Requires charlie:approve."
		item.auditReason = "approval_permission_denied"
	} else if !target {
		item.reason = "Requires the underlying " + descriptor.RBACResource + ":" + descriptor.RBACVerb + " permission."
		item.auditReason = "target_permission_denied"
	}
	return item, nil
}

func (s *ApprovalAccessService) verifyManifest(connection sqlc.CharlieConnection, session sqlc.CharlieSession, approval contract.Approval) (string, error) {
	mode := EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled)
	if !connection.Active || connection.EmergencyDisabled || (mode != ModeApproval && mode != ModeAuto) {
		return "", fmt.Errorf("Charlie approval mode or state is inactive")
	}
	publicKey, err := os.ReadFile(s.publicKeyFile)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return "", fmt.Errorf("Charlie approval signing trust is unavailable")
	}
	keyDigest := sha256.Sum256(publicKey)
	if hex.EncodeToString(keyDigest[:]) != strings.ToLower(connection.SigningKeyFingerprint) {
		return "", fmt.Errorf("Charlie approval signing trust does not match onboarding")
	}
	digest, err := contract.VerifyApprovalManifest(approval, ed25519.PublicKey(publicKey), connection.DeploymentID, s.now().UTC())
	if err != nil {
		return "", err
	}
	manifest := approval.Manifest
	if string(manifest.SessionId) != session.CharlieSessionID || normalizeDigest(manifest.DisclosureDigest) != normalizeDigest(connection.DisclosureDigest) ||
		manifest.ModeRevision != connection.VerifiedModeRevision || manifest.PolicyRevision != manifest.ModeRevision ||
		manifest.FencingEpoch != connection.FencingEpoch {
		return "", fmt.Errorf("Charlie approval authority is stale")
	}
	return digest, nil
}

func validateApprovalResources(manifest contract.ApprovalManifest, local []sqlc.CharlieSessionResource) (contract.ApprovalManifestResource, error) {
	if len(manifest.Resources) != 1 {
		return contract.ApprovalManifestResource{}, fmt.Errorf("Charlie approval must bind one exact product resource")
	}
	disclosed := make(map[string]struct{}, len(local))
	for _, resource := range local {
		if resource.RequiredVerb == "read" {
			disclosed[resource.ResourceType+"\x00"+resource.ResourceID] = struct{}{}
		}
	}
	resource := manifest.Resources[0]
	kind, id := strings.TrimSpace(resource.Kind), string(resource.Id)
	if resource.RequiredVerb != manifest.Capability {
		return contract.ApprovalManifestResource{}, fmt.Errorf("Charlie approval resource crosses the v1 management-plane boundary")
	}
	if _, ok := disclosed[kind+"\x00"+id]; !ok {
		return contract.ApprovalManifestResource{}, fmt.Errorf("Charlie approval resource was not disclosed by the product")
	}
	return resource, nil
}

func approvalView(item verifiedApproval) ApprovalView {
	targets := make([]string, 0, len(item.approval.Manifest.Resources))
	for _, resource := range item.approval.Manifest.Resources {
		targets = append(targets, resource.Kind+":"+string(resource.Id))
	}
	review, _ := approvalReviewView(item.approval.Review, item.approval.Manifest, item.descriptor)
	return ApprovalView{
		ID: string(item.approval.ApprovalId), Title: "Approve " + item.descriptor.Name,
		State: mapApprovalState(string(item.approval.State)), Eligible: item.eligible,
		Capability: item.descriptor.Name, Target: strings.Join(targets, ", "), Risk: item.descriptor.Risk,
		Effect: string(item.descriptor.Effect), RequiredPermission: item.descriptor.RBACResource + ":" + item.descriptor.RBACVerb,
		ExpiresAt: item.approval.ExpiresAt.UTC(), Reason: item.reason, Review: review,
	}
}

func approvalReviewView(review *contract.ApprovalReviewSummary, manifest contract.ApprovalManifest, descriptor CapabilityDescriptor) (*ApprovalReviewView, error) {
	if review == nil {
		return nil, nil
	}
	if !review.ArgumentsWithheld || review.Capability != manifest.Capability || string(review.Effect) != string(EffectWrite) || string(review.Risk) != descriptor.Risk {
		return nil, fmt.Errorf("Charlie approval review summary does not match product authority")
	}
	description, err := projectedApprovalReviewText(review.Description)
	if err != nil {
		return nil, err
	}
	impact, err := projectedApprovalReviewText(review.ExpectedImpact)
	if err != nil {
		return nil, err
	}
	rollback, err := projectedApprovalReviewText(review.Rollback)
	if err != nil {
		return nil, err
	}
	return &ApprovalReviewView{
		Description: description, ExpectedImpact: impact, Reversible: review.Reversible,
		Rollback: rollback, Destructive: review.Destructive, ArgumentsWithheld: true,
	}, nil
}

func projectedApprovalReviewText(value *string) (string, error) {
	if value == nil {
		return "", nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" || len(trimmed) > 4096 {
		return "", fmt.Errorf("Charlie approval review summary is invalid")
	}
	return trimmed, nil
}

func localApprovalView(row sqlc.CharlieActionApproval) ApprovalView {
	state := mapApprovalState(row.State)
	// Dispatch consumes the exact approval but does not change the browser
	// decision result. Keep the bounded public enum and avoid implying that a
	// replay initiated another execution.
	if state == "dispatched" {
		state = "approved"
	}
	return ApprovalView{ID: row.ApprovalID, Title: "Charlie action decision", State: state, Eligible: false, Capability: row.Capability, Target: "exact signed target", Risk: "bounded", Effect: "write", RequiredPermission: "Exact target permission", ExpiresAt: row.ExpiresAt.UTC(), Reason: "This exact action has already been " + state + "."}
}

func mapApprovalState(state string) string {
	if state == "rejected" {
		return "denied"
	}
	return state
}

func (s *ApprovalAccessService) rejectLocal(ctx context.Context, id uuid.UUID) {
	if id != uuid.Nil {
		_, _ = s.queries.TransitionCharlieActionApproval(ctx, sqlc.TransitionCharlieActionApprovalParams{ID: id, NextState: "rejected"})
	}
}

func (s *ApprovalAccessService) audit(ctx context.Context, item verifiedApproval, actorID uuid.UUID, decision, outcome string) {
	_ = s.requireAudit(ctx, item, actorID, decision, outcome)
}

func (s *ApprovalAccessService) requireAudit(ctx context.Context, item verifiedApproval, actorID uuid.UUID, decision, outcome string) error {
	if s == nil || s.auditor == nil {
		return fmt.Errorf("Charlie approval audit is unavailable")
	}
	return s.auditor.RecordCharlieApprovalLifecycle(ctx, ApprovalLifecycleAudit{
		ApprovalID: string(item.approval.ApprovalId), ActionID: string(item.approval.ActionId), SessionID: item.session.ID,
		ActorID: actorID, Capability: item.descriptor.Name, Decision: decision, OutcomeCode: outcome, ManifestDigest: item.manifestDigest,
	})
}
