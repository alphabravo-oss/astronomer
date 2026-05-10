package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeWriter struct {
	last *sqlc.CreateAuditLogV1Params
}

func (f *fakeWriter) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.last = &arg
	return nil
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
