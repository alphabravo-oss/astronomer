package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/downstreamboundary"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const MaxTriggerDispatchAttempts = 8

type triggerDispatchQueries interface {
	delegationQuerier
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	ClaimCharlieTriggerEvent(context.Context, uuid.UUID) (sqlc.CharlieTriggerEvent, error)
	GetCharlieTriggerRule(context.Context, uuid.UUID) (sqlc.CharlieTriggerRule, error)
	GetCharlieSessionByClientID(context.Context, sqlc.GetCharlieSessionByClientIDParams) (sqlc.CharlieSession, error)
	CreateCharlieSession(context.Context, sqlc.CreateCharlieSessionParams) (sqlc.CharlieSession, error)
	AddCharlieSessionResource(context.Context, sqlc.AddCharlieSessionResourceParams) error
	BindCharlieSessionCentralID(context.Context, sqlc.BindCharlieSessionCentralIDParams) (sqlc.CharlieSession, error)
	TransitionCharlieTriggerEvent(context.Context, sqlc.TransitionCharlieTriggerEventParams) (sqlc.CharlieTriggerEvent, error)
}

type BridgeInvestigationRequest struct {
	RequestID        string          `json:"request_id"`
	AuthorizationRef string          `json:"authorization_ref"`
	EventType        string          `json:"event_type"`
	ResourceType     string          `json:"resource_type"`
	ResourceID       string          `json:"resource_id"`
	Fingerprint      string          `json:"fingerprint"`
	RepeatCount      int32           `json:"repeat_count"`
	FirstOccurredAt  time.Time       `json:"first_occurred_at"`
	LastOccurredAt   time.Time       `json:"last_occurred_at"`
	SummaryMetadata  json.RawMessage `json:"summary_metadata"`
	ProductVersion   string          `json:"product_version"`
}

type InvestigationBridge interface {
	CreateInvestigation(context.Context, BridgeInvestigationRequest, string) (BridgeSessionReceipt, error)
}

type TriggerLifecyclePublisher interface {
	PublishCharlieTriggerLifecycle(context.Context, uuid.UUID, string, string)
}

type TriggerDispatcher struct {
	queries   triggerDispatchQueries
	bridge    InvestigationBridge
	publisher TriggerLifecyclePublisher
	auditor   AuthorityMutationAuditor
	active    func() bool
	now       func() time.Time
}

func NewTriggerDispatcher(queries triggerDispatchQueries, bridge InvestigationBridge, publisher TriggerLifecyclePublisher, auditor AuthorityMutationAuditor, active func() bool) (*TriggerDispatcher, error) {
	if queries == nil || bridge == nil || publisher == nil || auditor == nil || active == nil {
		return nil, fmt.Errorf("Charlie trigger dispatch requires durable state, bridge, publication, and activation")
	}
	return &TriggerDispatcher{queries: queries, bridge: bridge, publisher: publisher, auditor: auditor, active: active, now: time.Now}, nil
}

