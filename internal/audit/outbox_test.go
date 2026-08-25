package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type outboxCapture struct {
	arg sqlc.UpsertAuditOutboxParams
	err error
}

func (c *outboxCapture) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	c.arg = arg
	return sqlc.AuditOutbox{ID: arg.ID, DedupeKey: arg.DedupeKey, Detail: arg.Detail}, c.err
}

func TestRecordOutboxBuildsSanitizedStableIntent(t *testing.T) {
	id := uuid.New()
	created := time.Date(2026, 8, 23, 12, 0, 0, 0, time.FixedZone("test", 3600))
	q := &outboxCapture{}
	key := MutationDedupeKey("req-1", "cluster.delete", "cluster", "c-1")
	row, err := RecordOutbox(context.Background(), q, Event{
		Action: "cluster.delete", ResourceType: "cluster", ResourceID: "c-1",
		Detail: map[string]any{"ticket": "INC-1", "token": "must-not-survive"},
	}, key, OutboxOptions{ID: id, CreatedAt: created, MaxAttempts: 7})
	if err != nil {
		t.Fatalf("RecordOutbox: %v", err)
	}
	if row.ID != id || q.arg.EventCreatedAt.Location() != time.UTC || q.arg.MaxAttempts != 7 {
		t.Fatalf("outbox identity/options = %#v", q.arg)
	}
	if q.arg.DedupeKey != key || len(key) != len("audit:v1:")+64 {
		t.Fatalf("dedupe key = %q", q.arg.DedupeKey)
	}
	var detail map[string]any
	if err := json.Unmarshal(q.arg.Detail, &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail["token"] != "[redacted]" || detail["ticket"] != "INC-1" {
		t.Fatalf("sanitized detail = %#v", detail)
	}
}

func TestRecordOutboxRejectsMissingStoreAndInvalidEnvelope(t *testing.T) {
	_, err := RecordOutbox(context.Background(), nil, Event{}, "key", OutboxOptions{})
	if !errors.Is(err, ErrOutboxUnavailable) {
		t.Fatalf("missing store error = %v", err)
	}
	q := &outboxCapture{}
	for _, tc := range []struct {
		name  string
		event Event
		key   string
	}{
		{name: "empty key", event: Event{Action: "a", ResourceType: "r"}},
		{name: "empty action", event: Event{ResourceType: "r"}, key: "key"},
		{name: "empty resource", event: Event{Action: "a"}, key: "key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RecordOutbox(context.Background(), q, tc.event, tc.key, OutboxOptions{})
			if !errors.Is(err, ErrInvalidOutbox) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
