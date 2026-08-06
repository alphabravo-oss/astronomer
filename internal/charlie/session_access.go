package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	MaxCharlieMessageBytes = 32 << 10
	MaxCharlieHistoryItems = 100
)

type sessionAccessQueries interface {
	delegationQuerier
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	GetCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error)
	ListCharlieSessionResources(context.Context, uuid.UUID) ([]sqlc.CharlieSessionResource, error)
	AbortCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error)
	RevokeCharlieDelegationsForSession(context.Context, uuid.UUID) (int64, error)
	UpdateCharlieSessionCursor(context.Context, sqlc.UpdateCharlieSessionCursorParams) (sqlc.CharlieSession, error)
}

type accessibleSessionQueries interface {
	ListCharlieAccessibleSessionCandidates(context.Context, sqlc.ListCharlieAccessibleSessionCandidatesParams) ([]sqlc.CharlieSession, error)
	ListCharlieSessionResourcesBatch(context.Context, []uuid.UUID) ([]sqlc.CharlieSessionResource, error)
}

// SessionAccessAuthorizer is deliberately product-owned and live. It must
// resolve the user's current account and RBAC bindings on every call; no
// permission snapshot from Charlie or from session creation is accepted.
type SessionAccessAuthorizer interface {
	CanUseCharlie(context.Context, uuid.UUID) (bool, error)
	CanReadIncidentResources(context.Context, uuid.UUID, []sqlc.CharlieSessionResource) (bool, error)
}

// SessionContentBridge is a pure proxy boundary. Conversation and evidence
// bytes transit Astronomer memory but are never given to a persistence or audit
// interface in this package.
type SessionContentBridge interface {
	GetSession(context.Context, string, string) (json.RawMessage, error)
	GetHistory(context.Context, string, string, string, int) (json.RawMessage, error)
	CreateMessage(context.Context, string, string, uuid.UUID, string) (json.RawMessage, error)
	AbortSession(context.Context, string, string, uuid.UUID) error
	StreamSessionEvents(context.Context, string, string, string, func(contract.Event) error) error
}

type SessionLifecycleAudit struct {
	Action        string
	SessionID     uuid.UUID
	ActorID       uuid.UUID
	Visibility    string
	OutcomeCode   string
	ResourceCount int
}

type SessionLifecycleAuditor interface {
	RecordCharlieSessionLifecycle(context.Context, SessionLifecycleAudit)
}

type SessionAccessService struct {
	queries    sessionAccessQueries
	authorizer SessionAccessAuthorizer
	bridge     SessionContentBridge
	auditor    SessionLifecycleAuditor
	authority  AuthorityMutationAuditor
	active     func() bool
	now        func() time.Time
}

// CurrentMode returns only the effective product authority mode after the same
// live user and connection checks used by the session list. It intentionally
// exposes no central policy, disclosure, or credential material.
func (s *SessionAccessService) CurrentMode(ctx context.Context, actorID uuid.UUID) (Mode, error) {
	if err := s.guardActive(); err != nil || actorID == uuid.Nil {
		return ModeDisabled, fmt.Errorf("Charlie mode is unavailable")
	}
	allowed, err := s.authorizer.CanUseCharlie(ctx, actorID)
	if err != nil || !allowed {
		return ModeDisabled, fmt.Errorf("Charlie mode access is denied")
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active {
		return ModeDisabled, fmt.Errorf("Charlie connection is inactive")
	}
	return EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled), nil
}

func NewSessionAccessService(queries sessionAccessQueries, authorizer SessionAccessAuthorizer, bridge SessionContentBridge, auditor SessionLifecycleAuditor, active func() bool) (*SessionAccessService, error) {
	authority, authorityOK := auditor.(AuthorityMutationAuditor)
	if queries == nil || authorizer == nil || bridge == nil || auditor == nil || !authorityOK || authority == nil || active == nil {
		return nil, fmt.Errorf("Charlie session access requires local state, live authorization, bridge, audit, and activation")
	}
	return &SessionAccessService{queries: queries, authorizer: authorizer, bridge: bridge, auditor: auditor, authority: authority, active: active, now: time.Now}, nil
}

type SessionView struct {
	Session sqlc.CharlieSession `json:"session"`
	Remote  json.RawMessage     `json:"remote"`
}

