package provider

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// DatabasePublicationOwner reserves against the actual current base, and records
// publication only after the existing canonical signed consumer reads it back.
// It does not write/sign a registry artifact or claim any upstream signature.
// The publisher adapter must copy unchanged bytes and endorse them separately.
type DatabasePublicationOwner struct {
	pool         *pgxpool.Pool
	projection   *databaseSnapshotProjection
	repositories distribution.DatabasePublicationRepositories
}

type DatabasePublicationReservation struct {
	owner    *DatabasePublicationOwner
	attempt  sqlc.VulnerabilityDatabaseUpdateAttempt
	prepared *distribution.PreparedDatabasePublication
	document distribution.DatabaseUpdateDocument
}

func NewDatabasePublicationOwner(ctx context.Context, pool *pgxpool.Pool, authority *distribution.DatabaseAuthority, repositories distribution.DatabasePublicationRepositories) (*DatabasePublicationOwner, error) {
	if ctx == nil || pool == nil || repositories.Validate() != nil {
		return nil, errors.New("database publication owner constructor unavailable")
	}
	projection, err := newDatabaseSnapshotProjection(ctx, authority)
	if err != nil {
		return nil, err
	}
	if !projection.snapshot.Base.ScanningEnabled {
		return nil, errors.New("database publication base scanner is disabled")
	}
	return &DatabasePublicationOwner{pool: pool, projection: projection, repositories: repositories}, nil
}