// Dispatch handles one task-outbox payload. It claims only the referenced row,
// creates at most one stable incident session, and commits lifecycle state
// before publication. Redis/Asynq delivery may repeat this method safely.
func (d *TriggerDispatcher) Dispatch(ctx context.Context, eventID uuid.UUID) error {
	ctx = downstreamboundary.WithCharlieOrigin(ctx)
	if eventID == uuid.Nil || !d.active() {
		return nil
	}
	if err := requireAuthorityMutationAudit(ctx, d.auditor, AuthorityMutationAudit{
		Action: "charlie.trigger.dispatched", ResourceType: "charlie_trigger", ResourceID: eventID.String(), Fields: map[string]any{"attempt": 1},
	}); err != nil {
		return fmt.Errorf("Charlie trigger dispatch audit is unavailable")
	}
	event, err := d.queries.ClaimCharlieTriggerEvent(ctx, eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		// A prior attempt may have bound the central investigation and then
		// failed before the durable dispatched transition. Complete that
		// commit when the event is still dispatching with a live session.
		return d.completeBoundDispatch(ctx, eventID)
	}
	if err != nil {
		return fmt.Errorf("claim Charlie trigger event: %w", err)
	}
	rule, err := d.queries.GetCharlieTriggerRule(ctx, event.RuleID)
	if err != nil || !validTriggerRule(rule) {
		return d.fail(ctx, event, "rule_inactive")
	}
	connection, err := d.queries.GetActiveCharlieConnection(ctx)
	if err != nil || connection.ID != rule.ConnectionID || !connection.Active || connection.EmergencyDisabled || EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled) == ModeDisabled {
		return d.fail(ctx, event, "connection_inactive")
	}

	local, err := d.queries.GetCharlieSessionByClientID(ctx, sqlc.GetCharlieSessionByClientIDParams{ConnectionID: connection.ID, ClientSessionID: event.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		local, err = d.queries.CreateCharlieSession(ctx, sqlc.CreateCharlieSessionParams{
			ConnectionID: connection.ID, CharlieSessionID: "", ClientSessionID: event.ID,
			OwnerUserID: pgtype.UUID{}, Source: "event", Visibility: "incident",
			Intent: "event_investigation", ResourceScopeSummary: event.ResourceType + ":" + event.ResourceID, State: "creating",
		})
		if err == nil {
			// Always attach the originating finding resource plus the install-wide
			// scope so allowlisted auto writes (resource_id=local) resolve.
			err = d.queries.AddCharlieSessionResource(ctx, sqlc.AddCharlieSessionResourceParams{SessionID: local.ID, ResourceType: event.ResourceType, ResourceID: event.ResourceID, RequiredVerb: "read"})
			if err == nil {
				_ = d.queries.AddCharlieSessionResource(ctx, sqlc.AddCharlieSessionResourceParams{SessionID: local.ID, ResourceType: "installation", ResourceID: "local", RequiredVerb: "read"})
			}
		}
	}
	if err != nil {
		return d.retry(ctx, event, rule, "local_session_unavailable")
	}
	// A stable client-session id is the idempotency boundary for event-driven
	// investigations.  Never revive a locally aborted/failed incident or try to
	// bind a second central session onto a row whose lifecycle is ambiguous.
	// Operators may explicitly re-open the originating trigger as a new event;
	// a worker retry cannot widen authority on its own.
	if local.State == "aborted" || local.State == "failed" || (local.CharlieSessionID == "" && local.State != "creating") {
		return d.fail(ctx, event, "local_session_terminal")
	}
	if local.CharlieSessionID == "" {
		delegation, delegationErr := IssueDelegation(ctx, d.queries, d.auditor, local.ID, rule.ServiceIdentityID, "service", maxDelegationTTL, d.now().UTC())
		if delegationErr != nil {
			return d.retry(ctx, event, rule, "delegation_unavailable")
		}
		receipt, bridgeErr := d.bridge.CreateInvestigation(ctx, BridgeInvestigationRequest{
			RequestID: event.ID.String(), AuthorizationRef: delegation.Reference,
			EventType: event.EventType, ResourceType: event.ResourceType, ResourceID: event.ResourceID,
			Fingerprint: event.Fingerprint, RepeatCount: event.RepeatCount,
			FirstOccurredAt: event.FirstOccurredAt, LastOccurredAt: event.LastOccurredAt,
			SummaryMetadata: append(json.RawMessage(nil), event.SummaryMetadata...), ProductVersion: currentProductDocumentationVersion(),
		}, event.ID.String())
		if bridgeErr != nil || receipt.SessionID == "" || receipt.Revision < 1 {
			return d.retry(ctx, event, rule, "bridge_unavailable")
		}
		local, err = d.queries.BindCharlieSessionCentralID(ctx, sqlc.BindCharlieSessionCentralIDParams{CharlieSessionID: receipt.SessionID, CentralRevision: receipt.Revision, ID: local.ID})
		if err != nil {
			return d.retry(ctx, event, rule, "session_receipt_ambiguous")
		}
	}
	return d.commitDispatched(ctx, event.ID, local.ID)
}

