package catalogapp

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type operationStatusQuerier interface {
	GetCatalogApplicationRolloutStatus(context.Context, sqlc.GetCatalogApplicationRolloutStatusParams) (sqlc.GetCatalogApplicationRolloutStatusRow, error)
	GetCatalogApplicationDeletionStatus(context.Context, uuid.UUID) (string, error)
}

// RolloutStatus reads this operation's immutable rollout, never the target's
// newest deployment, which may already belong to a later upgrade.
func (s *Service) RolloutStatus(ctx context.Context, targetID, rolloutID uuid.UUID) (Status, error) {
	if s == nil || s.pool == nil || targetID == uuid.Nil || rolloutID == uuid.Nil {
		return Status{}, errors.New("catalog rollout identity is required")
	}
	return readRolloutStatus(ctx, sqlc.New(s.pool), targetID, rolloutID)
}

func readRolloutStatus(ctx context.Context, q operationStatusQuerier, targetID, rolloutID uuid.UUID) (Status, error) {
	row, err := q.GetCatalogApplicationRolloutStatus(ctx, sqlc.GetCatalogApplicationRolloutStatusParams{ID: rolloutID, TargetID: targetID})
	if err != nil {
		return Status{}, err
	}
	result := Status{LastErrorCode: row.LastErrorCode, LastMessage: row.LastMessage}
	switch row.State {
	case "succeeded":
		result.Phase = "ready"
	case "failed", "rejected", "aborted", "rolled_back", "rollback_failed":
		result.Phase = "failed"
	default:
		if row.DeletionState == "deleting" || row.DeletionState == "deleted" {
			result.Phase = "failed"
			if result.LastErrorCode == "" {
				result.LastErrorCode = "target_removed"
			}
		} else if row.State == "paused" {
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
	return readDeletionStatus(ctx, sqlc.New(s.pool), targetID)
}

func readDeletionStatus(ctx context.Context, q operationStatusQuerier, targetID uuid.UUID) (Status, error) {
	state, err := q.GetCatalogApplicationDeletionStatus(ctx, targetID)
	if err != nil {
		return Status{}, err
	}
	if state == "deleted" {
		return Status{Phase: "removed"}, nil
	}
	return Status{Phase: "pending"}, nil
}
