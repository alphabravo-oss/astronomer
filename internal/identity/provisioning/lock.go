package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// WithRegistrationLock serializes an installation's normal registration,
// recovery, rotation and cleanup across server/worker replicas. Generation is
// deliberately excluded, so rotation cannot race initial registration.
//
// This is mutual exclusion, not a distributed transaction with the issuer. Work
// must persist an operation intent BEFORE its external mutation and retain an
// ambiguous/recovery-required state after a crash or lost connection. A lock
// alone cannot make create-only upstream credentials crash-safe.
//
// The callback must honor its bounded context. Credential writes commit outside
// this transaction and must not be undone when this function returns an error.
func WithRegistrationLock(ctx context.Context, pool *pgxpool.Pool, scope Scope, work func(context.Context) error) error {
	if pool == nil || !scope.valid() || work == nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = tx.Rollback(cleanup)
	}()
	scope.Generation = 0
	key, err := json.Marshal(scope)
	if err != nil {
		return ErrInvalid
	}
	if err := sqlc.New(tx).LockIdentityProvisioning(ctx, string(key)); err != nil {
		return errors.Join(ErrUnavailable, ctx.Err())
	}
	if err := work(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.Join(ErrUnavailable, ctx.Err())
	}
	return nil
}
