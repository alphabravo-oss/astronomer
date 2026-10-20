package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
)

// Each collection section sees one PostgreSQL snapshot, including its count,
// every offset page and member/user reads. Concurrent inserts/deletes cannot
// shift the next page or turn an independent count into a completeness proof.
func complianceReadSnapshotRunner(begin func(context.Context, pgx.TxOptions) (pgx.Tx, error)) func(context.Context, func(handler.ComplianceQuerier) error) error {
	return func(ctx context.Context, fn func(handler.ComplianceQuerier) error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		tx, err := begin(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			return fmt.Errorf("begin compliance read snapshot: %w", err)
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(cleanup)
		}()
		if err := fn(sqlc.New(tx)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit compliance read snapshot: %w", err)
		}
		return nil
	}
}
