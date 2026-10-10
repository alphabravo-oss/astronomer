package provider

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DatabaseRestartRevoker only reduces the exact locked current retained base.
// It cannot create/promote/accept bases or feeds. It does not need a functioning
// Loader, temporary directory, signing mount or registry. Production startup
// must inject protected typed configuration before attempting normal loading.
type DatabaseRestartRevoker struct {
	pool      *pgxpool.Pool
	inspector *distribution.DatabaseRestartInspector
}

func NewDatabaseRestartRevoker(pool *pgxpool.Pool, options distribution.DatabaseRestartInspectionOptions) (*DatabaseRestartRevoker, error) {
	if pool == nil {
		return nil, errors.New("database restart repository unavailable")
	}
	inspector, err := distribution.NewDatabaseRestartInspector(options)
	if err != nil {
		return nil, err
	}
	return &DatabaseRestartRevoker{pool: pool, inspector: inspector}, nil
}

// ErrDatabaseRestartBaseAbsent means the locked singleton has no current base.
// It permits only the independent authenticated initial-base constructor to
// continue. It never represents an I/O, policy, context or trust-loss result.
var ErrDatabaseRestartBaseAbsent = errors.New("database restart current base absent")

// Healthy material returns ErrNoConfirmedDatabaseRestartTrustLoss without a
// write. Unknown source/configuration/I/O/context states fail closed without a
// revocation. Known loss is rechecked before permanent marker/outbox commit.
func (owner *DatabaseRestartRevoker) RevokeLostCurrent(ctx context.Context) (DatabaseBaseReceipt, error) {
	if ctx == nil || owner == nil || owner.pool == nil || owner.inspector == nil {
		return DatabaseBaseReceipt{}, errors.New("database restart revocation owner unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < maxSnapshotFinalizeAttempts; attempt++ {
		receipt, err := owner.revokeOnce(bounded)
		if err == nil || !retryOwnedSnapshot(err) {
			return receipt, err
		}
	}
	return DatabaseBaseReceipt{}, ErrSnapshotChanged
}

func (owner *DatabaseRestartRevoker) revokeOnce(ctx context.Context) (DatabaseBaseReceipt, error) {
	connection, err := owner.pool.Acquire(ctx)
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "restart connection", err)
	}
	session := sqlc.New(connection)
	var transaction pgx.Tx
	lockAttempted, committed := false, false
	defer finishOwnedSnapshotSession(connection, session, &transaction, uuid.Nil, &lockAttempted, &committed)
	transaction, err = connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "restart begin", err)
	}
	queries := session.WithTx(transaction)
	if err := queries.LockVulnerabilityDatabaseAuthority(ctx); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "restart authority lock", err)
	}
	current, err := queries.GetCurrentVulnerabilityDatabaseBaseForUpdate(ctx)
	if errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
		return DatabaseBaseReceipt{}, ErrDatabaseRestartBaseAbsent
	}
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "restart current base", err)
	}
	snapshot, err := retainedDatabaseAuthoritySnapshot(current)
	if err != nil {
		return DatabaseBaseReceipt{}, err
	}
	loss, err := owner.inspector.ObserveLoss(ctx, snapshot)
	if errors.Is(err, distribution.ErrNoConfirmedDatabaseRestartTrustLoss) {
		return DatabaseBaseReceipt{}, distribution.ErrNoConfirmedDatabaseRestartTrustLoss
	}
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "restart loss observation", err)
	}
	if err := revokeDatabaseBase(ctx, queries, current); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "restart permanent revocation", err)
	}
	if err := owner.inspector.RevalidateLoss(ctx, loss); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "restart precommit observation", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "restart commit", err)
	}
	committed = true
	return databaseBaseReceipt(current), nil
}

// The singleton lock selects this actual row. Reconstructing its scalar DATA
// is not an acceptance proof: its complete private binding must match before
// the independently configured inspector can even attempt a loss observation.
func retainedDatabaseAuthoritySnapshot(row sqlc.VulnerabilityDatabaseBasis) (distribution.DatabaseAuthoritySnapshot, error) {
	snapshot := distribution.DatabaseAuthoritySnapshot{
		Base: distribution.DatabaseBaseSnapshot{
			Binding: distribution.DatabaseUpdateBinding{
				BaseRelease: row.BaseRelease, BaseDistributionDigest: row.DistributionDigest, MirrorEndorsementDigest: row.MirrorEndorsementDigest.String,
				CatalogDigest: row.CatalogDigest, Profile: row.Profile, CandidateSourceCommit: row.CandidateSourceCommit,
				Scanner: distribution.DatabaseScannerCompatibility{ImageDigest: row.ScannerDigest, Version: row.ScannerVersion, MainSchema: int(row.MainSchema), JavaSchema: int(row.JavaSchema)},
			},
			PolicyFingerprint: row.PolicyFingerprint, DistributionIdentity: row.DistributionIdentity,
			ChartSourceURL: row.ChartSourceUrl, Chart: row.ChartName, ChartVersion: row.ChartVersion, ChartDigest: row.ChartDigest, ScanningEnabled: row.ScanningEnabled,
		},
		TrustMaterialFingerprint: row.TrustMaterialFingerprint, VendorTrustFingerprint: row.VendorTrustFingerprint, VendorTrustOriginFingerprint: row.VendorTrustOriginFingerprint,
		MirrorPolicyFingerprint: row.MirrorPolicyFingerprint.String, MirrorTrustFingerprint: row.MirrorTrustFingerprint.String, MirrorTrustOriginFingerprint: row.MirrorTrustOriginFingerprint.String,
		RegistryRootsFingerprint: row.RegistryRootsFingerprint.String, RegistryPolicyFingerprint: row.RegistryPolicyFingerprint.String, RegistryRootOriginFingerprint: row.RegistryRootOriginFingerprint.String,
	}
	if !databaseLostBaseMatches(row, snapshot, databaseSnapshotBindingFingerprint(snapshot)) {
		return distribution.DatabaseAuthoritySnapshot{}, errors.New("database restart retained immutable row or version differs")
	}
	return snapshot, nil
}
