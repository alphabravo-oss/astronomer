package provider

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// DatabaseImportOwner belongs only to the protected offline process command.
// It has no signer, publication attempt argument or public approval endpoint.
// Full original authentication and internal canonical whole readback precede
// its accurately labeled imported decision. The same existing finalizer creates
// assignments, accepted projections and rescan obligations transactionally.
type DatabaseImportOwner struct {
	pool       *pgxpool.Pool
	prototype  *Provider
	projection *databaseSnapshotProjection
	repository string
}

func NewDatabaseImportOwner(ctx context.Context, pool *pgxpool.Pool, original *Provider, authority *distribution.DatabaseAuthority, repository string) (*DatabaseImportOwner, error) {
	parsed, parseErr := remote.NewRepository(repository)
	if ctx == nil || pool == nil || original == nil || original.queries == nil || original.ownedSnapshots != nil || parseErr != nil || parsed.Reference.Reference != "" {
		return nil, errors.New("offline database import owner constructor unavailable")
	}
	projection, err := newDatabaseSnapshotProjection(ctx, authority)
	if err != nil {
		return nil, err
	}
	if !projection.snapshot.Base.ScanningEnabled || (projection.snapshot.RegistryRootsFingerprint != "" && (original.builtinCAIdentity != projection.snapshot.RegistryRootsFingerprint || original.authorizeBuiltinCA == nil)) {
		return nil, errors.New("offline database import scanner/provider authority differs")
	}
	copy := *original
	copy.builtinCA = bytes.Clone(original.builtinCA)
	copy.builtinSigningKey = bytes.Clone(original.builtinSigningKey)
	return &DatabaseImportOwner{pool: pool, prototype: &copy, projection: projection, repository: repository}, nil
}

