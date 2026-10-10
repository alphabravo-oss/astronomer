package server

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/provider"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/google/uuid"
)

// DatabasePublicationCommandOptions belongs only to the protected process
// command. Repositories are retrieval/destination DATA; mounted current base,
// roots, trust policy and immutable bytes independently authorize publication.
// No HTTP handler, feed, local attempt ID or approval boolean invokes this path.
type DatabasePublicationCommandOptions struct {
	MainRepository, JavaRepository, CosignBinary string
	Authentication                               resolver.DatabaseRegistryAuthentication
	SigningRequest                               *distribution.DatabasePublicationGitHubRequest
}
type DatabasePublicationCommandResult struct {
	AttemptID                   uuid.UUID
	Generation                  int64
	FeedDigest                  string
	Published, DiscoveryUpdated bool
}

// PublishDatabaseUpdate runs in the existing server image, but never starts
// NewApp, agents, credential rotation, migrations or a second HTTP server. The
// durable base must already be initialized by its actual owned startup path.
func PublishDatabaseUpdate(ctx context.Context, cfg *config.Config, command DatabasePublicationCommandOptions) (DatabasePublicationCommandResult, error) {
	var empty DatabasePublicationCommandResult
	if ctx == nil || cfg == nil || !cfg.DeliveryDatabaseUpdatesEnabled {
		return empty, errors.New("database update publication optional feature is disabled")
	}
	repositories := distribution.DatabasePublicationRepositories{Main: command.MainRepository, Java: command.JavaRepository}
	if repositories.Validate() != nil || cfg.DeliveryDatabaseUpdateRepository == repositories.Main || cfg.DeliveryDatabaseUpdateRepository == repositories.Java {
		return empty, errors.New("database update publication requires distinct operator role and feed repositories")
	}
	bounded, cancel := context.WithTimeout(ctx, 90*time.Minute)
	defer cancel()
	options, err := databaseStartupOptions(bounded, cfg)
	if err != nil {
		return empty, err
	}
	// Capture the executable before supplier network access or the first registry
	// write. Its pinned inode is executed and rehashed before/after every signature.
	signer, err := distribution.NewDatabasePublicationSignerWithGitHubOIDC(command.CosignBinary, command.SigningRequest)
	if err != nil {
		return empty, err
	}
	defer signer.Close()
	database, err := db.ConnectWithConfig(bounded, cfg.DatabaseURL, db.PoolConfig{MaxConns: 2, MinConns: 1})
	if err != nil {
		return empty, errors.New("database update publication database connection unavailable")
	}
	defer database.Close()
	if database.SchemaHealth(bounded) != nil {
		return empty, errors.New("database update publication schema health invalid")
	}
	schema, dirty, err := database.SchemaVersion(bounded)
	if err != nil || dirty || schema != db.ExpectedSchemaVersion {
		return empty, errors.New("database update publication current schema required")
	}
	restart, err := provider.NewDatabaseRestartRevoker(database.Pool(), options)
	if err != nil {
		return empty, err
	}
	_, err = restart.RevokeLostCurrent(bounded)
	if bounded.Err() != nil {
		return empty, bounded.Err()
	}
	// A publisher cannot bootstrap absence. Trust loss reduces authority first;
	// unknown/canceled/absent/revoked states never construct a signing loader.
	if !errors.Is(err, distribution.ErrNoConfirmedDatabaseRestartTrustLoss) {
		return empty, errors.New("database update publication requires existing healthy retained authority")
	}
	mirror, err := distributionMirrorPlanWithContext(bounded, cfg)
	if err != nil {
		return empty, err
	}
	if mirror == nil || mirror.Identity() != options.Distribution.UpdateBinding.MirrorEndorsementDigest {
		return empty, errors.New("database update publication authenticated mirror changed")
	}
	options.Distribution.MirrorPlan = mirror
	authority, err := distribution.NewPinnedDatabaseAuthority(bounded, options.Distribution, *options.Registry)
	if err != nil {
		return empty, err
	}
	defer authority.Close()
	proof, err := authority.Base(bounded)
	if err != nil {
		return empty, err
	}
	owner, err := provider.NewDatabasePublicationOwner(bounded, database.Pool(), authority, repositories)
	if err != nil {
		return empty, err
	}
	registries := make([]*distribution.DatabasePublicationRegistry, 0, 3)
	defer func() {
		for _, registry := range registries {
			registry.Close()
		}
	}()
	for _, repository := range []string{repositories.Main, repositories.Java, cfg.DeliveryDatabaseUpdateRepository} {
		registry, err := authority.PublicationRegistry(bounded, proof, repository, command.Authentication)
		if err != nil {
			return empty, err
		}
		registries = append(registries, registry)
	}
	// This private unique working directory retains complete supplier originals
	// on failure. It is not recursively removed; operator cleanup can retain the
	// failed publication's exact artifacts without touching shared cache paths.
	directory, err := authority.PublicationWorkingDirectory(bounded, proof, cfg.DeliveryDatabaseCacheDirectory)
	if err != nil {
		return empty, err
	}
	mainPath, javaPath := filepath.Join(directory, "main.tar.gz"), filepath.Join(directory, "java.tar.gz")
	mainCapture, main, err := authority.CapturePublicationSupplier(bounded, proof, "trivy-database", mainPath, time.Now().UTC())
	if err != nil {
		return empty, err
	}
	javaCapture, java, err := authority.CapturePublicationSupplier(bounded, proof, "trivy-java-database", javaPath, time.Now().UTC())
	if err != nil {
		return empty, err
	}
	return publishCapturedDatabaseUpdate(bounded, owner, authority, proof, signer, registries, mainCapture, main, mainPath, javaCapture, java, javaPath, cfg.DeliveryDatabaseUpdateRepository, directory)
}

