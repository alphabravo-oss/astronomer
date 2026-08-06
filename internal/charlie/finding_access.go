package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const MaxCharlieFindingItems = 100

type findingAccessQueries interface {
	delegationQuerier
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	GetCharlieFinding(context.Context, uuid.UUID) (sqlc.CharlieFinding, error)
	GetCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error)
	ListCharlieFindingResources(context.Context, uuid.UUID) ([]sqlc.CharlieFindingResource, error)
	ListCharlieFindings(context.Context, sqlc.ListCharlieFindingsParams) ([]sqlc.CharlieFinding, error)
	TransitionCharlieFinding(context.Context, sqlc.TransitionCharlieFindingParams) (sqlc.CharlieFinding, error)
}

// FindingContentBridge is the optional on-demand detail boundary. Astronomer
// keeps only a bounded redacted summary; full evidence remains in Charlie and
// is fetched only after current product authorization succeeds.
type FindingContentBridge interface {
	GetFinding(context.Context, string, string) (json.RawMessage, error)
	TransitionFinding(context.Context, string, string, uuid.UUID, string) (json.RawMessage, error)
}

type FindingLifecycleAudit struct {
	Action      string
	FindingID   uuid.UUID
	ActorID     uuid.UUID
	OutcomeCode string
	Resources   int
}

type FindingLifecycleAuditor interface {
	RecordCharlieFindingLifecycle(context.Context, FindingLifecycleAudit)
}

type FindingLifecyclePublisher interface {
	PublishCharlieFindingLifecycle(context.Context, FindingAlert)
}

type FindingAccessService struct {
	queries    findingAccessQueries
	authorizer SessionAccessAuthorizer
	bridge     FindingContentBridge
	auditor    FindingLifecycleAuditor
	publisher  FindingLifecyclePublisher
	syncer     FindingSummarySyncer
	active     func() bool
	now        func() time.Time
}

func NewFindingAccessService(queries findingAccessQueries, authorizer SessionAccessAuthorizer, bridge FindingContentBridge, auditor FindingLifecycleAuditor, publisher FindingLifecyclePublisher, syncer FindingSummarySyncer, active func() bool) (*FindingAccessService, error) {
	if queries == nil || authorizer == nil || bridge == nil || auditor == nil || publisher == nil || syncer == nil || active == nil {
		return nil, fmt.Errorf("Charlie finding access requires local state, live authorization, bridge, sync, audit, publication, and activation")
	}
	return &FindingAccessService{queries: queries, authorizer: authorizer, bridge: bridge, auditor: auditor, publisher: publisher, syncer: syncer, active: active, now: time.Now}, nil
}

type FindingView struct {
	Finding   sqlc.CharlieFinding           `json:"finding"`
	Resources []sqlc.CharlieFindingResource `json:"resources"`
	Remote    json.RawMessage               `json:"remote,omitempty"`
}