func (s *SessionAccessService) ListAccessible(ctx context.Context, actorID uuid.UUID, offset, limit int32) ([]sqlc.CharlieSession, error) {
	if err := s.guardActive(); err != nil {
		return nil, err
	}
	if actorID == uuid.Nil || offset < 0 || limit < 1 || limit > MaxCharlieHistoryItems {
		return nil, fmt.Errorf("Charlie session list request is invalid")
	}
	allowed, err := s.authorizer.CanUseCharlie(ctx, actorID)
	if err != nil || !allowed {
		s.audit(ctx, "charlie.session.list", uuid.Nil, actorID, "private", "authorization_denied", 0)
		return nil, fmt.Errorf("Charlie session access is denied")
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active {
		return nil, fmt.Errorf("Charlie session list is unavailable")
	}
	listing, ok := s.queries.(accessibleSessionQueries)
	if !ok {
		return nil, fmt.Errorf("Charlie session list is unavailable")
	}
	const candidatePageSize int32 = 100
	result := make([]sqlc.CharlieSession, 0, limit)
	var candidateOffset, accessibleOffset int32
	for len(result) < int(limit) {
		rows, queryErr := listing.ListCharlieAccessibleSessionCandidates(ctx, sqlc.ListCharlieAccessibleSessionCandidatesParams{
			ConnectionID: connection.ID,
			OwnerUserID:  pgtype.UUID{Bytes: actorID, Valid: true},
			PageOffset:   candidateOffset,
			PageLimit:    candidatePageSize,
		})
		if queryErr != nil {
			return nil, fmt.Errorf("Charlie session list is unavailable")
		}
		if len(rows) == 0 {
			break
		}
		incidentIDs := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			if row.Visibility == "incident" {
				incidentIDs = append(incidentIDs, row.ID)
			}
		}
		resourcesBySession := make(map[uuid.UUID][]sqlc.CharlieSessionResource, len(incidentIDs))
		if len(incidentIDs) > 0 {
			resources, resourceErr := listing.ListCharlieSessionResourcesBatch(ctx, incidentIDs)
			if resourceErr != nil {
				return nil, fmt.Errorf("Charlie session scopes are unavailable")
			}
			for _, resource := range resources {
				resourcesBySession[resource.SessionID] = append(resourcesBySession[resource.SessionID], resource)
			}
		}
		for _, row := range rows {
			visible := row.Visibility == "private" && row.Source == "user" && row.OwnerUserID.Valid && row.OwnerUserID.Bytes == actorID
			if row.Visibility == "incident" {
				resources := resourcesBySession[row.ID]
				resourceAllowed, resourceErr := s.authorizer.CanReadIncidentResources(ctx, actorID, resources)
				visible = len(resources) > 0 && resourceErr == nil && resourceAllowed
				if !visible {
					s.audit(ctx, "charlie.session.list_visibility", row.ID, actorID, row.Visibility, "resource_denied", len(resources))
				}
			}
			if !visible {
				continue
			}
			if accessibleOffset < offset {
				accessibleOffset++
				continue
			}
			result = append(result, row)
			if len(result) == int(limit) {
				break
			}
		}
		candidateOffset += int32(len(rows))
		if len(rows) < int(candidatePageSize) {
			break
		}
	}
	return result, nil
}

func (s *SessionAccessService) Get(ctx context.Context, actorID, sessionID uuid.UUID) (SessionView, error) {
	local, _, authorizationRef, err := s.authorize(ctx, actorID, sessionID)
	if err != nil {
		return SessionView{}, err
	}
	remote, err := s.bridge.GetSession(ctx, local.CharlieSessionID, authorizationRef)
	if err != nil {
		return SessionView{}, fmt.Errorf("Charlie session is unavailable")
	}
	s.audit(ctx, "charlie.session.read", local.ID, actorID, local.Visibility, "allowed", 0)
	return SessionView{Session: local, Remote: remote}, nil
}

func (s *SessionAccessService) History(ctx context.Context, actorID, sessionID uuid.UUID, cursor string, limit int) (json.RawMessage, error) {
	if len(cursor) > 128 || limit < 1 || limit > MaxCharlieHistoryItems {
		return nil, fmt.Errorf("Charlie history request is invalid")
	}
	local, resources, authorizationRef, err := s.authorize(ctx, actorID, sessionID)
	if err != nil {
		return nil, err
	}
	history, err := s.bridge.GetHistory(ctx, local.CharlieSessionID, authorizationRef, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("Charlie history is unavailable")
	}
	s.audit(ctx, "charlie.session.history", local.ID, actorID, local.Visibility, "allowed", len(resources))
	return history, nil
}

