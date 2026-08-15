package charlie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const SREContextSchema = "astronomer.sre-context/v1"

const (
	maxSessionIntentCharacters = 128
	maxSessionObjectiveBytes   = 4096
)

var ErrInvalidSessionRequest = errors.New("Charlie session request is invalid")

type SessionResource struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	RequiredVerb string `json:"required_verb"`
}

type SREContext struct {
	Schema                 string            `json:"schema"`
	InstallationID         string            `json:"installation_id"`
	ProductVersion         string            `json:"product_version"`
	ChartVersion           string            `json:"chart_version,omitempty"`
	Namespace              string            `json:"namespace,omitempty"`
	Release                string            `json:"release,omitempty"`
	KubernetesVersion      string            `json:"kubernetes_version,omitempty"`
	KubernetesDistribution string            `json:"kubernetes_distribution,omitempty"`
	Trigger                string            `json:"trigger,omitempty"`
	CurrentUIContext       string            `json:"current_ui_context,omitempty"`
	Resources              []SessionResource `json:"resources"`
	HealthSummary          string            `json:"health_summary,omitempty"`
	CorrelationRef         string            `json:"correlation_ref"`
}

type SessionContextProvider interface {
	Context(context.Context, []SessionResource, string, string) (SREContext, error)
}

type BridgeSessionRequest struct {
	ClientSessionID  string     `json:"client_session_id"`
	ActorID          string     `json:"actor_id"`
	ActorType        string     `json:"actor_type"`
	ActorLabel       string     `json:"actor_label"`
	AuthorizationRef string     `json:"authorization_ref"`
	Intent           string     `json:"intent"`
	Objective        string     `json:"objective"`
	ProductVersion   string               `json:"product_version"`
	Context          SREContext           `json:"context"`
	Platforms        []PlatformAssertion  `json:"platforms"`
}

type BridgeSessionReceipt struct {
	SessionID string `json:"session_id"`
	Revision  int64  `json:"revision"`
}

type SessionBridge interface {
	CreateSession(context.Context, BridgeSessionRequest, string) (BridgeSessionReceipt, error)
}

type sessionQueries interface {
	delegationQuerier
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	CreateCharlieSession(context.Context, sqlc.CreateCharlieSessionParams) (sqlc.CharlieSession, error)
	GetCharlieSessionByClientID(context.Context, sqlc.GetCharlieSessionByClientIDParams) (sqlc.CharlieSession, error)
	BindCharlieSessionCentralID(context.Context, sqlc.BindCharlieSessionCentralIDParams) (sqlc.CharlieSession, error)
	FailCreatingCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error)
	AddCharlieSessionResource(context.Context, sqlc.AddCharlieSessionResourceParams) error
	RevokeCharlieDelegationsForSession(context.Context, uuid.UUID) (int64, error)
}

type SessionService struct {
	queries    sessionQueries
	bridge     SessionBridge
	context    SessionContextProvider
	inventory  PlatformInventoryProvider
	authorizer SessionAccessAuthorizer
	auditor    AuthorityMutationAuditor
	active     func() bool
	now        func() time.Time
}

func (s *SessionService) SetPlatformInventory(provider PlatformInventoryProvider) {
	s.inventory = provider
}

type CreateSessionInput struct {
	ClientSessionID  uuid.UUID
	OwnerID          uuid.UUID
	ActorType        string
	ActorLabel       string
	Intent           string
	Trigger          string
	CurrentUIContext string
	Resources        []SessionResource
}

type CreatedSession struct {
	Local            sqlc.CharlieSession
	AuthorizationRef string `json:"-"`
	Replayed         bool
}

func NewSessionService(queries sessionQueries, bridge SessionBridge, contextProvider SessionContextProvider, authorizer SessionAccessAuthorizer, auditor AuthorityMutationAuditor, active func() bool) (*SessionService, error) {
	if queries == nil || bridge == nil || contextProvider == nil || authorizer == nil || auditor == nil || active == nil {
		return nil, fmt.Errorf("Charlie sessions require local persistence, product bridge, context, live authorization, and activation")
	}
	return &SessionService{queries: queries, bridge: bridge, context: contextProvider, authorizer: authorizer, auditor: auditor, active: active, now: time.Now}, nil
}

// DefaultInstallationResource is attached when a chat session is opened without
// an explicit product context picker. Write tools require arguments.resource_id
// to match a session-scoped resource; without this default, ordinary NL asks
// like "scale the worker" cannot propose writes because the model has no
// resource_id a user would ever know.
const DefaultInstallationResourceID = "local"