// AcceptCopied first records the local import decision against the actual
// current physical/base authority. Each eligible cluster then uses the same
// canonical locked acceptance. A partially successful sweep returns failure;
// no completed scan, witness, public release or local endorsement is inferred.
func (owner *DatabaseImportOwner) AcceptCopied(ctx context.Context, original *distribution.VerifiedOfflineDatabaseUpdate, readback *distribution.DatabaseRefreshPayloads) error {
	if ctx == nil || owner == nil || owner.projection == nil || original == nil || readback == nil {
		return errors.New("offline database import owned capture unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 35*time.Minute)
	defer cancel()
	attempt, captured, err := owner.recordImported(bounded, original, readback)
	if err != nil {
		return err
	}
	participant := &databaseAcceptanceParticipant{projection: owner.projection, attempt: attempt.ID, captured: captured, refresh: readback, offline: original}
	coordinator, err := newOwnedSnapshotCoordinator(owner.pool, owner.prototype, participant)
	if err != nil {
		return err
	}
	sweep := &DatabaseRefreshOwner{pool: owner.pool, queries: sqlc.New(owner.pool), prototype: owner.prototype, projection: owner.projection, repository: owner.repository}
	return sweep.refreshClusters(bounded, coordinator, readback)
}

func (owner *DatabaseImportOwner) recordImported(ctx context.Context, original *distribution.VerifiedOfflineDatabaseUpdate, readback *distribution.DatabaseRefreshPayloads) (sqlc.VulnerabilityDatabaseUpdateAttempt, databaseAcceptanceCapture, error) {
	var attempted sqlc.VulnerabilityDatabaseUpdateAttempt
	var captured databaseAcceptanceCapture
	revalidate := func(ctx context.Context) error {
		if err := original.ValidateCurrent(ctx, owner.projection.authority, owner.projection.proof, time.Now().UTC()); err != nil {
			return err
		}
		return readback.ValidateCurrent(ctx, owner.projection.authority, owner.projection.proof, time.Now().UTC())
	}
	if err := revalidate(ctx); err != nil {
		return attempted, captured, err
	}
	feed, document, main, java, valid := original.Snapshot()
	authenticated, observedMain, observedJava, readbackValid := readback.Snapshot()
	observedFeed, observedDocument, authenticatedValid := authenticated.Snapshot()
	if !valid || !readbackValid || !authenticatedValid || observedFeed != feed || !reflect.DeepEqual(document, observedDocument) || main != observedMain || java != observedJava {
		return attempted, captured, errors.New("offline database import canonical full readback differs")
	}
	captured = databaseAcceptanceCapture{authenticated: authenticated, feed: feed, document: document, main: main, java: java}
	decision, err := databaseUpdateDecisionDigest(document)
	if err != nil {
		return attempted, captured, err
	}
	transactionOwner := &DatabasePublicationOwner{pool: owner.pool, projection: owner.projection}
	err = transactionOwner.transaction(ctx, func(ctx context.Context, queries *sqlc.Queries) error {
		base, err := transactionOwner.currentBase(ctx, queries)
		if err != nil {
			return err
		}
		if err := revalidate(ctx); err != nil {
			return err
		}
		if !databasePublicationBindingMatchesBase(document.Binding, base) || document.Generation <= 0 {
			return errors.New("offline database import signed current base differs")
		}
		previous, err := queries.GetVulnerabilityDatabaseAttemptByGenerationForUpdate(ctx, sqlc.GetVulnerabilityDatabaseAttemptByGenerationForUpdateParams{BaseID: base.ID, BaseEpoch: base.Epoch, Generation: document.Generation})
		if err == nil {
			// A real local publication already recording these identical immutable
			// bytes stays published. An imported replay stays imported. Neither failed
			// nor reserved rows can be resurrected or relabeled by a kit.
			if !databasePublicationAttemptMatches(previous, base, document, decision) || !databaseCompleteAttemptPhase(previous.Phase) || !previous.PublishedFeedDigest.Valid || previous.PublishedFeedDigest.String != feed || document.Generation < base.AcceptedGeneration || document.Generation < base.NextGeneration {
				return errors.New("offline database import generation replay differs or was revoked")
			}
			attempted = previous
		} else {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if document.Generation <= base.NextGeneration || document.Generation <= base.AcceptedGeneration {
				return errors.New("offline database import cannot replace newer reserved/accepted generation")
			}
			rows, err := queries.ReserveVulnerabilityDatabaseImportedGeneration(ctx, sqlc.ReserveVulnerabilityDatabaseImportedGenerationParams{BaseID: base.ID, BaseEpoch: base.Epoch, Generation: document.Generation})
			if err != nil {
				return err
			}
			if rows != 1 {
				return errors.New("offline database import generation authority changed")
			}
			attempted, err = queries.CreateVulnerabilityDatabaseImportedAttempt(ctx, sqlc.CreateVulnerabilityDatabaseImportedAttemptParams{ID: uuid.New(), BaseID: base.ID, BaseEpoch: base.Epoch, Generation: document.Generation, DecisionDigest: decision, FeedDigest: databaseProjectionOptional(feed)})
			if err != nil {
				return err
			}
			if !databasePublicationAttemptMatches(attempted, base, document, decision) || attempted.Phase != "imported" || !attempted.PublishedFeedDigest.Valid || attempted.PublishedFeedDigest.String != feed {
				return errors.New("offline database import created disposition differs")
			}
		}
		if err := owner.projection.revalidateOwnedSnapshot(ctx); err != nil {
			return err
		}
		return revalidate(ctx)
	})
	return attempted, captured, err
}

func (owner *DatabaseImportOwner) CaptureKit(ctx context.Context, directory string, now time.Time) (*distribution.VerifiedOfflineDatabaseUpdate, error) {
	if owner == nil || owner.projection == nil {
		return nil, errors.New("offline database import owner unavailable")
	}
	return owner.projection.authority.CaptureOfflineUpdateKit(ctx, owner.projection.proof, directory, now)
}

// CopyKit performs actual unchanged copy and complete canonical internal
// readback. Captured original bytes do not choose the process's feed repository,
// mapping, destination policy, read/write authentication or trust material.
func (owner *DatabaseImportOwner) CopyKit(ctx context.Context, captured *distribution.VerifiedOfflineDatabaseUpdate, authentication resolver.DatabaseRegistryAuthentication, cacheDirectory string) (*distribution.DatabaseRefreshPayloads, error) {
	if ctx == nil || owner == nil || owner.projection == nil || captured == nil {
		return nil, errors.New("offline database import copy owner unavailable")
	}
	projection := owner.projection
	if err := captured.ValidateCurrent(ctx, projection.authority, projection.proof, time.Now().UTC()); err != nil {
		return nil, err
	}
	feed, document, _, _, valid := captured.Snapshot()
	original, sourceValid := captured.OriginalSubject()
	if !valid || !sourceValid {
		return nil, errors.New("offline database import graph unavailable")
	}
	target, err := projection.authority.DatabaseUpdateDestination(ctx, projection.proof, original)
	if err != nil {
		return nil, err
	}
	feedRepository, feedDigest, found := strings.Cut(target, "@")
	if !found || feedRepository != owner.repository || feedDigest != feed {
		return nil, errors.New("offline database import feed differs from configured destination")
	}
	if err := captured.CheckReadbackCapacity(ctx, projection.authority, projection.proof, cacheDirectory); err != nil {
		return nil, err
	}
	originals := make([]string, 0, 3)
	for _, snapshot := range document.Databases {
		originals = append(originals, snapshot.PublishedSubject)
	}
	originals = append(originals, original)
	// All three destinations are checked before the first immutable registry write.
	registries := make([]*distribution.DatabasePublicationRegistry, 0, 3)
	defer func() {
		for _, registry := range registries {
			registry.Close()
		}
	}()
	for _, subject := range originals {
		target, err := projection.authority.DatabaseUpdateDestination(ctx, projection.proof, subject)
		if err != nil {
			return nil, err
		}
		repository, _, _ := strings.Cut(target, "@")
		registry, err := projection.authority.PublicationRegistry(ctx, projection.proof, repository, authentication)
		if err != nil {
			return nil, err
		}
		registries = append(registries, registry)
	}
	for index, original := range originals {
		if err := registries[index].CopyOfflineUpdateSubject(ctx, captured, original, time.Now().UTC()); err != nil {
			return nil, err
		}
	}
	update, err := projection.authority.LoadUpdate(ctx, projection.proof, owner.repository, feed, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return projection.authority.CaptureRefreshPayloads(ctx, projection.proof, owner.repository, update, cacheDirectory, time.Now().UTC())
}
