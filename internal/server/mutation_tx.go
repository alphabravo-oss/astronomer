package server

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// sqlcMutationTxRunner is the single production transaction adapter for typed
// handler mutation surfaces. Keeping begin/rollback/commit behavior here avoids
// subtle drift between independently wired handlers.
func sqlcMutationTxRunner[T any](database *db.DB) func(context.Context, func(T) error) error {
	return func(ctx context.Context, fn func(T) error) error {
		tx, err := database.Pool().BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("%w: begin mutation transaction: %w", audit.ErrOutboxUnavailable, err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		queries, ok := any(sqlc.New(tx)).(T)
		if !ok {
			return fmt.Errorf("transaction-bound sqlc query set does not satisfy mutation surface")
		}
		if err := fn(queries); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("%w: commit mutation transaction: %w", audit.ErrOutboxUnavailable, err)
		}
		return nil
	}
}
