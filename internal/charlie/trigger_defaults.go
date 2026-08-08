package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type defaultTriggerQuerier interface {
	CreateCharlieTriggerRule(context.Context, sqlc.CreateCharlieTriggerRuleParams) (sqlc.CharlieTriggerRule, error)
}

// EnsureDefaultTriggerRules persists reviewed product policy disabled by
// default. Onboarding therefore exposes available automation without silently
// starting investigations or writes.
func EnsureDefaultTriggerRules(ctx context.Context, q defaultTriggerQuerier, connectionID, serviceIdentityID, actorID uuid.UUID) error {
	if q == nil || connectionID == uuid.Nil || serviceIdentityID == uuid.Nil || actorID == uuid.Nil {
		return fmt.Errorf("Charlie default trigger identity is invalid")
	}
	for _, rule := range DefaultTriggerRules() {
		thresholds := map[string]any{"count": rule.Threshold, "maximum_attempts": MaxTriggerDispatchAttempts, "dead_letter_enabled": true}
		encodedThresholds, err := json.Marshal(thresholds)
		if err != nil {
			return err
		}
		if _, err := q.CreateCharlieTriggerRule(ctx, sqlc.CreateCharlieTriggerRuleParams{
			ConnectionID: connectionID, Name: rule.Name, RuleType: rule.Name,
			Category: rule.Category, Enabled: rule.EnabledByDefault,
			MinimumSeverity: rule.MinimumSeverity, Selectors: json.RawMessage(`{}`),
			Thresholds: encodedThresholds, WindowSeconds: int32(rule.Window / time.Second),
			CooldownSeconds: int32(rule.Cooldown / time.Second), ServiceIdentityID: serviceIdentityID,
			ModeCeiling: string(rule.ModeCeiling), CreatedByID: pgtype.UUID{Bytes: actorID, Valid: true},
		}); err != nil {
			return fmt.Errorf("create Charlie default trigger %s: %w", rule.Name, err)
		}
	}
	return nil
}
