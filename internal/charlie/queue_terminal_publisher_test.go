package charlie

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type queueTerminalPublisherFake struct {
	triggerIngestFake
	feature    bool
	connection sqlc.CharlieConnection
	rules      []sqlc.CharlieTriggerRule
}

func (f *queueTerminalPublisherFake) GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error) {
	value, _ := json.Marshal(f.feature)
	return sqlc.PlatformSetting{Key: "feature.charlie", Value: value}, nil
}

func (f *queueTerminalPublisherFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}

func (f *queueTerminalPublisherFake) ListEnabledCharlieTriggerRules(context.Context, uuid.UUID) ([]sqlc.CharlieTriggerRule, error) {
	return f.rules, nil
}

func TestQueueTerminalPublisherUsesNormalAtomicTriggerPathWithoutPayload(t *testing.T) {
	connectionID := uuid.New()
	fake := &queueTerminalPublisherFake{
		feature:    true,
		connection: sqlc.CharlieConnection{ID: connectionID, Active: true, OnboardingState: "active", RequestedMode: string(ModeAuto), VerifiedMode: string(ModeAuto)},
		rules: []sqlc.CharlieTriggerRule{{
			ID: uuid.New(), ConnectionID: connectionID, Name: "queue_terminal_failure", Enabled: true, MinimumSeverity: "warning",
			WindowSeconds: 60, CooldownSeconds: 60, ModeCeiling: string(ModeAuto), Thresholds: json.RawMessage(`{}`),
		}},
	}
	publisher, err := NewQueueTerminalFailurePublisher(fake)
	if err != nil {
		t.Fatal(err)
	}
	publisher.now = func() time.Time { return time.Unix(1000, 0).UTC() }
	taskID := uuid.NewString()
	if err = publisher.PublishQueueTerminalFailure(t.Context(), "catalog:sync", taskID, "retry_exhausted"); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 1 {
		t.Fatalf("atomic trigger/outbox writes = %d, want 1", len(fake.created))
	}
	created := fake.created[0]
	if created.EventType != "queue_terminal_failure" || created.ResourceType != "workflow" || created.ResourceID != "catalog:sync:"+taskID ||
		created.OriginResourceRef != "catalog:sync" || created.OriginEventRef != taskID {
		t.Fatalf("queue identity was not preserved: %#v", created)
	}
	metadata := string(created.SummaryMetadata)
	if containsSecret(metadata) || json.Valid(created.SummaryMetadata) == false {
		t.Fatalf("terminal metadata was unsafe: %s", metadata)
	}
}

func TestQueueTerminalPublisherIsInertUnlessExplicitlyActive(t *testing.T) {
	connectionID := uuid.New()
	base := &queueTerminalPublisherFake{
		connection: sqlc.CharlieConnection{ID: connectionID, Active: true, OnboardingState: "active", RequestedMode: string(ModeReadOnly), VerifiedMode: string(ModeReadOnly)},
		rules:      []sqlc.CharlieTriggerRule{{ID: uuid.New(), ConnectionID: connectionID, Name: "queue_terminal_failure", Enabled: true, MinimumSeverity: "warning", WindowSeconds: 60, ModeCeiling: string(ModeReadOnly)}},
	}
	publisher, _ := NewQueueTerminalFailurePublisher(base)
	if err := publisher.PublishQueueTerminalFailure(t.Context(), "catalog:sync", uuid.NewString(), "retry_exhausted"); err != nil || len(base.created) != 0 {
		t.Fatalf("feature-off publisher was not inert: writes=%d err=%v", len(base.created), err)
	}
	base.feature = true
	base.connection.EmergencyDisabled = true
	if err := publisher.PublishQueueTerminalFailure(t.Context(), "catalog:sync", uuid.NewString(), "retry_exhausted"); err != nil || len(base.created) != 0 {
		t.Fatalf("emergency-disabled publisher was not inert: writes=%d err=%v", len(base.created), err)
	}
	base.connection.EmergencyDisabled = false
	base.rules = nil
	if err := publisher.PublishQueueTerminalFailure(t.Context(), "catalog:sync", uuid.NewString(), "retry_exhausted"); err != nil || len(base.created) != 0 {
		t.Fatalf("rule-disabled publisher was not inert: writes=%d err=%v", len(base.created), err)
	}
}

func TestQueueTerminalPublisherRejectsUnboundedOrLogInjectingIdentity(t *testing.T) {
	fake := &queueTerminalPublisherFake{}
	publisher, _ := NewQueueTerminalFailurePublisher(fake)
	for _, taskID := range []string{"", "bad\nidentity", "../escape", strings.Repeat("x", 129)} {
		if err := publisher.PublishQueueTerminalFailure(t.Context(), "catalog:sync", taskID, "retry_exhausted"); err == nil {
			t.Fatalf("unsafe task identity %q was accepted", taskID)
		}
	}
}
