package provider

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// DatabaseBaseOwner is optional internal composition, not an API approval or
// caller-supplied snapshot. It captures the same opaque physical constructor
// used by the provider. Initialize must run before wrapping that provider;
// ordinary startup never automatically promotes or resurrects a durable base.
type DatabaseBaseOwner struct {
	pool       *pgxpool.Pool
	projection *databaseSnapshotProjection
}

type DatabaseBaseReceipt struct {
	ID    uuid.UUID
	Epoch int64
}

func NewDatabaseBaseOwner(ctx context.Context, pool *pgxpool.Pool, authority *distribution.DatabaseAuthority) (*DatabaseBaseOwner, error) {
	if pool == nil {
		return nil, errors.New("database base repository unavailable")
	}
	projection, err := newDatabaseSnapshotProjection(ctx, authority)
	if err != nil {
		return nil, err
	}
	return &DatabaseBaseOwner{pool: pool, projection: projection}, nil
}

func (owner *DatabaseBaseOwner) Initialize(ctx context.Context) (DatabaseBaseReceipt, error) {
	return owner.transition(ctx, databaseBaseInitialize)
}

// Promote is an explicit owning composition operation after capturing a NEW
// canonical authenticated base. No supplied ID, epoch, boolean or raw catalog
// can select it. This is not publication, rollout, or scan-used evidence.
func (owner *DatabaseBaseOwner) Promote(ctx context.Context) (DatabaseBaseReceipt, error) {
	return owner.transition(ctx, databaseBasePromote)
}

func (owner *DatabaseBaseOwner) Revoke(ctx context.Context) (DatabaseBaseReceipt, error) {
	return owner.transition(ctx, databaseBaseRevoke)
}

type databaseBaseOperation uint8

const (
	databaseBaseInitialize databaseBaseOperation = iota
	databaseBasePromote
	databaseBaseRevoke
)

func (owner *DatabaseBaseOwner) transition(ctx context.Context, operation databaseBaseOperation) (DatabaseBaseReceipt, error) {
	if ctx == nil || owner == nil || owner.pool == nil || owner.projection == nil {
		return DatabaseBaseReceipt{}, errors.New("database base physical owner unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < maxSnapshotFinalizeAttempts; attempt++ {
		receipt, err := owner.transitionOnce(bounded, operation)
		if err == nil || !retryOwnedSnapshot(err) {
			return receipt, err
		}
	}
	return DatabaseBaseReceipt{}, ErrSnapshotChanged
}

func (owner *DatabaseBaseOwner) transitionOnce(ctx context.Context, operation databaseBaseOperation) (DatabaseBaseReceipt, error) {
	if _, err := owner.projection.authority.Catalog(ctx, owner.projection.proof); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "base physical authority", err)
	}
	connection, err := owner.pool.Acquire(ctx)
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "base connection", err)
	}
	session := sqlc.New(connection)
	var transaction pgx.Tx
	lockAttempted, committed := false, false
	// Reuse the canonical finite rollback/hijack-close path. There is no
	// cluster session lock here: this owner changes no delivery assignment.
	// It must never acquire cluster locks after the global authority lock.
	defer finishOwnedSnapshotSession(connection, session, &transaction, uuid.Nil, &lockAttempted, &committed)
	transaction, err = connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "base begin", err)
	}
	queries := session.WithTx(transaction)
	if err := queries.LockVulnerabilityDatabaseAuthority(ctx); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "base authority lock", err)
	}
	receipt, err := owner.lockedTransition(ctx, queries, operation)
	if err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "base transition", err)
	}
	if err := owner.projection.revalidateOwnedSnapshot(ctx); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "base precommit physical authority", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return DatabaseBaseReceipt{}, safeOwnedSnapshotError(ctx, "base commit", err)
	}
	committed = true
	return receipt, nil
}