func (s *SessionAccessService) Message(ctx context.Context, actorID, sessionID, clientMessageID uuid.UUID, message string) (json.RawMessage, error) {
	if clientMessageID == uuid.Nil || strings.TrimSpace(message) == "" || len([]byte(message)) > MaxCharlieMessageBytes {
		return nil, fmt.Errorf("Charlie message request is invalid")
	}
	local, resources, authorizationRef, err := s.authorize(ctx, actorID, sessionID)
	if err != nil {
		return nil, err
	}
	if local.State != "active" && local.State != "waiting_approval" {
		return nil, fmt.Errorf("Charlie session does not accept messages")
	}
	receipt, err := s.bridge.CreateMessage(ctx, local.CharlieSessionID, authorizationRef, clientMessageID, message)
	if err != nil {
		return nil, fmt.Errorf("Charlie message is unavailable")
	}
	s.audit(ctx, "charlie.session.message_accepted", local.ID, actorID, local.Visibility, "accepted", len(resources))
	return receipt, nil
}

// Abort closes local authority before making the idempotent bridge request.
// If the bridge is unavailable, the session remains locally aborted and every
// subsequent MCP/content request is denied; reconciliation may retry remotely.
func (s *SessionAccessService) Abort(ctx context.Context, actorID, sessionID, requestID uuid.UUID) error {
	if requestID == uuid.Nil {
		return fmt.Errorf("Charlie abort request is invalid")
	}
	local, resources, authorizationRef, err := s.authorize(ctx, actorID, sessionID)
	if err != nil {
		return err
	}
	if _, err := s.queries.AbortCharlieSession(ctx, local.ID); err != nil {
		return fmt.Errorf("Charlie session could not be aborted safely")
	}
	if _, err := s.queries.RevokeCharlieDelegationsForSession(ctx, local.ID); err != nil {
		return fmt.Errorf("Charlie session authority could not be revoked safely")
	}
	if err := s.bridge.AbortSession(ctx, local.CharlieSessionID, authorizationRef, requestID); err != nil {
		s.audit(ctx, "charlie.session.aborted", local.ID, actorID, local.Visibility, "remote_pending", len(resources))
		return fmt.Errorf("Charlie session is locally aborted; remote reconciliation is pending")
	}
	s.audit(ctx, "charlie.session.aborted", local.ID, actorID, local.Visibility, "completed", len(resources))
	return nil
}

// Stream delivers opaque Charlie events only while the initiating actor keeps
// live product access. The browser callback must finish writing and flushing an
// event before it returns nil; only then is the central event ID persisted.
// This makes Last-Event-ID replay safe across browser, server, agent, and
// Charlie restarts without storing conversation content in Astronomer.
func (s *SessionAccessService) Stream(ctx context.Context, actorID, sessionID uuid.UUID, lastEventID string, handle func(contract.Event) error) error {
	if handle == nil || len(lastEventID) > 128 {
		return fmt.Errorf("Charlie event stream request is invalid")
	}
	local, resources, authorizationRef, err := s.authorize(ctx, actorID, sessionID)
	if err != nil {
		return err
	}
	if lastEventID == "" {
		lastEventID = local.LastEventID
	}
	observeStreamOpened(lastEventID != "")
	failed := false
	defer func() { observeStreamClosed(failed) }()
	s.audit(ctx, "charlie.session.stream_opened", local.ID, actorID, local.Visibility, "allowed", len(resources))
	err = s.bridge.StreamSessionEvents(ctx, local.CharlieSessionID, authorizationRef, lastEventID, func(event contract.Event) error {
		current, currentResources, accessErr := s.authorizeLocal(ctx, actorID, sessionID)
		if accessErr != nil {
			return accessErr
		}
		if event.ID != "" && (len(event.ID) > 128 || strings.ContainsAny(event.ID, "\r\n")) {
			return fmt.Errorf("Charlie event ID is invalid")
		}
		if handleErr := handle(event); handleErr != nil {
			observeStreamAcknowledgement(false)
			return handleErr
		}
		if event.ID == "" {
			return nil
		}
		state, revision, completedAt := sessionCursorState(current, event, s.now().UTC())
		if _, updateErr := s.queries.UpdateCharlieSessionCursor(ctx, sqlc.UpdateCharlieSessionCursorParams{
			ID: current.ID, State: state, LastEventID: event.ID,
			CentralRevision: revision, CompletedAt: completedAt,
		}); updateErr != nil {
			observeStreamAcknowledgement(false)
			return fmt.Errorf("Charlie event acknowledgement is unavailable")
		}
		observeStreamAcknowledgement(true)
		resources = currentResources
		return nil
	})
	outcome := "closed"
	if err != nil {
		outcome = "failed"
		failed = true
	}
	s.audit(context.WithoutCancel(ctx), "charlie.session.stream_closed", local.ID, actorID, local.Visibility, outcome, len(resources))
	return err
}

