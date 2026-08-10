package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type queueTerminalPublisherQueries interface {
	triggerIngestQueries
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error)
	ListEnabledCharlieTriggerRules(context.Context, uuid.UUID) ([]sqlc.CharlieTriggerRule, error)
}

// QueueTerminalFailurePublisher is the normal production adapter from Asynq's
// terminal error callback into the same durable, deduplicating trigger/outbox
// transaction used by every other product-owned signal. It cannot see payloads
// or error strings and therefore cannot leak job content into Charlie.
type QueueTerminalFailurePublisher struct {
	queries queueTerminalPublisherQueries
	now     func() time.Time
}

var queueTerminalIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func NewQueueTerminalFailurePublisher(queries queueTerminalPublisherQueries) (*QueueTerminalFailurePublisher, error) {
	if queries == nil {
		return nil, fmt.Errorf("Charlie queue terminal publisher storage is unavailable")
	}
	return &QueueTerminalFailurePublisher{queries: queries, now: time.Now}, nil
}

func (p *QueueTerminalFailurePublisher) PublishQueueTerminalFailure(ctx context.Context, taskType, taskID, failureClass string) error {
	if p == nil || p.queries == nil || !queueTerminalIdentityPattern.MatchString(taskType) || !queueTerminalIdentityPattern.MatchString(taskID) ||
		(failureClass != "retry_exhausted" && failureClass != "non_retryable" && failureClass != "panic_retry_exhausted") {
		return fmt.Errorf("Charlie queue terminal failure binding is invalid")
	}
	setting, err := p.queries.GetPlatformSetting(ctx, "feature.charlie")
	var featureEnabled bool
	if err != nil || json.Unmarshal(setting.Value, &featureEnabled) != nil || !featureEnabled {
		return nil
	}
	connection, err := p.queries.GetActiveCharlieConnection(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	if !connection.Active || connection.OnboardingState != "active" || connection.EmergencyDisabled ||
		connection.RequestedMode == string(ModeDisabled) || connection.VerifiedMode == string(ModeDisabled) {
		return nil
	}
	rules, err := p.queries.ListEnabledCharlieTriggerRules(ctx, connection.ID)
	if err != nil {
		return err
	}
	var rule sqlc.CharlieTriggerRule
	for _, candidate := range rules {
		if candidate.Name == "queue_terminal_failure" {
			rule = candidate
			break
		}
	}
	if rule.ID == [16]byte{} {
		return nil
	}
	resourceID := taskType + ":" + taskID
	if len(resourceID) > 255 {
		return fmt.Errorf("Charlie queue terminal failure identity exceeds bound")
	}
	ingestor, err := NewTriggerIngestor(p.queries, func() bool { return true })
	if err != nil {
		return err
	}
	_, _, err = ingestor.Ingest(ctx, rule, TriggerObservation{
		Signal: TriggerSignal{
			Source: "astronomer_queue", EventType: "queue_terminal_failure", ResourceType: "workflow", ResourceID: resourceID,
			FailureClass: failureClass, Severity: "critical", State: "terminal", Timestamp: p.now().UTC(),
			Summary: "A background workflow reached its terminal retry boundary.",
		},
		OriginResourceRef: taskType, OriginEventRef: taskID,
	})
	return err
}