func (s *FindingAccessService) List(ctx context.Context, actorID uuid.UUID, status string, offset, limit int32) ([]FindingView, error) {
	connection, err := s.authorizeConnection(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if offset < 0 || limit < 1 || limit > MaxCharlieFindingItems || !validFindingStatusFilter(status) {
		return nil, fmt.Errorf("Charlie finding list request is invalid")
	}
	// Central sync is best-effort. An outage must not hide already-durable,
	// locally scoped findings from an authorized user.
	_ = s.syncer.SyncForActor(ctx, actorID)
	statusArg := pgtype.Text{}
	if status != "" {
		statusArg = pgtype.Text{String: status, Valid: true}
	}
	rows, err := s.queries.ListCharlieFindings(ctx, sqlc.ListCharlieFindingsParams{ConnectionID: connection.ID, Status: statusArg, PageOffset: offset, PageLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("Charlie findings are unavailable")
	}
	result := make([]FindingView, 0, len(rows))
	for _, row := range rows {
		resources, resourceErr := s.queries.ListCharlieFindingResources(ctx, row.ID)
		if resourceErr != nil || len(resources) == 0 {
			continue // fail closed per finding without failing the entire list
		}
		allowed, authErr := s.authorizer.CanReadIncidentResources(ctx, actorID, findingResourcesAsSession(resources))
		sessionAllowed := s.findingSessionAuthorized(ctx, actorID, connection.ID, row)
		if authErr == nil && allowed && sessionAllowed {
			result = append(result, FindingView{Finding: row, Resources: resources})
		}
	}
	s.audit(ctx, "charlie.finding.list", uuid.Nil, actorID, "allowed", len(result))
	return result, nil
}

func (s *FindingAccessService) Get(ctx context.Context, actorID, findingID uuid.UUID) (FindingView, error) {
	connection, row, resources, err := s.authorizeFinding(ctx, actorID, findingID)
	if err != nil {
		return FindingView{}, err
	}
	view := FindingView{Finding: row, Resources: resources}
	if shouldFetchCentralFinding(row) {
		authorizationRef, authErr := s.issueFindingDelegation(ctx, row, actorID, connection.ID)
		if authErr != nil {
			return FindingView{}, authErr
		}
		remote, bridgeErr := s.bridge.GetFinding(ctx, row.CharlieFindingID, authorizationRef)
		if bridgeErr != nil {
			return FindingView{}, fmt.Errorf("Charlie finding detail is unavailable")
		}
		view.Remote = remote
	}
	s.audit(ctx, "charlie.finding.read", row.ID, actorID, "allowed", len(resources))
	return view, nil
}

func (s *FindingAccessService) Transition(ctx context.Context, actorID, findingID, requestID uuid.UUID, decision string) (FindingView, error) {
	if requestID == uuid.Nil || !validFindingDecision(decision) {
		return FindingView{}, fmt.Errorf("Charlie finding transition is invalid")
	}
	connection, row, resources, err := s.authorizeFinding(ctx, actorID, findingID)
	if err != nil {
		return FindingView{}, err
	}
	nextStatus, nextWorkflow, allowed := findingDecisionTarget(row, decision, s.now().UTC())
	if !allowed {
		return FindingView{}, fmt.Errorf("Charlie finding transition is invalid")
	}
	var remote json.RawMessage
	if shouldFetchCentralFinding(row) {
		authorizationRef, authErr := s.issueFindingDelegation(ctx, row, actorID, connection.ID)
		if authErr != nil {
			return FindingView{}, authErr
		}
		remote, err = s.bridge.TransitionFinding(ctx, row.CharlieFindingID, authorizationRef, requestID, decision)
		if err != nil {
			return FindingView{}, fmt.Errorf("Charlie finding transition is unavailable")
		}
	}
	updated, err := s.queries.TransitionCharlieFinding(ctx, sqlc.TransitionCharlieFindingParams{
		NextStatus: nextStatus, NextWorkflowState: nextWorkflow,
		ActorID: pgtype.UUID{Bytes: actorID, Valid: true}, ID: row.ID,
		ExpectedStatus: row.Status, ExpectedWorkflowState: row.WorkflowState,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return FindingView{}, fmt.Errorf("Charlie finding changed; refresh and try again")
	}
	if err != nil {
		return FindingView{}, fmt.Errorf("Charlie finding transition could not be committed")
	}
	alert := FindingAlert{FindingID: updated.ID.String(), Severity: NormalizeFindingSeverity(updated.Severity), Status: updated.Status, ResourceType: resources[0].ResourceType, ResourceID: resources[0].ResourceID, BlockCode: updated.ExecutionBlockCode, RepeatCount: int(updated.RepeatCount)}
	s.publisher.PublishCharlieFindingLifecycle(ctx, alert)
	s.audit(ctx, "charlie.finding."+decision, updated.ID, actorID, "completed", len(resources))
	return FindingView{Finding: updated, Resources: resources, Remote: remote}, nil
}

func (s *FindingAccessService) authorizeConnection(ctx context.Context, actorID uuid.UUID) (sqlc.CharlieConnection, error) {
	if s == nil || s.active == nil || !s.active() || actorID == uuid.Nil {
		return sqlc.CharlieConnection{}, fmt.Errorf("Charlie runtime is inactive")
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active || connection.EmergencyDisabled || EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled) == ModeDisabled {
		return sqlc.CharlieConnection{}, fmt.Errorf("Charlie connection is inactive")
	}
	allowed, err := s.authorizer.CanUseCharlie(ctx, actorID)
	if err != nil || !allowed {
		s.audit(ctx, "charlie.finding.authorization", uuid.Nil, actorID, "authorization_denied", 0)
		return sqlc.CharlieConnection{}, fmt.Errorf("Charlie finding access is denied")
	}
	return connection, nil
}

func (s *FindingAccessService) authorizeFinding(ctx context.Context, actorID, findingID uuid.UUID) (sqlc.CharlieConnection, sqlc.CharlieFinding, []sqlc.CharlieFindingResource, error) {
	connection, err := s.authorizeConnection(ctx, actorID)
	if err != nil || findingID == uuid.Nil {
		return sqlc.CharlieConnection{}, sqlc.CharlieFinding{}, nil, fmt.Errorf("Charlie finding access is denied")
	}
	row, err := s.queries.GetCharlieFinding(ctx, findingID)
	if err != nil || row.ConnectionID != connection.ID {
		return sqlc.CharlieConnection{}, sqlc.CharlieFinding{}, nil, fmt.Errorf("Charlie finding access is denied")
	}
	resources, err := s.queries.ListCharlieFindingResources(ctx, row.ID)
	if err != nil || len(resources) == 0 {
		return sqlc.CharlieConnection{}, sqlc.CharlieFinding{}, nil, fmt.Errorf("Charlie finding access is denied")
	}
	allowed, err := s.authorizer.CanReadIncidentResources(ctx, actorID, findingResourcesAsSession(resources))
	if err != nil || !allowed {
		s.audit(ctx, "charlie.finding.authorization", row.ID, actorID, "resource_denied", len(resources))
		return sqlc.CharlieConnection{}, sqlc.CharlieFinding{}, nil, fmt.Errorf("Charlie finding access is denied")
	}
	if !s.findingSessionAuthorized(ctx, actorID, connection.ID, row) {
		s.audit(ctx, "charlie.finding.authorization", row.ID, actorID, "session_denied", len(resources))
		return sqlc.CharlieConnection{}, sqlc.CharlieFinding{}, nil, fmt.Errorf("Charlie finding access is denied")
	}
	return connection, row, resources, nil
}

func (s *FindingAccessService) findingSessionAuthorized(ctx context.Context, actorID, connectionID uuid.UUID, row sqlc.CharlieFinding) bool {
	if !row.SessionID.Valid {
		// Product-local system findings may intentionally have no session.
		// A central finding without its required local linkage fails closed.
		return strings.HasPrefix(row.CharlieFindingID, "local-")
	}
	session, err := s.queries.GetCharlieSession(ctx, row.SessionID.Bytes)
	if err != nil || session.ConnectionID != connectionID || session.State == "aborted" || session.State == "failed" {
		return false
	}
	switch session.Visibility {
	case "private":
		return session.Source == "user" && session.OwnerUserID.Valid && session.OwnerUserID.Bytes == actorID
	case "incident":
		return session.Source == "event"
	default:
		return false
	}
}

func (s *FindingAccessService) issueFindingDelegation(ctx context.Context, row sqlc.CharlieFinding, actorID, connectionID uuid.UUID) (string, error) {
	if !row.SessionID.Valid {
		return "", fmt.Errorf("Charlie finding detail is unavailable")
	}
	session, err := s.queries.GetCharlieSession(ctx, row.SessionID.Bytes)
	if err != nil || session.ConnectionID != connectionID || session.State == "aborted" || session.State == "failed" {
		return "", fmt.Errorf("Charlie finding detail is unavailable")
	}
	delegation, err := IssueDelegation(ctx, s.queries, session.ID, actorID, "user", maxDelegationTTL, s.now().UTC())
	if err != nil {
		return "", fmt.Errorf("Charlie finding authorization is unavailable")
	}
	return delegation.Reference, nil
}

func findingResourcesAsSession(resources []sqlc.CharlieFindingResource) []sqlc.CharlieSessionResource {
	result := make([]sqlc.CharlieSessionResource, 0, len(resources))
	for _, resource := range resources {
		result = append(result, sqlc.CharlieSessionResource{SessionID: resource.FindingID, ResourceType: resource.ResourceType, ResourceID: resource.ResourceID, RequiredVerb: resource.RequiredVerb})
	}
	return result
}

func shouldFetchCentralFinding(row sqlc.CharlieFinding) bool {
	return row.SessionID.Valid && strings.TrimSpace(row.CharlieFindingID) != "" && !strings.HasPrefix(row.CharlieFindingID, "local-")
}

func validFindingStatusFilter(value string) bool {
	return value == "" || value == "open" || value == "acknowledged" || value == "dismissed" || value == "resolved" || value == "expired"
}

func validFindingDecision(value string) bool {
	return value == "acknowledge" || value == "start_remediation" || value == "request_verification" ||
		value == "dismiss" || value == "resolve"
}

func findingDecisionTarget(row sqlc.CharlieFinding, decision string, now time.Time) (string, string, bool) {
	if !FindingWorkflowAllows(row, decision, now) {
		return "", "", false
	}
	switch decision {
	case "acknowledge":
		return "acknowledged", row.WorkflowState, true
	case "start_remediation":
		return "acknowledged", string(FindingWorkflowRemediationInProgress), true
	case "request_verification":
		return "acknowledged", string(FindingWorkflowVerificationPending), true
	case "dismiss":
		return "dismissed", string(FindingWorkflowDismissed), true
	case "resolve":
		return "resolved", string(FindingWorkflowResolved), true
	default:
		return "", "", false
	}
}

func NormalizeFindingSeverity(value string) string {
	if value == "warning" {
		return "medium"
	}
	return value
}

func (s *FindingAccessService) audit(ctx context.Context, action string, findingID, actorID uuid.UUID, outcome string, resources int) {
	if s != nil && s.auditor != nil {
		s.auditor.RecordCharlieFindingLifecycle(ctx, FindingLifecycleAudit{Action: action, FindingID: findingID, ActorID: actorID, OutcomeCode: outcome, Resources: resources})
	}
}
