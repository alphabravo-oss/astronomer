package deployment

import (
	"context"
	"errors"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type TargetDeletionQueries interface {
	RequestDeliveryTargetDeletionCAS(context.Context, sqlc.RequestDeliveryTargetDeletionCASParams) (sqlc.RequestDeliveryTargetDeletionCASRow, error)
	CountDeliveryRollouts(context.Context, sqlc.CountDeliveryRolloutsParams) (int64, error)
	FinalizeDeliveryTargetDeletionIfComplete(context.Context, uuid.UUID) (sqlc.DeliveryTarget, error)
}

// RequestTargetDeletion requires transaction-bound queries. The initial CAS
// locks the target and prevents new rollout planning. Subsequent statements get
// fresh READ COMMITTED snapshots, including a planner that committed while the
// CAS was waiting for that lock. Only a never-rolled-out target can take this
// immediate path; existing rollout histories retain the acknowledgment path.
func RequestTargetDeletion(ctx context.Context, q TargetDeletionQueries, params sqlc.RequestDeliveryTargetDeletionCASParams) (sqlc.RequestDeliveryTargetDeletionCASRow, error) {
	row, err := q.RequestDeliveryTargetDeletionCAS(ctx, params)
	if err != nil || row.DeploymentCount != 0 {
		return row, err
	}
	count, err := q.CountDeliveryRollouts(ctx, sqlc.CountDeliveryRolloutsParams{
		ProjectID: params.ProjectID, TargetID: pgtype.UUID{Bytes: row.ID, Valid: true},
	})
	if err != nil || count != 0 {
		return row, err
	}
	finalized, err := q.FinalizeDeliveryTargetDeletionIfComplete(ctx, row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, nil
	}
	if err != nil {
		return row, err
	}
	row.DeletionState = finalized.DeletionState
	row.ResourceVersion = finalized.ResourceVersion
	return row, nil
}