func (owner *DatabasePublicationOwner) Reserve(ctx context.Context, main, java *distribution.VerifiedSupplierDatabasePayload) (*DatabasePublicationReservation, error) {
	if ctx == nil || owner == nil || owner.pool == nil || owner.projection == nil {
		return nil, errors.New("database publication owner unavailable")
	}
	prepared, err := owner.projection.authority.PrepareDatabasePublication(ctx, owner.projection.proof, main, java, owner.repositories, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	var reservation *DatabasePublicationReservation
	err = owner.transaction(ctx, func(ctx context.Context, queries *sqlc.Queries) error {
		base, err := owner.currentBase(ctx, queries)
		if err != nil {
			return err
		}
		if err := prepared.ValidateCurrent(ctx, time.Now().UTC()); err != nil {
			return err
		}
		generation, err := queries.ReserveVulnerabilityDatabaseGeneration(ctx, sqlc.ReserveVulnerabilityDatabaseGenerationParams{BaseID: base.ID, BaseEpoch: base.Epoch})
		if err != nil {
			return err
		}
		document, valid := prepared.Document(generation)
		if !valid {
			return errors.New("database publication generation unavailable")
		}
		decision, err := databaseUpdateDecisionDigest(document)
		if err != nil {
			return err
		}
		attempt, err := queries.CreateVulnerabilityDatabaseUpdateAttempt(ctx, sqlc.CreateVulnerabilityDatabaseUpdateAttemptParams{ID: uuid.New(), BaseID: base.ID, BaseEpoch: base.Epoch, Generation: generation, DecisionDigest: decision, ExpiresAt: time.Now().UTC().Add(45 * time.Minute)})
		if err != nil {
			return err
		}
		if !databasePublicationAttemptMatches(attempt, base, document, decision) || attempt.Phase != "reserved" || attempt.PublishedFeedDigest.Valid {
			return errors.New("database publication created reservation differs")
		}
		if err := prepared.ValidateCurrent(ctx, time.Now().UTC()); err != nil {
			return err
		}
		reservation = &DatabasePublicationReservation{owner: owner, attempt: attempt, prepared: prepared, document: document}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reservation, nil
}

// Snapshot exposes exact immutable signing DATA and the actual reservation ID.
// This cannot create/advance a phase. Only owner.PublishVerified can do that.
func (reservation *DatabasePublicationReservation) Snapshot() (uuid.UUID, distribution.DatabaseUpdateDocument, bool) {
	if reservation == nil || reservation.owner == nil || reservation.prepared == nil || reservation.attempt.ID == uuid.Nil {
		return uuid.Nil, distribution.DatabaseUpdateDocument{}, false
	}
	document := reservation.document
	document.Databases = slices.Clone(document.Databases)
	return reservation.attempt.ID, document, true
}

// transaction is private constructor composition. The callback never comes
// from an HTTP caller. Authority changes use the existing global lock and same
// finite rollback/hijack-close path; no per-cluster lock or assignment is used.
func (owner *DatabasePublicationOwner) transaction(ctx context.Context, action func(context.Context, *sqlc.Queries) error) error {
	if ctx == nil || owner == nil || owner.pool == nil || action == nil {
		return errors.New("database publication transaction unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < maxSnapshotFinalizeAttempts; attempt++ {
		err := owner.transactionOnce(bounded, action)
		if err == nil || !retryOwnedSnapshot(err) {
			return err
		}
	}
	return ErrSnapshotChanged
}

func (owner *DatabasePublicationOwner) transactionOnce(ctx context.Context, action func(context.Context, *sqlc.Queries) error) error {
	connection, err := owner.pool.Acquire(ctx)
	if err != nil {
		return safeOwnedSnapshotError(ctx, "publication connection", err)
	}
	session := sqlc.New(connection)
	var transaction pgx.Tx
	lockAttempted, committed := false, false
	defer finishOwnedSnapshotSession(connection, session, &transaction, uuid.Nil, &lockAttempted, &committed)
	transaction, err = connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return safeOwnedSnapshotError(ctx, "publication begin", err)
	}
	queries := session.WithTx(transaction)
	if err := queries.LockVulnerabilityDatabaseAuthority(ctx); err != nil {
		return safeOwnedSnapshotError(ctx, "publication authority lock", err)
	}
	if err := action(ctx, queries); err != nil {
		return safeOwnedSnapshotError(ctx, "publication transition", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return safeOwnedSnapshotError(ctx, "publication commit", err)
	}
	committed = true
	return nil
}

func (owner *DatabasePublicationOwner) currentBase(ctx context.Context, queries *sqlc.Queries) (sqlc.VulnerabilityDatabaseBasis, error) {
	if owner == nil || owner.projection == nil || queries == nil {
		return sqlc.VulnerabilityDatabaseBasis{}, errors.New("database publication current authority unavailable")
	}
	if err := owner.projection.revalidateOwnedSnapshot(ctx); err != nil {
		return sqlc.VulnerabilityDatabaseBasis{}, err
	}
	base, err := queries.GetCurrentVulnerabilityDatabaseBaseForUpdate(ctx)
	if err != nil {
		return sqlc.VulnerabilityDatabaseBasis{}, err
	}
	if !databaseProjectionBaseMatches(base, owner.projection.snapshot, owner.projection.bindingFingerprint) || !base.ScanningEnabled {
		return sqlc.VulnerabilityDatabaseBasis{}, errors.New("database publication current base changed or revoked")
	}
	return base, nil
}

func databasePublicationAttemptMatches(attempt sqlc.VulnerabilityDatabaseUpdateAttempt, base sqlc.VulnerabilityDatabaseBasis, document distribution.DatabaseUpdateDocument, decision string) bool {
	return attempt.ID != uuid.Nil && base.ID != uuid.Nil && !base.RevokedAt.Valid && base.ScanningEnabled && attempt.BaseID == base.ID && attempt.BaseEpoch == base.Epoch && attempt.Generation == document.Generation && attempt.DecisionDigest == decision && databasePublicationBindingMatchesBase(document.Binding, base)
}

func databasePublicationDocumentMatches(actual, expected distribution.DatabaseUpdateDocument) bool {
	return reflect.DeepEqual(actual, expected)
}

func databasePublicationBindingMatchesBase(binding distribution.DatabaseUpdateBinding, base sqlc.VulnerabilityDatabaseBasis) bool {
	return binding.BaseRelease == base.BaseRelease && binding.BaseDistributionDigest == base.DistributionDigest && databaseProjectionOptional(binding.MirrorEndorsementDigest) == base.MirrorEndorsementDigest && binding.CatalogDigest == base.CatalogDigest && binding.Profile == base.Profile && binding.CandidateSourceCommit == base.CandidateSourceCommit && binding.Scanner.ImageDigest == base.ScannerDigest && binding.Scanner.Version == base.ScannerVersion && int64(binding.Scanner.MainSchema) == int64(base.MainSchema) && int64(binding.Scanner.JavaSchema) == int64(base.JavaSchema)
}

func databasePublicationReservationMatches(actual, reserved sqlc.VulnerabilityDatabaseUpdateAttempt) bool {
	return actual.ID != uuid.Nil && actual.ID == reserved.ID && actual.BaseID == reserved.BaseID && actual.BaseEpoch == reserved.BaseEpoch && actual.Generation == reserved.Generation && actual.DecisionDigest == reserved.DecisionDigest && actual.CreatedAt.Equal(reserved.CreatedAt) && actual.ExpiresAt.Equal(reserved.ExpiresAt)
}
