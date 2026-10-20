package provider

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// DatabaseUpdateSource is operator-owned locator DATA. No field selects a
// verifier, signing identity, TLS roots, network permission or approval.
// AttemptID must identify the owning producer's actual published reservation.
type DatabaseUpdateSource struct {
	Repository      string
	Digest          string
	AttemptID       uuid.UUID
	MainPayloadPath string
	JavaPayloadPath string
}

// DatabaseUpdateAcceptor uses the existing canonical finalizer and an actual
// Serializable transaction. It is optional internal composition, not a public
// approval endpoint, production startup wiring or scan-used witness.
type DatabaseUpdateAcceptor struct {
	pool       *pgxpool.Pool
	prototype  *Provider
	projection *databaseSnapshotProjection
	source     DatabaseUpdateSource
}

func NewDatabaseUpdateAcceptor(ctx context.Context, pool *pgxpool.Pool, original *Provider, authority *distribution.DatabaseAuthority, source DatabaseUpdateSource) (*DatabaseUpdateAcceptor, error) {
	if ctx == nil || pool == nil || original == nil || original.queries == nil || original.ownedSnapshots != nil || source.AttemptID == uuid.Nil || source.Repository == "" || source.Digest == "" || !filepath.IsAbs(source.MainPayloadPath) || !filepath.IsAbs(source.JavaPayloadPath) {
		return nil, errors.New("database update acceptor constructor unavailable")
	}
	projection, err := newDatabaseSnapshotProjection(ctx, authority)
	if err != nil {
		return nil, err
	}
	if roots := projection.snapshot.RegistryRootsFingerprint; roots != "" && (original.builtinCAIdentity != roots || original.authorizeBuiltinCA == nil) {
		return nil, errors.New("database update provider TLS differs from physical authority")
	}
	copy := *original
	copy.builtinCA = bytes.Clone(original.builtinCA)
	copy.builtinSigningKey = bytes.Clone(original.builtinSigningKey)
	return &DatabaseUpdateAcceptor{pool: pool, prototype: &copy, projection: projection, source: source}, nil
}

