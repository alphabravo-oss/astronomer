package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type fakeWriter struct {
	last *sqlc.CreateAuditLogV1Params
	err  error
}

func (f *fakeWriter) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.last = &arg
	return f.err
}

func TestRecordMandatoryReturnsPersistenceFailure(t *testing.T) {
	want := errors.New("database unavailable")
	writer := &fakeWriter{err: want}
	err := RecordMandatory(context.Background(), writer, Event{
		Action:       "compliance.report.export",
		ActionClass:  ClassRead,
		ResourceType: "security_scan",
	})
	if !errors.Is(err, want) {
		t.Fatalf("RecordMandatory error = %v, want %v", err, want)
	}
	if writer.last == nil {
		t.Fatal("mandatory audit row was not attempted")
	}
	if writer.last.ActionClass != ClassRead {
		t.Fatalf("action class = %q, want %q", writer.last.ActionClass, ClassRead)
	}
}

func TestRecordMandatoryRejectsMissingWriter(t *testing.T) {
	if err := RecordMandatory(context.Background(), nil, Event{Action: "compliance.report.export"}); !errors.Is(err, ErrMandatoryPersistenceUnavailable) {
		t.Fatalf("RecordMandatory nil writer error = %v", err)
	}
}

func TestSanitizeDetail(t *testing.T) {
	sanitized := SanitizeDetail(map[string]any{
		"password": "secret",
		"nested": map[string]any{
			"token": "abc",
			"name":  "visible",
			"items": []any{
				map[string]any{"client_secret": "hidden"},
			},
		},
	})

	if sanitized["password"] != "[redacted]" {
		t.Fatalf("password = %v, want [redacted]", sanitized["password"])
	}
	nested := sanitized["nested"].(map[string]any)
	if nested["token"] != "[redacted]" {
		t.Fatalf("nested token = %v, want [redacted]", nested["token"])
	}
	if nested["name"] != "visible" {
		t.Fatalf("nested name = %v, want visible", nested["name"])
	}
	items := nested["items"].([]any)
	item := items[0].(map[string]any)
	if item["client_secret"] != "[redacted]" {
		t.Fatalf("nested list secret = %v, want [redacted]", item["client_secret"])
	}
}

func TestRecordDefaultsAndRedaction(t *testing.T) {
	writer := &fakeWriter{}
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(previous)

	Record(context.Background(), writer, Event{
		UserID:       pgtype.UUID{},
		Action:       "auth.login_failed",
		ResourceType: "user",
		RequestID:    "req-123",
		Detail: map[string]any{
			"password": "secret",
			"email":    "user@example.com",
		},
	})

	if writer.last == nil {
		t.Fatal("expected audit row to be written")
	}
	if writer.last.Source != "service" {
		t.Fatalf("source = %q, want service", writer.last.Source)
	}
	if writer.last.CorrelationID != "req-123" {
		t.Fatalf("correlation_id = %q, want req-123", writer.last.CorrelationID)
	}
	if writer.last.ActionClass != ClassAuth {
		t.Fatalf("action_class = %q, want %s", writer.last.ActionClass, ClassAuth)
	}

	var detail map[string]any
	if err := json.Unmarshal(writer.last.Detail, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail["password"] != "[redacted]" {
		t.Fatalf("password = %v, want [redacted]", detail["password"])
	}
	if detail["email"] != "user@example.com" {
		t.Fatalf("email = %v, want user@example.com", detail["email"])
	}

	var logPayload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &logPayload); err != nil {
		t.Fatalf("unmarshal log payload: %v", err)
	}
	if logPayload["event"] != "audit_recorded" {
		t.Fatalf("event = %v, want audit_recorded", logPayload["event"])
	}
	if logPayload["correlation_id"] != "req-123" {
		t.Fatalf("correlation_id = %v, want req-123", logPayload["correlation_id"])
	}
}

func TestRecordIncludesBeforeAfterAndTags(t *testing.T) {
	writer := &fakeWriter{}

	Record(context.Background(), writer, Event{
		Action:       "role.update",
		ResourceType: "global_role",
		RequestID:    "req-456",
		Before: map[string]any{
			"name":   "viewer",
			"secret": "hidden-before",
		},
		After: map[string]any{
			"name": "editor",
			"spec": []any{map[string]any{"token": "hidden-after"}},
		},
		Tags: map[string]string{
			"scope": "global",
		},
	})

	var detail map[string]any
	if err := json.Unmarshal(writer.last.Detail, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}

	before := detail["before"].(map[string]any)
	if before["secret"] != "[redacted]" {
		t.Fatalf("before.secret = %v, want [redacted]", before["secret"])
	}
	after := detail["after"].(map[string]any)
	spec := after["spec"].([]any)[0].(map[string]any)
	if spec["token"] != "[redacted]" {
		t.Fatalf("after.spec[0].token = %v, want [redacted]", spec["token"])
	}
	tags := detail["tags"].(map[string]any)
	if tags["scope"] != "global" {
		t.Fatalf("tags.scope = %v, want global", tags["scope"])
	}
}

type invalidEnvelopeBus struct{ calls int }

func (b *invalidEnvelopeBus) Publish(string, any) { b.calls++ }

func TestRecordInvalidEnvelopeReportsFailureWithoutDBOrBus(t *testing.T) {
	var log bytes.Buffer
	previousLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&log, nil)))
	defer slog.SetDefault(previousLog)
	busPublisherMu.RLock()
	previousBus := busPublisher
	busPublisherMu.RUnlock()
	bus := &invalidEnvelopeBus{}
	SetBusPublisher(bus)
	defer SetBusPublisher(previousBus)
	writer := &fakeWriter{}
	event := Event{Action: "cluster.update", ResourceType: "cluster", ResourceID: "private-fixture", Detail: map[string]any{"value": make(chan string)}}
	counter := auditWriteFailuresTotal.WithLabelValues(observability.MetricValues("sync")...)
	before := testutil.ToFloat64(counter)
	Record(t.Context(), writer, event)
	if err := RecordMandatory(t.Context(), writer, event); !errors.Is(err, ErrMandatoryPersistenceUnavailable) {
		t.Fatalf("mandatory path did not reject invalid envelope: %v", err)
	}
	if writer.last != nil || bus.calls != 0 {
		t.Fatalf("invalid envelope reached database/bus: db=%t bus=%d", writer.last != nil, bus.calls)
	}
	if testutil.ToFloat64(counter) != before+2 {
		t.Fatal("marshal failures missing from existing persistence failure metric")
	}
	if strings.Contains(log.String(), "private-fixture") || !strings.Contains(log.String(), "invalid_json") {
		t.Fatal("diagnostic must contain category and no event content")
	}
}
