package server

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

type connectionMetricsBegin func(context.Context, pgx.TxOptions) (pgx.Tx, error)

func collectConnectionMetricsSnapshot(ctx context.Context, begin connectionMetricsBegin) ([]sqlc.ListClusterConnectionStatusRow, error) {
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
	var complete []sqlc.ListClusterConnectionStatusRow
	for offset := int32(0); offset <= tunnel.ConnectionMetricsMaxOwners; offset += tunnel.ConnectionMetricsPageSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := queries.ListClusterConnectionStatus(ctx, sqlc.ListClusterConnectionStatusParams{QueryLimit: tunnel.ConnectionMetricsPageSize, QueryOffset: offset})
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		complete, err = tunnel.AppendConnectionMetricsPage(complete, rows)
		if err != nil {
			return nil, err
		}
		if len(rows) < int(tunnel.ConnectionMetricsPageSize) {
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
