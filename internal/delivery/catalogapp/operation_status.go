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
	var state, deletionState string
	var result Status
	if err := q.QueryRow(ctx, `SELECT r.state,r.last_error_code,t.deletion_state,COALESCE(d.last_message,'')
		FROM delivery_rollouts r
		JOIN delivery_targets t ON t.id=r.target_id
		LEFT JOIN LATERAL (
			SELECT last_message FROM cluster_deployments
			WHERE current_rollout_id=r.id ORDER BY updated_at DESC,id DESC LIMIT 1
		) d ON true
		WHERE r.id=$1 AND r.target_id=$2`, rolloutID, targetID).Scan(&state, &result.LastErrorCode, &deletionState, &result.LastMessage); err != nil {
		return Status{}, err
	}
	switch state {
	case "succeeded":
		result.Phase = "ready"
	case "failed", "rejected", "aborted", "rolled_back", "rollback_failed":
		result.Phase = "failed"
	default:
		if deletionState == "deleting" || deletionState == "deleted" {
			result.Phase = "failed"
			if result.LastErrorCode == "" {
				result.LastErrorCode = "target_removed"
			}
		} else if state == "paused" {
			result.Phase = "degraded"
		} else {
			result.Phase = "pending"
		}
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