// Accept is invoked by the internal refresh owner for its authenticated cluster.
// It authenticates the immutable feed and both child subjects, then validates
// complete local payloads before any database transaction/mutation. The actual
// producer reservation, physical/base/scanner/assignment authority and monotonic
// projection are checked inside the same canonical finalization transaction.
func (acceptor *DatabaseUpdateAcceptor) Accept(ctx context.Context, cluster uuid.UUID) (protocol.DeliveryStateResponseV2, error) {
	if ctx == nil || cluster == uuid.Nil || acceptor == nil || acceptor.pool == nil || acceptor.prototype == nil || acceptor.projection == nil {
		return protocol.DeliveryStateResponseV2{}, errors.New("database update acceptor owner unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Minute)
	defer cancel()
	captured, err := acceptor.capture(ctx)
	if err != nil {
		return protocol.DeliveryStateResponseV2{}, safeOwnedSnapshotError(ctx, "database update authentication", err)
	}
	participant := &databaseAcceptanceParticipant{projection: acceptor.projection, attempt: acceptor.source.AttemptID, captured: captured}
	provider, err := newOwnedSnapshotCoordinator(acceptor.pool, acceptor.prototype, participant)
	if err != nil {
		return protocol.DeliveryStateResponseV2{}, err
	}
	transactionContext, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	// An empty request forces full canonical assignment materialization. Receipt
	// fields never come from a caller ETag or reconstructed assignment hash.
	return provider.ownedSnapshots.snapshot(transactionContext, cluster, protocol.DeliveryStateRequestV2{})
}

type databaseAcceptanceCapture struct {
	authenticated *distribution.AuthenticatedDatabaseUpdate
	feed          string
	document      distribution.DatabaseUpdateDocument
	main, java    distribution.DatabaseUpdateSnapshot
}

func (acceptor *DatabaseUpdateAcceptor) capture(ctx context.Context) (databaseAcceptanceCapture, error) {
	projection, source := acceptor.projection, acceptor.source
	update, err := projection.authority.LoadUpdate(ctx, projection.proof, source.Repository, source.Digest, time.Now().UTC())
	if err != nil {
		return databaseAcceptanceCapture{}, err
	}
	feed, document, valid := update.Snapshot()
	if !valid {
		return databaseAcceptanceCapture{}, errors.New("database update authenticated feed unavailable")
	}
	captured := databaseAcceptanceCapture{feed: feed, document: document}
	for _, item := range []struct{ role, path string }{{"trivy-database", source.MainPayloadPath}, {"trivy-java-database", source.JavaPayloadPath}} {
		proof, err := distribution.InspectDatabaseUpdatePayload(ctx, update, item.role, item.path, distribution.CadenceDatabaseUpdateTiming(), time.Now().UTC())
		if err != nil {
			return databaseAcceptanceCapture{}, err
		}
		proofFeed, snapshot, valid := proof.Snapshot()
		if !valid || proofFeed != feed || snapshot.Role != item.role {
			return databaseAcceptanceCapture{}, errors.New("database update payload proof differs")
		}
		if item.role == "trivy-database" {
			captured.main = snapshot
		} else {
			captured.java = snapshot
		}
	}
	// The canonical loader's finite freshness and revocation policy is evaluated
	// again after both potentially large inspections, before taking a transaction.
	current, err := projection.authority.LoadUpdate(ctx, projection.proof, source.Repository, source.Digest, time.Now().UTC())
	if err != nil {
		return databaseAcceptanceCapture{}, err
	}
	currentFeed, currentDocument, valid := current.Snapshot()
	if !valid || currentFeed != feed || !reflect.DeepEqual(currentDocument, document) {
		return databaseAcceptanceCapture{}, errors.New("database update changed after full payload inspection")
	}
	captured.authenticated = current
	return captured, nil
}

type databaseAcceptanceParticipant struct {
	projection *databaseSnapshotProjection
	attempt    uuid.UUID
	captured   databaseAcceptanceCapture
	refresh    *distribution.DatabaseRefreshPayloads
	offline    *distribution.VerifiedOfflineDatabaseUpdate
}

func (participant *databaseAcceptanceParticipant) revalidateOwnedSnapshot(ctx context.Context) error {
	if participant == nil || participant.projection == nil {
		return errors.New("database acceptance physical owner unavailable")
	}
	if err := participant.projection.revalidateOwnedSnapshot(ctx); err != nil {
		return err
	}
	if participant.offline != nil {
		if err := participant.offline.ValidateCurrent(ctx, participant.projection.authority, participant.projection.proof, time.Now().UTC()); err != nil {
			return err
		}
	}
	if participant.refresh != nil {
		if err := participant.refresh.ValidateCurrent(ctx, participant.projection.authority, participant.projection.proof, time.Now().UTC()); err != nil {
			return err
		}
	}
	return participant.captured.authenticated.ValidateCurrentMetadata(time.Now().UTC())
}

func (participant *databaseAcceptanceParticipant) prepareOwnedSnapshot(ctx context.Context, queries *sqlc.Queries, cluster uuid.UUID) (ownedSnapshotRefresh, error) {
	if ctx == nil || queries == nil || cluster == uuid.Nil {
		return nil, errors.New("database acceptance transaction unavailable")
	}
	if err := participant.revalidateOwnedSnapshot(ctx); err != nil {
		return nil, err
	}
	projection := participant.projection
	catalog, err := projection.authority.Catalog(ctx, projection.proof)
	if err != nil {
		return nil, err
	}
	if err := queries.LockVulnerabilityDatabaseAuthority(ctx); err != nil {
		return nil, err
	}
	base, err := queries.GetCurrentVulnerabilityDatabaseBaseForUpdate(ctx)
	if err != nil || !databaseProjectionBaseMatches(base, projection.snapshot, projection.bindingFingerprint) || !projection.snapshot.Base.ScanningEnabled {
		return nil, errors.New("database acceptance current base differs or scanner is disabled")
	}
	if participant.captured.document.Binding != projection.snapshot.Base.Binding {
		return nil, errors.New("database acceptance signed base binding differs")
	}
	intent, err := lockDatabaseScannerIntent(ctx, queries, cluster, catalog)
	if err != nil {
		return nil, err
	}
	update, err := participant.acceptLockedUpdate(ctx, queries, base)
	if err != nil {
		return nil, err
	}
	expected, err := databaseProjectionExpectedAssignment(intent.expected, intent.deployment.ID, intent.deployment.DesiredGeneration, intent.effective)
	if err != nil {
		return nil, err
	}
	if projection.snapshot.RegistryRootsFingerprint != "" {
		expected.Source.CredentialSecret = protocol.ObjectNames(intent.expected.ProjectID.String(), intent.deployment.ID.String()).AuthSecret
	}
	return participant.assignmentAcceptance(cluster, base, update, intent, expected), nil
}