func sessionCursorState(current sqlc.CharlieSession, event contract.Event, now time.Time) (string, int64, pgtype.Timestamptz) {
	state := current.State
	completedAt := current.CompletedAt
	switch event.Event {
	case "permission.requested":
		state = "waiting_approval"
	case "permission.responded", "turn.started", "text.delta", "tool.proposed", "tool.running", "tool.succeeded", "tool.failed":
		state = "active"
	case "turn.completed":
		state = "completed"
		completedAt = pgtype.Timestamptz{Time: now, Valid: true}
	case "turn.failed":
		state = "failed"
		completedAt = pgtype.Timestamptz{Time: now, Valid: true}
	case "turn.aborted":
		state = "aborted"
		completedAt = pgtype.Timestamptz{Time: now, Valid: true}
	}
	revision := current.CentralRevision
	if parsed, err := strconv.ParseInt(event.ID, 10, 64); err == nil && parsed > revision {
		revision = parsed
	}
	return state, revision, completedAt
}

func (s *SessionAccessService) authorize(ctx context.Context, actorID, sessionID uuid.UUID) (sqlc.CharlieSession, []sqlc.CharlieSessionResource, string, error) {
	local, resources, err := s.authorizeLocal(ctx, actorID, sessionID)
	if err != nil {
		return sqlc.CharlieSession{}, nil, "", err
	}
	delegation, err := IssueDelegation(ctx, s.queries, s.authority, local.ID, actorID, "user", maxDelegationTTL, s.now().UTC())
	if err != nil {
		return sqlc.CharlieSession{}, nil, "", fmt.Errorf("Charlie session authorization is unavailable")
	}
	return local, resources, delegation.Reference, nil
}

func (s *SessionAccessService) authorizeLocal(ctx context.Context, actorID, sessionID uuid.UUID) (sqlc.CharlieSession, []sqlc.CharlieSessionResource, error) {
	if err := s.guardActive(); err != nil {
		return sqlc.CharlieSession{}, nil, err
	}
	if actorID == uuid.Nil || sessionID == uuid.Nil {
		return sqlc.CharlieSession{}, nil, fmt.Errorf("Charlie session access is invalid")
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active || connection.EmergencyDisabled || EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled) == ModeDisabled {
		return sqlc.CharlieSession{}, nil, fmt.Errorf("Charlie connection is inactive")
	}
	local, err := s.queries.GetCharlieSession(ctx, sessionID)
	if err != nil || local.ConnectionID != connection.ID || strings.TrimSpace(local.CharlieSessionID) == "" || local.State == "aborted" || local.State == "failed" {
		return sqlc.CharlieSession{}, nil, fmt.Errorf("Charlie session is unavailable")
	}
	resources, err := s.queries.ListCharlieSessionResources(ctx, local.ID)
	if err != nil {
		return sqlc.CharlieSession{}, nil, fmt.Errorf("Charlie session scope is unavailable")
	}
	canUse, err := s.authorizer.CanUseCharlie(ctx, actorID)
	if err != nil || !canUse {
		s.audit(ctx, "charlie.session.authorization", local.ID, actorID, local.Visibility, "authorization_denied", len(resources))
		return sqlc.CharlieSession{}, nil, fmt.Errorf("Charlie session access is denied")
	}
	switch local.Visibility {
	case "private":
		if local.Source != "user" || !local.OwnerUserID.Valid || local.OwnerUserID.Bytes != actorID {
			s.audit(ctx, "charlie.session.authorization", local.ID, actorID, local.Visibility, "owner_denied", len(resources))
			return sqlc.CharlieSession{}, nil, fmt.Errorf("Charlie session access is denied")
		}
	case "incident":
		allowed, resourceErr := s.authorizer.CanReadIncidentResources(ctx, actorID, resources)
		if resourceErr != nil || !allowed {
			s.audit(ctx, "charlie.session.authorization", local.ID, actorID, local.Visibility, "resource_denied", len(resources))
			return sqlc.CharlieSession{}, nil, fmt.Errorf("Charlie session access is denied")
		}
	default:
		return sqlc.CharlieSession{}, nil, fmt.Errorf("Charlie session access is denied")
	}
	return local, resources, nil
}

func (s *SessionAccessService) guardActive() error {
	if s == nil || s.active == nil || !s.active() {
		return fmt.Errorf("Charlie runtime is inactive")
	}
	return nil
}

func (s *SessionAccessService) audit(ctx context.Context, action string, sessionID, actorID uuid.UUID, visibility, outcome string, resources int) {
	if s != nil && s.auditor != nil {
		s.auditor.RecordCharlieSessionLifecycle(ctx, SessionLifecycleAudit{Action: action, SessionID: sessionID, ActorID: actorID, Visibility: visibility, OutcomeCode: outcome, ResourceCount: resources})
	}
}