func (owner *DatabaseBaseOwner) lockedTransition(ctx context.Context, queries *sqlc.Queries, operation databaseBaseOperation) (DatabaseBaseReceipt, error) {
	current, err := queries.GetCurrentVulnerabilityDatabaseBaseForUpdate(ctx)
	if errors.Is(err, pgx.ErrNoRows) && operation == databaseBaseInitialize {
		created, err := owner.createBase(ctx, queries, 1)
		if err != nil {
			return DatabaseBaseReceipt{}, err
		}
		rows, err := queries.InitializeVulnerabilityDatabaseAuthority(ctx, sqlc.InitializeVulnerabilityDatabaseAuthorityParams{BaseID: created.ID, BaseEpoch: created.Epoch})
		if err != nil {
			return DatabaseBaseReceipt{}, err
		}
		if rows != 1 {
			return DatabaseBaseReceipt{}, errors.New("database base initialization lost authority")
		}
		return databaseBaseReceipt(created), nil
	}
	if err != nil {
		return DatabaseBaseReceipt{}, err
	}
	matches := databaseProjectionBaseMatches(current, owner.projection.snapshot, owner.projection.bindingFingerprint)
	if operation == databaseBaseInitialize {
		if !matches {
			return DatabaseBaseReceipt{}, errors.New("database base startup differs or is revoked")
		}
		return databaseBaseReceipt(current), nil
	}
	if operation == databaseBaseRevoke {
		if !matches {
			return DatabaseBaseReceipt{}, errors.New("database base revocation does not match owned current base")
		}
		if err := revokeDatabaseBase(ctx, queries, current); err != nil {
			return DatabaseBaseReceipt{}, err
		}
		return databaseBaseReceipt(current), nil
	}
	if operation != databaseBasePromote || matches {
		return DatabaseBaseReceipt{}, errors.New("database base promotion requires distinct owned base")
	}
	history, err := queries.GetVulnerabilityDatabaseBaseHistory(ctx, owner.projection.snapshot.Base.Binding.BaseDistributionDigest)
	if err != nil {
		return DatabaseBaseReceipt{}, err
	}
	if err := validateDatabaseBasePromotion(current, owner.projection.snapshot, owner.projection.bindingFingerprint, history); err != nil {
		return DatabaseBaseReceipt{}, err
	}
	created, err := owner.createBase(ctx, queries, current.Epoch+1)
	if err != nil {
		return DatabaseBaseReceipt{}, err
	}
	s := owner.projection.snapshot
	rows, err := queries.PromoteVulnerabilityDatabaseAuthority(ctx, sqlc.PromoteVulnerabilityDatabaseAuthorityParams{
		PreviousBaseID: current.ID, PreviousBaseEpoch: current.Epoch, CandidateBaseID: created.ID,
		ExpectedBindingFingerprint: owner.projection.bindingFingerprint, ExpectedPolicyFingerprint: s.Base.PolicyFingerprint,
		ExpectedTrustMaterialFingerprint: s.TrustMaterialFingerprint, ExpectedDistributionIdentity: s.Base.DistributionIdentity,
		ExpectedMirrorEndorsementDigest:      databaseProjectionOptional(s.Base.Binding.MirrorEndorsementDigest),
		ExpectedMirrorTrustFingerprint:       databaseProjectionOptional(s.MirrorTrustFingerprint),
		ExpectedMirrorTrustOriginFingerprint: databaseProjectionOptional(s.MirrorTrustOriginFingerprint),
	})
	if err != nil {
		return DatabaseBaseReceipt{}, err
	}
	if rows != 1 {
		return DatabaseBaseReceipt{}, errors.New("database base promotion lost authority")
	}
	if err := revokeDatabaseBase(ctx, queries, current); err != nil {
		return DatabaseBaseReceipt{}, err
	}
	return databaseBaseReceipt(created), nil
}

