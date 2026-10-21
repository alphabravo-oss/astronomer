package provider

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// DatabaseRefreshOwner is private process composition. It has no publication
// credentials or public approval entry point. Both repository and cache origin
// are operator-owned; feed data never expands trust or transport permissions.
type DatabaseRefreshOwner struct {
	pool                       *pgxpool.Pool
	restart                    *DatabaseRestartRevoker
	queries                    *sqlc.Queries
	prototype                  *Provider
	projection                 *databaseSnapshotProjection
	repository, cacheDirectory string
}

func NewDatabaseRefreshOwner(ctx context.Context, pool *pgxpool.Pool, original *Provider, authority *distribution.DatabaseAuthority, repository, cacheDirectory string, restart *DatabaseRestartRevoker) (*DatabaseRefreshOwner, error) {
	parsed, err := remote.NewRepository(repository)
	if ctx == nil || pool == nil || restart == nil || original == nil || original.queries == nil || original.ownedSnapshots != nil || err != nil || parsed.Reference.Reference != "" || !filepath.IsAbs(cacheDirectory) || filepath.Clean(cacheDirectory) != cacheDirectory {
		return nil, errors.New("database refresh constructor unavailable")
	}
	projection, err := newDatabaseSnapshotProjection(ctx, authority)
	if err != nil {
		return nil, err
	}
	if !projection.snapshot.Base.ScanningEnabled || (projection.snapshot.RegistryRootsFingerprint != "" && (original.builtinCAIdentity != projection.snapshot.RegistryRootsFingerprint || original.authorizeBuiltinCA == nil)) {
		return nil, errors.New("database refresh scanner or provider TLS differs")
	}
	// Clone only through the same constructor as acceptance, preserving mounted
	// CA and exact model compiler; never clone an already-wrapped coordinator.
	copy := *original
	copy.builtinCA = append([]byte(nil), original.builtinCA...)
	copy.builtinSigningKey = append([]byte(nil), original.builtinSigningKey...)
	return &DatabaseRefreshOwner{pool: pool, restart: restart, queries: sqlc.New(pool), prototype: &copy, projection: projection, repository: repository, cacheDirectory: cacheDirectory}, nil
}

// Refresh is synchronous and finite. Any capture, trust, reservation, paging or
// cluster acceptance error is a failed sweep even if earlier cluster commits
// succeeded. Existing known findings remain untouched. Rescan intent dispatch
// and actual scan-used witnesses are independent runtime responsibilities.
func (owner *DatabaseRefreshOwner) Refresh(ctx context.Context) error {
	if ctx == nil || owner == nil || owner.projection == nil {
		return errors.New("database refresh owner unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 35*time.Minute)
	defer cancel()
	if owner.restart == nil {
		return errors.New("database refresh current trust inspector unavailable")
	}
	_, trustError := owner.restart.RevokeLostCurrent(bounded)
	if bounded.Err() != nil {
		return bounded.Err()
	}
	if trustError == nil {
		return errors.New("database refresh physical trust lost; current base permanently revoked")
	}
	if !errors.Is(trustError, distribution.ErrNoConfirmedDatabaseRestartTrustLoss) {
		return trustError
	}
	projection := owner.projection
	update, err := projection.authority.DiscoverUpdate(bounded, projection.proof, owner.repository, time.Now().UTC())
	if err != nil {
		return err
	}
	// Resolve the real publisher or constructor-owned imported decision before
	// downloading large bytes. Imported remains a distinct durable disposition.
	feed, document, valid := update.Snapshot()
	if !valid {
		return errors.New("database refresh signed feed unavailable")
	}
	decision, err := databaseUpdateDecisionDigest(document)
	if err != nil {
		return err
	}
	if err := projection.revalidateOwnedSnapshot(bounded); err != nil {
		return err
	}
	attempt, err := owner.queries.GetCurrentPublishedVulnerabilityDatabaseAttempt(bounded, sqlc.GetCurrentPublishedVulnerabilityDatabaseAttemptParams{
		ExpectedBindingFingerprint:       projection.bindingFingerprint,
		ExpectedPolicyFingerprint:        projection.snapshot.Base.PolicyFingerprint,
		ExpectedTrustMaterialFingerprint: projection.snapshot.TrustMaterialFingerprint,
		FeedDigest:                       databaseProjectionOptional(feed), Generation: document.Generation, DecisionDigest: decision,
	})
	if err != nil {
		return safeOwnedSnapshotError(bounded, "database refresh completed reservation", err)
	}
	if attempt.ID == uuid.Nil || !databaseCompleteAttemptPhase(attempt.Phase) || !attempt.PublishedFeedDigest.Valid || attempt.PublishedFeedDigest.String != feed {
		return errors.New("database refresh completed reservation differs")
	}
	payloads, err := projection.authority.CaptureRefreshPayloads(bounded, projection.proof, owner.repository, update, owner.cacheDirectory, time.Now().UTC())
	if err != nil {
		return err
	}
	defer payloads.Close()
	authenticated, main, java, valid := payloads.Snapshot()
	if !valid {
		return errors.New("database refresh full payload capture unavailable")
	}
	captured := databaseAcceptanceCapture{authenticated: authenticated, feed: feed, document: document, main: main, java: java}
	participant := &databaseAcceptanceParticipant{projection: projection, attempt: attempt.ID, captured: captured, refresh: payloads}
	coordinator, err := newOwnedSnapshotCoordinator(owner.pool, owner.prototype, participant)
	if err != nil {
		return err
	}
	return owner.refreshClusters(bounded, coordinator, payloads)
}

func (owner *DatabaseRefreshOwner) refreshClusters(ctx context.Context, coordinator *Provider, payloads *distribution.DatabaseRefreshPayloads) error {
	catalog, err := owner.projection.authority.Catalog(ctx, owner.projection.proof)
	if err != nil {
		return err
	}
	slug := ""
	for _, component := range catalog.Components {
		if component.Source.ChartName() == "trivy-operator" {
			if slug != "" {
				return errors.New("database refresh scanner component ambiguous")
			}
			slug = component.Slug
		}
	}
	if slug == "" {
		return errors.New("database refresh scanner component unavailable")
	}
	// The canonical compiler's name is retrieval DATA, not source or assignment
	// authority. First adoption waits for ordinary scanner materialization; an
	// invalid materialized source/target remains visible to locked acceptance.
	params := sqlc.ListVulnerabilityDatabaseRefreshClustersParams{QueryLimit: 100, ScannerName: "astronomer-builtins-" + slug}
	// 10,000 clusters is a finite sweep ceiling, not claimed scale qualification.
	for page := 0; page < 100; page++ {
		clusters, err := owner.queries.ListVulnerabilityDatabaseRefreshClusters(ctx, params)
		if err != nil {
			return safeOwnedSnapshotError(ctx, "database refresh cluster page", err)
		}
		for _, cluster := range clusters {
			if err := payloads.ValidateCurrent(ctx, owner.projection.authority, owner.projection.proof, time.Now().UTC()); err != nil {
				return err
			}
			transactionContext, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, err := coordinator.ownedSnapshots.snapshot(transactionContext, cluster, protocol.DeliveryStateRequestV2{})
			cancel()
			if err != nil {
				return safeOwnedSnapshotError(ctx, "database refresh cluster acceptance", err)
			}
		}
		if len(clusters) < int(params.QueryLimit) {
			return nil
		}
		last := clusters[len(clusters)-1]
		params.AfterID = last
	}
	return errors.New("database refresh finite cluster sweep limit reached")
}
