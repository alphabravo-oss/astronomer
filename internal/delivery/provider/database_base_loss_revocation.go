package provider

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// RevokeAfterTrustLoss is restricted to this existing constructor's previously
// captured base. It needs neither a refetched catalog nor valid lost material,
// but cannot select a different durable base or mint new authority. A restart
// without the original physical constructor remains a separate recovery case.
func (owner *DatabaseBaseOwner) RevokeAfterTrustLoss(ctx context.Context) (DatabaseBaseReceipt, error) {
	if ctx == nil || owner == nil || owner.pool == nil || owner.projection == nil {
		return DatabaseBaseReceipt{}, errors.New("database loss revocation physical owner unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	loss, err := owner.projection.authority.ObserveTrustLoss(bounded, owner.projection.proof)
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(bounded, "loss observation", err)
	}
	for attempt := 0; attempt < maxSnapshotFinalizeAttempts; attempt++ {
		receipt, err := owner.revokeLostBaseOnce(bounded, loss)
		if err == nil || !retryOwnedSnapshot(err) {
			return receipt, err
		}
	}
	return DatabaseBaseReceipt{}, ErrSnapshotChanged
}

func (owner *DatabaseBaseOwner) revokeLostBaseOnce(ctx context.Context, loss *distribution.ObservedDatabaseTrustLoss) (DatabaseBaseReceipt, error) {
	if err := owner.projection.authority.RevalidateTrustLoss(ctx, loss); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "loss current observation", err)
	}
	connection, err := owner.pool.Acquire(ctx)
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "loss connection", err)
	}
	session := sqlc.New(connection)
	var transaction pgx.Tx
	lockAttempted, committed := false, false
	defer finishOwnedSnapshotSession(connection, session, &transaction, uuid.Nil, &lockAttempted, &committed)
	transaction, err = connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "loss begin", err)
	}
	queries := session.WithTx(transaction)
	if err := queries.LockVulnerabilityDatabaseAuthority(ctx); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "loss authority lock", err)
	}
	current, err := queries.GetCurrentVulnerabilityDatabaseBaseForUpdate(ctx)
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "loss current base", err)
	}
	if !databaseLostBaseMatches(current, owner.projection.snapshot, owner.projection.bindingFingerprint) {
		return DatabaseBaseReceipt{}, errors.New("database loss revocation differs from owned historical base")
	}
	if err := revokeDatabaseBase(ctx, queries, current); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "loss permanent revocation", err)
	}
	if err := owner.projection.authority.RevalidateTrustLoss(ctx, loss); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "loss precommit observation", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "loss commit", err)
	}
	committed = true
	return databaseBaseReceipt(current), nil
}

func databaseLostBaseMatches(current sqlc.VulnerabilityDatabaseBasis, snapshot distribution.DatabaseAuthoritySnapshot, fingerprint string) bool {
	// Idempotent revocation may read an already permanently revoked row. Only
	// that marker is excluded; every captured physical/base binding still matches.
	// The actual revocation helper never clears revoked_at or accepts a feed.
	current.RevokedAt = pgtype.Timestamptz{}
	return databaseProjectionBaseMatches(current, snapshot, fingerprint)
}