func (owner *DatabaseBaseOwner) createBase(ctx context.Context, queries *sqlc.Queries, epoch int64) (sqlc.VulnerabilityDatabaseBasis, error) {
	s := owner.projection.snapshot
	b, binding := s.Base, s.Base.Binding
	return queries.CreateVulnerabilityDatabaseBase(ctx, sqlc.CreateVulnerabilityDatabaseBaseParams{
		ID: uuid.New(), Epoch: epoch, BaseRelease: binding.BaseRelease,
		DistributionDigest: binding.BaseDistributionDigest, DistributionIdentity: b.DistributionIdentity,
		MirrorEndorsementDigest:      databaseProjectionOptional(binding.MirrorEndorsementDigest),
		MirrorPolicyFingerprint:      databaseProjectionOptional(s.MirrorPolicyFingerprint),
		MirrorTrustFingerprint:       databaseProjectionOptional(s.MirrorTrustFingerprint),
		MirrorTrustOriginFingerprint: databaseProjectionOptional(s.MirrorTrustOriginFingerprint),
		CatalogDigest:                binding.CatalogDigest, Profile: binding.Profile, CandidateSourceCommit: binding.CandidateSourceCommit,
		ScannerDigest: binding.Scanner.ImageDigest, ScannerVersion: binding.Scanner.Version,
		MainSchema: safeconv.Int32(binding.Scanner.MainSchema), JavaSchema: safeconv.Int32(binding.Scanner.JavaSchema),
		PolicyFingerprint: b.PolicyFingerprint, TrustMaterialFingerprint: s.TrustMaterialFingerprint,
		VendorTrustFingerprint: s.VendorTrustFingerprint, VendorTrustOriginFingerprint: s.VendorTrustOriginFingerprint,
		RegistryRootsFingerprint: databaseProjectionOptional(s.RegistryRootsFingerprint), RegistryPolicyFingerprint: databaseProjectionOptional(s.RegistryPolicyFingerprint), RegistryRootOriginFingerprint: databaseProjectionOptional(s.RegistryRootOriginFingerprint), ScanningEnabled: b.ScanningEnabled,
		ChartSourceUrl: b.ChartSourceURL, ChartName: b.Chart, ChartVersion: b.ChartVersion, ChartDigest: b.ChartDigest,
		BindingFingerprint: owner.projection.bindingFingerprint,
	})
}

func revokeDatabaseBase(ctx context.Context, queries *sqlc.Queries, current sqlc.VulnerabilityDatabaseBasis) error {
	if !current.RevokedAt.Valid {
		rows, err := queries.RevokeVulnerabilityDatabaseBase(ctx, sqlc.RevokeVulnerabilityDatabaseBaseParams{ID: current.ID, Epoch: current.Epoch})
		if err != nil {
			return err
		}
		if rows != 1 {
			return errors.New("database base revocation lost authority")
		}
	}
	// Existing historical projections remain intact. The outbox is an
	// invalidation/rescan intent only; its consumer must revalidate current
	// base/source/target before any job and cannot fabricate use provenance.
	return queries.QueueVulnerabilityDatabaseBaseRevocationIntents(ctx, current.ID)
}

func databaseBaseReceipt(base sqlc.VulnerabilityDatabaseBasis) DatabaseBaseReceipt {
	return DatabaseBaseReceipt{ID: base.ID, Epoch: base.Epoch}
}

func validateDatabaseBasePromotion(current sqlc.VulnerabilityDatabaseBasis, candidate distribution.DatabaseAuthoritySnapshot, fingerprint string, history []sqlc.VulnerabilityDatabaseBasis) error {
	if current.Epoch <= 0 || current.Epoch == math.MaxInt64 || current.BindingFingerprint == fingerprint {
		return errors.New("database base promotion epoch or binding invalid")
	}
	previous, err := semver.StrictNewVersion(strings.TrimPrefix(current.BaseRelease, "v"))
	if err != nil {
		return errors.New("database base previous version invalid")
	}
	next, err := semver.StrictNewVersion(strings.TrimPrefix(candidate.Base.Binding.BaseRelease, "v"))
	if err != nil || next.LessThan(previous) {
		return errors.New("database base promotion version regresses")
	}
	if len(history) > 0 && (current.RevokedAt.Valid || current.DistributionDigest != candidate.Base.Binding.BaseDistributionDigest) {
		// Only the current ACTIVE parent may retain its content through a
		// newly captured explicit trust epoch. Returning to any superseded
		// parent is forbidden, even with a different physical fingerprint.
		return errors.New("database base promotion replays historical parent")
	}
	for _, base := range history {
		// Historical base objects are never reused. The unique binding index
		// also prevents restoring an older trust fingerprint for this parent.
		if base.BindingFingerprint == fingerprint {
			return errors.New("database base promotion replays historical trust binding")
		}
	}
	return nil
}
