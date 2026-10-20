package server

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var errRegistrationMetricsBudget = errors.New("registration metrics observation exceeds owner budget")

// ListClusters is the existing canonical non-decommissioned projection. It
// already includes the committed registration phase, so resync adds no SQL.
func collectRegistrationMetricsSnapshot(ctx context.Context, begin connectionMetricsBegin) ([]observability.RegistrationMetricSnapshotRow, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := begin(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	queries := sqlc.New(tx)
	const pageSize int32 = 500
	var complete []observability.RegistrationMetricSnapshotRow
	for offset := int32(0); offset <= observability.MetricOwnerMax; offset += pageSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(rows) > int(pageSize) || len(complete)+len(rows) > observability.MetricOwnerMax {
			return nil, errRegistrationMetricsBudget
		}
		for _, row := range rows {
			complete = append(complete, observability.RegistrationMetricSnapshotRow{ClusterID: row.ID.String(), Phase: row.RegistrationPhase})
		}
		if len(rows) < int(pageSize) {
			break
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return complete, nil
}

// One supervised serial loop; a slow bounded observation never launches an
// overlapping collection. Failed/canceled reads leave the preceding snapshot.
func runRegistrationMetricsCollector(ctx context.Context, begin connectionMetricsBegin, logger zerolog.Logger) {
	collect := func() {
		started := time.Now().UTC()
		watermark := observability.RegistrationMetricsWatermark()
		rows, err := collectRegistrationMetricsSnapshot(ctx, begin)
		if err != nil {
			result := "failed"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				result = "canceled"
			}
			if errors.Is(err, errRegistrationMetricsBudget) {
				result = "owner_budget"
			}
			observability.RecordRegistrationMetricFailure(result)
			logger.Warn().Str("error_kind", "collection_failed").Msg("registration metrics observation incomplete")
			return
		}
		if ctx.Err() != nil {
			observability.RecordRegistrationMetricFailure("canceled")
			return
		}
		observability.PublishRegistrationMetricSnapshot(rows, watermark, started)
	}
	collect()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collect()
		}
	}
}