func defaultSessionResources(resources []SessionResource) []SessionResource {
	if len(resources) > 0 {
		return resources
	}
	return []SessionResource{{
		Type: "installation", ID: DefaultInstallationResourceID, RequiredVerb: "read",
	}}
}

func (s *SessionService) Create(ctx context.Context, input CreateSessionInput) (CreatedSession, error) {
	if !s.active() {
		return CreatedSession{}, fmt.Errorf("Charlie runtime is inactive")
	}
	input.Resources = defaultSessionResources(input.Resources)
	if err := validateCreateSession(input); err != nil {
		return CreatedSession{}, err
	}
	canUse, authErr := s.authorizer.CanUseCharlie(ctx, input.OwnerID)
	resources := make([]sqlc.CharlieSessionResource, 0, len(input.Resources))
	for _, resource := range input.Resources {
		resources = append(resources, sqlc.CharlieSessionResource{ResourceType: resource.Type, ResourceID: resource.ID, RequiredVerb: resource.RequiredVerb})
	}
	canRead, resourceErr := s.authorizer.CanReadIncidentResources(ctx, input.OwnerID, resources)
	if authErr != nil || resourceErr != nil || !canUse || !canRead {
		return CreatedSession{}, fmt.Errorf("Charlie session resource access is denied")
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active || connection.EmergencyDisabled || EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled) == ModeDisabled {
		return CreatedSession{}, fmt.Errorf("Charlie connection is inactive")
	}

	local, err := s.queries.GetCharlieSessionByClientID(ctx, sqlc.GetCharlieSessionByClientIDParams{ConnectionID: connection.ID, ClientSessionID: input.ClientSessionID})
	if err == nil && local.CharlieSessionID != "" {
		if !local.OwnerUserID.Valid || local.OwnerUserID.Bytes != input.OwnerID || local.Visibility != "private" || local.Source != "user" {
			return CreatedSession{}, fmt.Errorf("Charlie session ownership changed")
		}
		return CreatedSession{Local: local, Replayed: true}, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CreatedSession{}, fmt.Errorf("load Charlie session: %w", err)
	}
	if err := requireAuthorityMutationAudit(ctx, s.auditor, AuthorityMutationAudit{
		Action: "charlie.session.created", ResourceType: "charlie_session", ResourceID: input.ClientSessionID.String(), ActorID: input.OwnerID,
		Fields: map[string]any{"visibility": "private", "resource_count": len(input.Resources)},
	}); err != nil {
		return CreatedSession{}, fmt.Errorf("Charlie session audit is unavailable")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		intent := sessionIntentSummary(input.Intent)
		local, err = s.queries.CreateCharlieSession(ctx, sqlc.CreateCharlieSessionParams{
			ConnectionID: connection.ID, CharlieSessionID: "", ClientSessionID: input.ClientSessionID,
			OwnerUserID: pgtype.UUID{Bytes: input.OwnerID, Valid: true}, Source: "user", Visibility: "private",
			Intent: intent, ResourceScopeSummary: resourceScopeSummary(input.Resources), State: "creating",
		})
		if err != nil {
			return CreatedSession{}, fmt.Errorf("persist Charlie session intent: %w", err)
		}
		for _, resource := range input.Resources {
			if err := s.queries.AddCharlieSessionResource(ctx, sqlc.AddCharlieSessionResourceParams{SessionID: local.ID, ResourceType: resource.Type, ResourceID: resource.ID, RequiredVerb: resource.RequiredVerb}); err != nil {
				_, _ = s.queries.FailCreatingCharlieSession(ctx, local.ID)
				return CreatedSession{}, fmt.Errorf("persist Charlie session scope: %w", err)
			}
		}
	}

	delegation, err := IssueDelegation(ctx, s.queries, s.auditor, local.ID, input.OwnerID, input.ActorType, maxDelegationTTL, s.now().UTC())
	if err != nil {
		return CreatedSession{}, err
	}
	productContext, err := s.context.Context(ctx, input.Resources, input.Trigger, input.CurrentUIContext)
	if err != nil || !validSREContext(productContext, connection.InstallationID.String()) {
		_, _ = s.queries.RevokeCharlieDelegationsForSession(ctx, local.ID)
		return CreatedSession{}, fmt.Errorf("build bounded Charlie product context")
	}
	productContext.ProductVersion = currentProductDocumentationVersion()
	productContext.Resources = append([]SessionResource(nil), input.Resources...)
	receipt, err := s.bridge.CreateSession(ctx, BridgeSessionRequest{
		ClientSessionID: input.ClientSessionID.String(), ActorID: input.OwnerID.String(),
		ActorType: input.ActorType, ActorLabel: input.ActorLabel,
		AuthorizationRef: delegation.Reference, Intent: sessionIntentSummary(input.Intent), Objective: input.Intent,
		ProductVersion: currentProductDocumentationVersion(), Context: productContext,
		Platforms: collectPlatforms(ctx, s.inventory),
	}, input.ClientSessionID.String())
	if err != nil {
		_, _ = s.queries.RevokeCharlieDelegationsForSession(ctx, local.ID)
		return CreatedSession{}, fmt.Errorf("Charlie bridge session is unavailable")
	}
	if strings.TrimSpace(receipt.SessionID) == "" || receipt.Revision < 1 {
		_, _ = s.queries.RevokeCharlieDelegationsForSession(ctx, local.ID)
		_, _ = s.queries.FailCreatingCharlieSession(ctx, local.ID)
		return CreatedSession{}, fmt.Errorf("Charlie bridge returned an invalid session receipt")
	}
	bound, err := s.queries.BindCharlieSessionCentralID(ctx, sqlc.BindCharlieSessionCentralIDParams{CharlieSessionID: receipt.SessionID, CentralRevision: receipt.Revision, ID: local.ID})
	if err != nil {
		_, _ = s.queries.RevokeCharlieDelegationsForSession(ctx, local.ID)
		return CreatedSession{}, fmt.Errorf("persist Charlie bridge session receipt: %w", err)
	}
	return CreatedSession{Local: bound, AuthorizationRef: delegation.Reference}, nil
}

