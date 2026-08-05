package charlie

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type triggerAdminFake struct {
	connection sqlc.CharlieConnection
	rows       []sqlc.CharlieTriggerEvent
	list       sqlc.ListCharlieTriggerEventsForAdminParams
	retry      sqlc.RetryDeadCharlieTriggerEventWithOutboxParams
}

func (f *triggerAdminFake) GetLatestCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *triggerAdminFake) ListCharlieTriggerEventsForAdmin(_ context.Context, arg sqlc.ListCharlieTriggerEventsForAdminParams) ([]sqlc.CharlieTriggerEvent, error) {
	f.list = arg
	return f.rows, nil
}
func (f *triggerAdminFake) RetryDeadCharlieTriggerEventWithOutbox(_ context.Context, arg sqlc.RetryDeadCharlieTriggerEventWithOutboxParams) (sqlc.RetryDeadCharlieTriggerEventWithOutboxRow, error) {
	f.retry = arg
	source := f.rows[0]
	return sqlc.RetryDeadCharlieTriggerEventWithOutboxRow{
		ID: arg.RequestID, RuleID: source.RuleID, RetryOfEventID: pgtype.UUID{Bytes: arg.RetryOfEventID, Valid: true},
		EventType: source.EventType, ResourceType: source.ResourceType, ResourceID: source.ResourceID,
		State: "pending", RepeatCount: source.RepeatCount, FirstOccurredAt: source.FirstOccurredAt,
		LastOccurredAt: source.LastOccurredAt, NextAttemptAt: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}, nil
}

func TestTriggerAdminListsBoundedUIStateWithoutStoredMetadata(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	connectionID, eventID := uuid.New(), uuid.New()
	fake := &triggerAdminFake{
		connection: sqlc.CharlieConnection{ID: connectionID},
		rows: []sqlc.CharlieTriggerEvent{{
			ID: eventID, RuleID: uuid.New(), EventType: "queue_terminal_failure", ResourceType: "workflow", ResourceID: "queue-a",
			Fingerprint: "internal-fingerprint", SummaryMetadata: json.RawMessage(`{"summary":"private"}`),
			OriginResourceRef: "internal:resource", OriginEventRef: "audit:event", State: "dead", RepeatCount: 2,
			AttemptCount: 8, LastErrorCode: "bridge_unavailable", FirstOccurredAt: now, LastOccurredAt: now, UpdatedAt: now,
		}},
	}
	service, _ := NewTriggerAdminService(fake)
	items, err := service.List(context.Background(), "dead", 0, 20)
	if err != nil || len(items) != 1 || items[0].ID != eventID.String() || fake.list.ConnectionID != connectionID || !fake.list.EventState.Valid || fake.list.EventState.String != "dead" {
		t.Fatalf("dead-letter list items=%#v query=%#v err=%v", items, fake.list, err)
	}
	raw, _ := json.Marshal(items)
	for _, forbidden := range []string{"private", "internal-fingerprint", "audit:event", "summary_metadata", "origin_event_ref", "session_id"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("admin trigger response leaked %q: %s", forbidden, raw)
		}
	}
}

func TestTriggerAdminRetryUsesFreshIdempotentAttemptAndRetainsSource(t *testing.T) {
	sourceID, requestID := uuid.New(), uuid.New()
	connection := readySessionConnection()
	connection.RequestedMode, connection.VerifiedMode = "read_only", "read_only"
	fake := &triggerAdminFake{
		connection: connection,
		rows:       []sqlc.CharlieTriggerEvent{{ID: sourceID, RuleID: uuid.New(), EventType: "queue_terminal_failure", ResourceType: "workflow", ResourceID: "queue-a", State: "dead", RepeatCount: 4, FirstOccurredAt: time.Now(), LastOccurredAt: time.Now()}},
	}
	service, _ := NewTriggerAdminService(fake)
	view, err := service.Retry(context.Background(), sourceID, requestID)
	if err != nil || view.ID != requestID.String() || view.RetryOfEventID != sourceID.String() || view.State != "pending" {
		t.Fatalf("retry view=%#v err=%v", view, err)
	}
	if fake.retry.RetryOfEventID != sourceID || fake.retry.RequestID != requestID || fake.retry.ConnectionID != connection.ID {
		t.Fatalf("retry query lost immutable correlation: %#v", fake.retry)
	}
}

func TestTriggerAdminRetryFailsClosedWhileDisabled(t *testing.T) {
	connection := readySessionConnection()
	connection.RequestedMode, connection.VerifiedMode = "disabled", "disabled"
	fake := &triggerAdminFake{connection: connection, rows: []sqlc.CharlieTriggerEvent{{ID: uuid.New()}}}
	service, _ := NewTriggerAdminService(fake)
	if _, err := service.Retry(context.Background(), uuid.New(), uuid.New()); err == nil || fake.retry.RequestID != uuid.Nil {
		t.Fatalf("disabled retry was not inert: query=%#v err=%v", fake.retry, err)
	}
}
