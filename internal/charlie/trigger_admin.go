package charlie

import (
	"context"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const MaxAdminTriggerEvents = 100

type triggerAdminQueries interface {
	GetLatestCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	ListCharlieTriggerEventsForAdmin(context.Context, sqlc.ListCharlieTriggerEventsForAdminParams) ([]sqlc.CharlieTriggerEvent, error)
	RetryDeadCharlieTriggerEventWithOutbox(context.Context, sqlc.RetryDeadCharlieTriggerEventWithOutboxParams) (sqlc.RetryDeadCharlieTriggerEventWithOutboxRow, error)
}

// AdminTriggerEventView deliberately omits the event summary, fingerprint,
// origin references, session identifier, and outbox payload. It is safe for an
// administrator UI and contains only the state needed to diagnose/retry work.
type AdminTriggerEventView struct {
	ID             string `json:"id"`
	RetryOfEventID string `json:"retry_of_event_id,omitempty"`
	RuleID         string `json:"rule_id"`
	EventType      string `json:"event_type"`
	ResourceType   string `json:"resource_type"`
	ResourceID     string `json:"resource_id"`
	State          string `json:"state"`
	RepeatCount    int32  `json:"repeat_count"`
	AttemptCount   int32  `json:"attempt_count"`
	LastErrorCode  string `json:"last_error_code,omitempty"`
	FirstOccurred  string `json:"first_occurred_at"`
	LastOccurred   string `json:"last_occurred_at"`
	DeadLetteredAt string `json:"dead_lettered_at,omitempty"`
	UpdatedAt      string `json:"updated_at"`
}

type TriggerAdminService struct{ queries triggerAdminQueries }

func NewTriggerAdminService(queries triggerAdminQueries) (*TriggerAdminService, error) {
	if queries == nil {
		return nil, ErrAdminUnavailable
	}
	return &TriggerAdminService{queries: queries}, nil
}

func (s *TriggerAdminService) List(ctx context.Context, state string, offset, limit int32) ([]AdminTriggerEventView, error) {
	if s == nil || offset < 0 || limit < 1 || limit > MaxAdminTriggerEvents || !validAdminTriggerEventState(state) {
		return nil, ErrAdminConflict
	}
	connection, err := s.queries.GetLatestCharlieConnection(ctx)
	if err != nil {
		return nil, ErrAdminNotConfigured
	}
	stateArg := pgtype.Text{}
	if state != "" {
		stateArg = pgtype.Text{String: state, Valid: true}
	}
	rows, err := s.queries.ListCharlieTriggerEventsForAdmin(ctx, sqlc.ListCharlieTriggerEventsForAdminParams{
		ConnectionID: connection.ID, EventState: stateArg, PageOffset: offset, PageLimit: limit,
	})
	if err != nil {
		return nil, ErrAdminUnavailable
	}
	items := make([]AdminTriggerEventView, 0, len(rows))
	for _, row := range rows {
		items = append(items, safeAdminTriggerEvent(row))
	}
	return items, nil
}

func (s *TriggerAdminService) Retry(ctx context.Context, eventID, requestID uuid.UUID) (AdminTriggerEventView, error) {
	if s == nil || eventID == uuid.Nil || requestID == uuid.Nil {
		return AdminTriggerEventView{}, ErrAdminConflict
	}
	connection, err := s.queries.GetLatestCharlieConnection(ctx)
	if err != nil {
		return AdminTriggerEventView{}, ErrAdminNotConfigured
	}
	if !connection.Active || connection.EmergencyDisabled || EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled) == ModeDisabled {
		return AdminTriggerEventView{}, fmt.Errorf("%w: Charlie must be active before retrying trigger work", ErrAdminConflict)
	}
	row, err := s.queries.RetryDeadCharlieTriggerEventWithOutbox(ctx, sqlc.RetryDeadCharlieTriggerEventWithOutboxParams{
		RetryOfEventID: eventID, ConnectionID: connection.ID, RequestID: requestID,
	})
	if err != nil {
		return AdminTriggerEventView{}, fmt.Errorf("%w: dead-letter retry was not created", ErrAdminConflict)
	}
	return safeAdminRetriedTriggerEvent(row), nil
}

func validAdminTriggerEventState(state string) bool {
	switch state {
	case "", "pending", "dispatching", "dispatched", "retry", "dead", "completed", "suppressed":
		return true
	default:
		return false
	}
}

func safeAdminTriggerEvent(row sqlc.CharlieTriggerEvent) AdminTriggerEventView {
	view := AdminTriggerEventView{
		ID: row.ID.String(), RuleID: row.RuleID.String(), EventType: row.EventType,
		ResourceType: row.ResourceType, ResourceID: row.ResourceID, State: row.State,
		RepeatCount: row.RepeatCount, AttemptCount: row.AttemptCount,
		LastErrorCode: row.LastErrorCode, FirstOccurred: utcText(row.FirstOccurredAt),
		LastOccurred: utcText(row.LastOccurredAt), UpdatedAt: utcText(row.UpdatedAt),
	}
	if row.RetryOfEventID.Valid {
		view.RetryOfEventID = uuid.UUID(row.RetryOfEventID.Bytes).String()
	}
	if row.DeadLetteredAt.Valid {
		view.DeadLetteredAt = utcText(row.DeadLetteredAt.Time)
	}
	return view
}

func safeAdminRetriedTriggerEvent(row sqlc.RetryDeadCharlieTriggerEventWithOutboxRow) AdminTriggerEventView {
	view := AdminTriggerEventView{
		ID: row.ID.String(), RuleID: row.RuleID.String(), EventType: row.EventType,
		ResourceType: row.ResourceType, ResourceID: row.ResourceID, State: row.State,
		RepeatCount: row.RepeatCount, AttemptCount: row.AttemptCount,
		LastErrorCode: row.LastErrorCode, FirstOccurred: utcText(row.FirstOccurredAt),
		LastOccurred: utcText(row.LastOccurredAt), UpdatedAt: utcText(row.UpdatedAt),
	}
	if row.RetryOfEventID.Valid {
		view.RetryOfEventID = uuid.UUID(row.RetryOfEventID.Bytes).String()
	}
	if row.DeadLetteredAt.Valid {
		view.DeadLetteredAt = utcText(row.DeadLetteredAt.Time)
	}
	return view
}

func utcText(value time.Time) string { return value.UTC().Format(time.RFC3339) }
