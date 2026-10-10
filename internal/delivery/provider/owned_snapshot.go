package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Only the private database authority repository implements this participant.
// prepare locks actual base/source/target authority and returns its owned
// decision closure; no request can supply transaction, ETag or authority bits.
type ownedSnapshotProjection interface {
	prepareOwnedSnapshot(context.Context, *sqlc.Queries, uuid.UUID) (ownedSnapshotRefresh, error)
	revalidateOwnedSnapshot(context.Context) error
}

type ownedSnapshotRefresh func(context.Context, *sqlc.Queries, sqlc.DeliveryAssignmentReceipt, protocol.DeliveryStateResponseV2) error

type ownedSnapshotCoordinator struct {
	pool       *pgxpool.Pool
	prototype  *Provider
	projection ownedSnapshotProjection
}

// Private constructor: the owning repository supplies opaque physical authority
// and projection logic. There is no exported callback/boolean approval API.
func newOwnedSnapshotCoordinator(pool *pgxpool.Pool, original *Provider, projection ownedSnapshotProjection) (*Provider, error) {
	if pool == nil || original == nil || original.queries == nil || projection == nil || original.ownedSnapshots != nil {
		return nil, errors.New("owned delivery snapshot constructor invalid")
	}
	copy := *original
	copy.builtinCA = bytes.Clone(original.builtinCA)
	copy.builtinSigningKey = bytes.Clone(original.builtinSigningKey)
	prototype := copy
	copy.ownedSnapshots = &ownedSnapshotCoordinator{pool: pool, prototype: &prototype, projection: projection}
	return &copy, nil
}

func (coordinator *ownedSnapshotCoordinator) snapshot(ctx context.Context, cluster uuid.UUID, request protocol.DeliveryStateRequestV2) (protocol.DeliveryStateResponseV2, error) {
	for attempt := 0; attempt < maxSnapshotFinalizeAttempts; attempt++ {
		response, err := coordinator.snapshotTransaction(ctx, cluster, request)
		if err == nil || !retryOwnedSnapshot(err) {
			return response, err
		}
	}
	return protocol.DeliveryStateResponseV2{}, ErrSnapshotChanged
}

func (coordinator *ownedSnapshotCoordinator) snapshotTransaction(ctx context.Context, cluster uuid.UUID, request protocol.DeliveryStateRequestV2) (protocol.DeliveryStateResponseV2, error) {
	if ctx == nil {
		return protocol.DeliveryStateResponseV2{}, errors.New("owned delivery snapshot context unavailable")
	}
	if err := coordinator.projection.revalidateOwnedSnapshot(ctx); err != nil {
		return protocol.DeliveryStateResponseV2{}, safeOwnedSnapshotError(ctx, "physical authority", err)
	}
	connection, err := coordinator.pool.Acquire(ctx)
	if err != nil {
		return protocol.DeliveryStateResponseV2{}, safeOwnedSnapshotError(ctx, "connection", err)
	}
	session := sqlc.New(connection)
	var transaction pgx.Tx
	lockAttempted, committed := false, false
	defer finishOwnedSnapshotSession(connection, session, &transaction, cluster, &lockAttempted, &committed)
	// PostgreSQL must not establish a serializable snapshot while queued behind
	// the builtin owner. All configured provider requests use this same lane.
	lockAttempted = true
	if err := session.LockBuiltinAssetsSession(ctx, cluster.String()); err != nil {
		return protocol.DeliveryStateResponseV2{}, safeOwnedSnapshotError(ctx, "session lock", err)
	}
	transaction, err = connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return protocol.DeliveryStateResponseV2{}, safeOwnedSnapshotError(ctx, "begin", err)
	}
	queries := session.WithTx(transaction)
	refresh, err := coordinator.projection.prepareOwnedSnapshot(ctx, queries, cluster)
	if err != nil || refresh == nil {
		return protocol.DeliveryStateResponseV2{}, safeOwnedSnapshotError(ctx, "relational authority", err)
	}
	provider := *coordinator.prototype
	provider.queries = queries
	response, receipt, err := provider.finalizeSnapshot(ctx, cluster, request)
	if err != nil {
		return protocol.DeliveryStateResponseV2{}, safeOwnedSnapshotError(ctx, "canonical finalization", err)
	}
	if receipt.ClusterID != cluster || receipt.DesiredSnapshotGeneration != response.SnapshotGeneration || receipt.DesiredSnapshotEtag != response.ETag || receipt.CredentialEpoch != response.CredentialEpoch {
		return protocol.DeliveryStateResponseV2{}, errors.New("owned canonical delivery receipt differs")
	}
	if err := refresh(ctx, queries, receipt, response); err != nil {
		return protocol.DeliveryStateResponseV2{}, safeOwnedSnapshotError(ctx, "projection", err)
	}
	if err := coordinator.projection.revalidateOwnedSnapshot(ctx); err != nil {
		return protocol.DeliveryStateResponseV2{}, safeOwnedSnapshotError(ctx, "precommit physical authority", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return protocol.DeliveryStateResponseV2{}, safeOwnedSnapshotError(ctx, "commit", err)
	}
	committed = true
	return response, nil
}

// Fresh bounded cleanup context survives caller cancellation. Unknown lock
// state or failed rollback/unlock closes this exact hijacked connection instead
// of returning a potentially locked session to the shared pool. This is local
// cleanup, not proof that a PostgreSQL backend has already exited; actual DB
// cancellation tests must observe release and unchanged rows separately.
func finishOwnedSnapshotSession(connection *pgxpool.Conn, session *sqlc.Queries, transaction *pgx.Tx, cluster uuid.UUID, lockAttempted, committed *bool) {
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	valid := true
	if *transaction != nil && !*committed {
		err := (*transaction).Rollback(cleanup)
		valid = err == nil || errors.Is(err, pgx.ErrTxClosed)
	}
	if *lockAttempted && valid {
		unlocked, err := session.UnlockBuiltinAssetsSession(cleanup, cluster.String())
		valid = err == nil && unlocked
	}
	if valid {
		connection.Release()
		return
	}
	raw := connection.Hijack()
	_ = raw.Close(cleanup)
}

type ownedSnapshotFailure struct {
	stage string
	code  string
	cause error
}

func (failure *ownedSnapshotFailure) Error() string {
	return fmt.Sprintf("owned delivery snapshot %s failed", failure.stage)
}
func (failure *ownedSnapshotFailure) Unwrap() error { return failure.cause }

func safeOwnedSnapshotError(ctx context.Context, stage string, original error) error {
	failure := &ownedSnapshotFailure{stage: stage}
	if ctx.Err() != nil {
		failure.cause = ctx.Err()
	} else if errors.Is(original, context.Canceled) {
		failure.cause = context.Canceled
	} else if errors.Is(original, context.DeadlineExceeded) {
		failure.cause = context.DeadlineExceeded
	} else if errors.Is(original, ErrSnapshotChanged) {
		failure.cause = ErrSnapshotChanged
	}
	var database *pgconn.PgError
	if failure.cause == nil && errors.As(original, &database) {
		failure.code = database.Code
	}
	return failure
}

func retryOwnedSnapshot(err error) bool {
	var failure *ownedSnapshotFailure
	return errors.As(err, &failure) && (failure.code == "40001" || failure.code == "40P01")
}