func validateCreateSession(input CreateSessionInput) error {
	if input.ClientSessionID == uuid.Nil || input.OwnerID == uuid.Nil || input.ActorType != "user" || strings.TrimSpace(input.ActorLabel) == "" || len(input.ActorLabel) > 128 || strings.TrimSpace(input.Intent) == "" || len(input.Intent) > maxSessionObjectiveBytes || len(input.Trigger) > 128 || len(input.CurrentUIContext) > 255 || len(input.Resources) > 25 {
		return ErrInvalidSessionRequest
	}
	seenIDs := make(map[string]struct{}, len(input.Resources))
	for _, resource := range input.Resources {
		if !allowedSessionResource(resource) {
			return ErrInvalidSessionRequest
		}
		if _, duplicate := seenIDs[resource.ID]; duplicate {
			return ErrInvalidSessionRequest
		}
		seenIDs[resource.ID] = struct{}{}
	}
	return nil
}

func sessionIntentSummary(objective string) string {
	runes := []rune(strings.TrimSpace(objective))
	if len(runes) > maxSessionIntentCharacters {
		runes = runes[:maxSessionIntentCharacters]
	}
	return string(runes)
}

func validSREContext(value SREContext, installationID string) bool {
	if value.Schema != SREContextSchema || value.InstallationID != installationID ||
		len(value.ProductVersion) > 64 || len(value.ChartVersion) > 64 || len(value.Namespace) > 63 ||
		len(value.Release) > 128 || len(value.KubernetesVersion) > 64 || len(value.KubernetesDistribution) > 64 ||
		len(value.Trigger) > 128 || len(value.CurrentUIContext) > 255 || len(value.HealthSummary) > 1024 ||
		strings.TrimSpace(value.CorrelationRef) == "" || len(value.CorrelationRef) > 128 {
		return false
	}
	return true
}

func allowedSessionResource(resource SessionResource) bool {
	allowedTypes := map[string]bool{
		"installation": true, "management_component": true, "alert": true,
		"backup": true, "self_management_application": true,
		"agent_connection_record": true, "agent_fleet": true, "tunnel": true,
	}
	return allowedTypes[resource.Type] && strings.TrimSpace(resource.ID) != "" && len(resource.ID) <= 255 && resource.RequiredVerb == "read"
}

func resourceScopeSummary(resources []SessionResource) string {
	if len(resources) == 0 {
		return "installation"
	}
	if len(resources) == 1 && resources[0].Type == "installation" && resources[0].ID == DefaultInstallationResourceID {
		return "installation"
	}
	return fmt.Sprintf("%d explicitly attached management-plane resources", len(resources))
}