// completeBoundDispatch finishes a dispatch that already created the central
// investigation session but never committed the durable event transition.
func (d *TriggerDispatcher) completeBoundDispatch(ctx context.Context, eventID uuid.UUID) error {
	connection, err := d.queries.GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active {
		return nil
	}
	local, err := d.queries.GetCharlieSessionByClientID(ctx, sqlc.GetCharlieSessionByClientIDParams{ConnectionID: connection.ID, ClientSessionID: eventID})
	if err != nil || local.CharlieSessionID == "" || local.State == "aborted" || local.State == "failed" {
		return nil
	}
	return d.commitDispatched(ctx, eventID, local.ID)
}

func (d *TriggerDispatcher) commitDispatched(ctx context.Context, eventID, sessionID uuid.UUID) error {
	// pgtype.UUID.Scan does not accept google/uuid.UUID; assign the raw bytes.
	sessionUUID := pgtype.UUID{Bytes: [16]byte(sessionID), Valid: true}
	transitioned, err := d.queries.TransitionCharlieTriggerEvent(ctx, sqlc.TransitionCharlieTriggerEventParams{
		NextState: "dispatched", SessionID: sessionUUID,
		NextAttemptAt: d.now().UTC(), LastErrorCode: "", ID: eventID, ExpectedState: "dispatching",
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Already left dispatching (dispatched/dead/retry) — treat as done.
			return nil
		}
		return fmt.Errorf("commit Charlie trigger dispatch: %w", err)
	}
	d.publisher.PublishCharlieTriggerLifecycle(ctx, transitioned.ID, transitioned.State, "")
	observeTrigger(transitioned.EventType, "dispatched")
	return nil
}

func (d *TriggerDispatcher) retry(ctx context.Context, event sqlc.CharlieTriggerEvent, rule sqlc.CharlieTriggerRule, code string) error {
	if event.AttemptCount >= triggerMaximumAttempts(rule) {
		return d.fail(ctx, event, code)
	}
	next := d.now().UTC().Add(triggerRetryBackoff(event.AttemptCount))
	transitioned, err := d.queries.TransitionCharlieTriggerEvent(ctx, sqlc.TransitionCharlieTriggerEventParams{
		NextState: "retry", SessionID: pgtype.UUID{}, NextAttemptAt: next,
		LastErrorCode: code, ID: event.ID, ExpectedState: "dispatching",
	})
	if err != nil {
		return fmt.Errorf("schedule Charlie trigger retry: %w", err)
	}
	d.publisher.PublishCharlieTriggerLifecycle(ctx, transitioned.ID, transitioned.State, code)
	observeTrigger(event.EventType, "retry")
	return fmt.Errorf("Charlie trigger dispatch will retry: %s", code)
}

func triggerMaximumAttempts(rule sqlc.CharlieTriggerRule) int32 {
	maximum := int32(MaxTriggerDispatchAttempts)
	var thresholds map[string]any
	if json.Unmarshal(rule.Thresholds, &thresholds) == nil {
		if configured, ok := thresholds["maximum_attempts"].(float64); ok && configured >= 1 && configured <= 20 {
			maximum = int32(configured)
		}
	}
	return maximum
}

func (d *TriggerDispatcher) fail(ctx context.Context, event sqlc.CharlieTriggerEvent, code string) error {
	transitioned, err := d.queries.TransitionCharlieTriggerEvent(ctx, sqlc.TransitionCharlieTriggerEventParams{
		NextState: "dead", SessionID: pgtype.UUID{}, NextAttemptAt: d.now().UTC(),
		LastErrorCode: code, ID: event.ID, ExpectedState: "dispatching",
	})
	if err != nil {
		return fmt.Errorf("dead-letter Charlie trigger: %w", err)
	}
	d.publisher.PublishCharlieTriggerLifecycle(ctx, transitioned.ID, transitioned.State, code)
	observeTrigger(event.EventType, "dead")
	return nil
}

func triggerRetryBackoff(attempt int32) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<uint(attempt-1)) * 15 * time.Second
}
