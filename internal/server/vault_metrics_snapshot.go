package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/jackc/pgx/v5"
)

var errVaultMetricsBudget = errors.New("Vault metrics observation exceeds owner budget")

// Use the canonical credential-free list projection and discard its other
// fields page by page. No auth blob is returned, exported or retained for metrics.
func collectVaultMetricsSnapshot(ctx context.Context, begin connectionMetricsBegin) ([]observability.VaultMetricSnapshotRow, error) {
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
	var complete []observability.VaultMetricSnapshotRow
	for offset := int32(0); offset <= observability.MetricOwnerMax; offset += pageSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := queries.ListVaultConnectionsPage(ctx, sqlc.ListVaultConnectionsPageParams{QueryLimit: pageSize, QueryOffset: offset})
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(rows) > int(pageSize) || len(complete)+len(rows) > observability.MetricOwnerMax {
			return nil, errVaultMetricsBudget
		}
		for _, row := range rows {
			complete = append(complete, observability.VaultMetricSnapshotRow{ID: row.ID, Name: row.Name})
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

func runVaultMetricsCollector(ctx context.Context, begin connectionMetricsBegin, logger *slog.Logger) {
	collect := func() {
		started := time.Now().UTC()
		watermark := observability.VaultMetricsWatermark()
		rows, err := collectVaultMetricsSnapshot(ctx, begin)
		if err != nil {
			result := "failed"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				result = "canceled"
			}
			if errors.Is(err, errVaultMetricsBudget) {
				result = "owner_budget"
			}
			observability.RecordVaultMetricFailure(result)
			if logger != nil {
				logger.Warn("Vault metrics observation incomplete", "error_kind", "collection_failed")
			}
			return
		}
		if ctx.Err() != nil {
			observability.RecordVaultMetricFailure("canceled")
			return
		}
		if !observability.RetireVaultMetrics(rows, watermark) {
			observability.RecordVaultMetricFailure("invalid")
			return
		}
		observability.RecordVaultMetricComplete(started)
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
