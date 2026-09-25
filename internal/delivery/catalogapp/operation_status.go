package catalogapp

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type operationStatusQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// RolloutStatus reads this operation's immutable rollout, never the target's
// newest deployment, which may already belong to a later upgrade.
func (s *Service) RolloutStatus(ctx context.Context, targetID, rolloutID uuid.UUID) (Status, error) {
	if s == nil || s.pool == nil || targetID == uuid.Nil || rolloutID == uuid.Nil {
		return Status{}, errors.New("catalog rollout identity is required")
	}
	return readRolloutStatus(ctx, s.pool, targetID, rolloutID)
}

func readRolloutStatus(ctx context.Context, q operationStatusQuerier, targetID, rolloutID uuid.UUID) (Status, error) {
	var state string
	var result Status
	if err := q.QueryRow(ctx, `SELECT state,last_error_code FROM delivery_rollouts WHERE id=$1 AND target_id=$2`, rolloutID, targetID).Scan(&state, &result.LastErrorCode); err != nil {
		return Status{}, err
	}
	switch state {
	case "succeeded":
		result.Phase = "ready"
	case "failed", "rejected", "aborted", "rolled_back", "rollback_failed":
		result.Phase = "failed"
	case "paused":
		result.Phase = "degraded"
	default:
		result.Phase = "pending"
	}
	return result, nil
}

// DeletionStatus observes the monotonically deleting target captured by the
// uninstall operation. An active/ready deployment is never removal evidence.
func (s *Service) DeletionStatus(ctx context.Context, targetID uuid.UUID) (Status, error) {
	if s == nil || s.pool == nil || targetID == uuid.Nil {
		return Status{}, errors.New("catalog deletion identity is required")
	}
	return readDeletionStatus(ctx, s.pool, targetID)
}

func readDeletionStatus(ctx context.Context, q operationStatusQuerier, targetID uuid.UUID) (Status, error) {
	var state string
	if err := q.QueryRow(ctx, `SELECT deletion_state FROM delivery_targets WHERE id=$1`, targetID).Scan(&state); err != nil {
		return Status{}, err
	}
	if state == "deleted" {
		return Status{Phase: "removed"}, nil
	}
	return Status{Phase: "pending"}, nil
}