func publishCapturedDatabaseUpdate(ctx context.Context, owner *provider.DatabasePublicationOwner, authority *distribution.DatabaseAuthority, proof *distribution.VerifiedDatabaseAuthorityBase, signer *distribution.DatabasePublicationSigner, registries []*distribution.DatabasePublicationRegistry, mainCapture *distribution.CapturedSupplierDatabase, main *distribution.VerifiedSupplierDatabasePayload, mainPath string, javaCapture *distribution.CapturedSupplierDatabase, java *distribution.VerifiedSupplierDatabasePayload, javaPath, feedRepository, cache string) (result DatabasePublicationCommandResult, err error) {
	if len(registries) != 3 {
		return result, errors.New("database update publication scoped registry closure invalid")
	}
	reservation, err := owner.Reserve(ctx, main, java)
	if err != nil {
		return result, err
	}
	attempt, document, valid := reservation.Snapshot()
	if !valid || len(document.Databases) != 2 {
		return result, errors.New("database update publication reservation unavailable")
	}
	result.AttemptID, result.Generation = attempt, document.Generation
	defer func() {
		if err != nil && !result.Published {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if failure := owner.Fail(cleanup, reservation); failure != nil {
				err = errors.Join(err, errors.New("database update publication reservation failure transition unavailable"))
			}
		}
	}()
	for index, snapshot := range document.Databases {
		captured, payload, path := mainCapture, main, mainPath
		if index == 1 {
			captured, payload, path = javaCapture, java, javaPath
		}
		if err = registries[index].PublishSupplierChild(ctx, signer, captured, payload, path, snapshot, time.Now().UTC()); err != nil {
			return result, err
		}
	}
	result.FeedDigest, err = registries[2].PublishFeed(ctx, signer, document, time.Now().UTC())
	if err != nil {
		return result, err
	}
	update, err := authority.LoadUpdate(ctx, proof, feedRepository, result.FeedDigest, time.Now().UTC())
	if err != nil {
		return result, err
	}
	// Readback includes actual unchanged full layers under signature-first
	// canonical transfer/metadata inspection, not just uploaded descriptors.
	captured, err := authority.CaptureRefreshPayloads(ctx, proof, feedRepository, update, cache, time.Now().UTC())
	if err != nil {
		return result, err
	}
	defer captured.Close()
	if err = captured.ValidateCurrent(ctx, authority, proof, time.Now().UTC()); err != nil {
		return result, err
	}
	if err = owner.PublishVerified(ctx, reservation, feedRepository, result.FeedDigest); err != nil {
		return result, err
	}
	result.Published = true
	// A locator failure cannot convert an already published decision to failed.
	// Consumers still authenticate the immutable digest and actual reservation.
	if err = registries[2].PublishCurrent(ctx, result.FeedDigest); err != nil {
		return result, errors.New("database update published; discovery locator update failed")
	}
	result.DiscoveryUpdated = true
	return result, nil
}
