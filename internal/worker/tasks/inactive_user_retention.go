package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/platformsettings"
)

// InactiveUserRetentionType identifies the daily human-account inactivity
// sweep. It is leader-gated because the operation spans the installation.
const InactiveUserRetentionType = "users:enforce_inactive_retention"

type inactiveUserRetentionQuerier interface {
	DeactivateInactiveUsers(context.Context, sqlc.DeactivateInactiveUsersParams) ([]sqlc.DeactivateInactiveUsersRow, error)
	GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error)
}

// NewInactiveUserRetentionTask constructs the bounded-retry periodic task.
func NewInactiveUserRetentionTask() *asynq.Task {
	return asynq.NewTask(InactiveUserRetentionType, nil, asynq.MaxRetry(2))
}

// HandleInactiveUserRetention deactivates non-privileged human users whose
// latest successful login (or account creation when they never logged in) is
// older than the configured horizon. The SQL operation also invalidates all
// tokens, removes SSO session material, and records one audit row per user in
// the same transaction.
func HandleInactiveUserRetention(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, InactiveUserRetentionType, func() error {
		q, ok := runtimeDependencies(ctx).Queries.(inactiveUserRetentionQuerier)
		if !ok || q == nil {
			return fmt.Errorf("inactive user retention runtime is not configured")
		}
		days := platformsettings.DefaultInactiveUserRetentionDays
		setting, err := q.GetPlatformSetting(ctx, platformsettings.InactiveUserRetentionKey)
		switch {
		case err == nil:
			days = 0
			if err := json.Unmarshal(setting.Value, &days); err != nil || days < 1 || days > platformsettings.MaxInactiveUserRetentionDays {
				return fmt.Errorf("invalid inactive user retention setting")
			}
		case errors.Is(err, pgx.ErrNoRows):
			// An absent setting uses the same canonical default as the API.
		default:
			return fmt.Errorf("read inactive user retention setting: %w", err)
		}
		now := time.Now().UTC()
		cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
		rows, err := q.DeactivateInactiveUsers(ctx, sqlc.DeactivateInactiveUsersParams{
			InvalidatedAt: now,
			Cutoff:        cutoff,
			RetentionDays: int32(days),
		})
		if err != nil {
			return fmt.Errorf("deactivate inactive users: %w", err)
		}
		if len(rows) > 0 {
			auth.SessionRevocationsTotal.WithLabelValues(observability.MetricValues("user", "inactive_retention")...).Add(float64(len(rows)))
		}
		runtimeLogger(ctx).InfoContext(ctx, "enforced inactive user retention",
			"users_deactivated", len(rows),
			"cutoff", cutoff.Format(time.RFC3339),
			"retention_days", days,
		)
		return nil
	})
}
