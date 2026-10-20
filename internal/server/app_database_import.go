package server

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/provider"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// Import options belong only to the protected process bootstrap. A kit can
// choose no signer, issuer, policy, roots, target repository, attempt or approval.
// Write authentication is independent of the configured mounted read credential.
type DatabaseImportCommandOptions struct {
	KitDirectory   string
	Authentication resolver.DatabaseRegistryAuthentication
}
type DatabaseImportCommandResult struct {
	Generation                         int64
	FeedDigest                         string
	Copied, Accepted, DiscoveryUpdated bool
}

// ImportDatabaseUpdate copies already authenticated originals without signing.
// It never starts NewApp/agents, applies migrations or bootstraps a missing base.
// Local rows retain imported disposition; original published_at remains vendor
// DATA and no local OIDC execution is invented by import/readback success.
func ImportDatabaseUpdate(ctx context.Context, cfg *config.Config, command DatabaseImportCommandOptions) (DatabaseImportCommandResult, error) {
	var result DatabaseImportCommandResult
	if ctx == nil || cfg == nil || !cfg.DeliveryDatabaseUpdatesEnabled || !filepath.IsAbs(command.KitDirectory) || filepath.Clean(command.KitDirectory) != command.KitDirectory {
		return result, errors.New("offline database import feature or input invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 90*time.Minute)
	defer cancel()
	resources, err := prepareDatabaseTransferCommand(bounded, cfg)
	if err != nil {
		return result, err
	}
	defer resources.Close()
	authority, proof, original, database := resources.authority, resources.proof, resources.provider, resources.database
	owner, err := provider.NewDatabaseImportOwner(bounded, database.Pool(), original, authority, cfg.DeliveryDatabaseUpdateRepository)
	if err != nil {
		return result, err
	}
	captured, err := owner.CaptureKit(bounded, command.KitDirectory, time.Now().UTC())
	if err != nil {
		return result, err
	}
	defer captured.Close()
	feed, document, _, _, valid := captured.Snapshot()
	if !valid {
		return result, errors.New("offline database import canonical capture unavailable")
	}
	result.Generation, result.FeedDigest = document.Generation, feed
	readback, err := owner.CopyKit(bounded, captured, command.Authentication, cfg.DeliveryDatabaseCacheDirectory)
	if err != nil {
		return result, err
	}
	defer readback.Close()
	result.Copied = true
	if err := owner.AcceptCopied(bounded, captured, readback); err != nil {
		return result, err
	}
	result.Accepted = true
	// PublishCurrent changes only the independently configured internal discovery
	// locator after complete readback and the real imported acceptance sweep. A
	// locator failure cannot erase or relabel committed imported decisions.
	registry, err := authority.PublicationRegistry(bounded, proof, cfg.DeliveryDatabaseUpdateRepository, command.Authentication)
	if err != nil {
		return result, err
	}
	defer registry.Close()
	if err := registry.PublishCurrent(bounded, feed); err != nil {
		return result, errors.New("offline database import accepted; discovery locator update failed")
	}
	result.DiscoveryUpdated = true
	return result, nil
}

// Both protected process commands reuse this one ordinary bootstrap boundary.
// It never initializes a missing base, starts NewApp, grants loss-based approval
// or signs. Returned resources are private to this process and joined caller.
type databaseTransferCommandResources struct {
	database  *db.DB
	authority *distribution.DatabaseAuthority
	proof     *distribution.VerifiedDatabaseAuthorityBase
	provider  *provider.Provider
}

func (resources *databaseTransferCommandResources) Close() {
	if resources != nil {
		resources.authority.Close()
		resources.database.Close()
	}
}
func prepareDatabaseTransferCommand(bounded context.Context, cfg *config.Config) (*databaseTransferCommandResources, error) {
	options, err := databaseStartupOptions(bounded, cfg)
	if err != nil {
		return nil, err
	}
	database, err := db.ConnectWithConfig(bounded, cfg.DatabaseURL, db.PoolConfig{MaxConns: 2, MinConns: 1})
	if err != nil {
		return nil, errors.New("offline database import database connection unavailable")
	}
	accepted := false
	defer func() {
		if !accepted {
			database.Close()
		}
	}()
	if database.SchemaHealth(bounded) != nil {
		return nil, errors.New("offline database import schema health invalid")
	}
	schema, err := database.SchemaVersion(bounded)
	if err != nil || schema != db.ExpectedSchemaVersion {
		return nil, errors.New("offline database import current schema required")
	}
	restart, err := provider.NewDatabaseRestartRevoker(database.Pool(), options)
	if err != nil {
		return nil, err
	}
	_, err = restart.RevokeLostCurrent(bounded)
	if bounded.Err() != nil {
		return nil, bounded.Err()
	}
	if !errors.Is(err, distribution.ErrNoConfirmedDatabaseRestartTrustLoss) {
		return nil, errors.New("offline database import requires existing healthy retained authority")
	}
	mirror, err := distributionMirrorPlanWithContext(bounded, cfg)
	if err != nil {
		return nil, err
	}
	if mirror == nil || mirror.Identity() != options.Distribution.UpdateBinding.MirrorEndorsementDigest {
		return nil, errors.New("offline database import operator endorsement changed")
	}
	options.Distribution.MirrorPlan = mirror
	authority, err := distribution.NewPinnedDatabaseAuthority(bounded, options.Distribution, *options.Registry)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !accepted {
			authority.Close()
		}
	}()
	proof, err := authority.Base(bounded)
	if err != nil {
		return nil, err
	}
	ca, err := authority.RegistryCertificateAuthorities(bounded, proof)
	if err != nil {
		return nil, err
	}
	defer clear(ca)
	encryptor, err := auth.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, errors.New("offline database import existing credential reader unavailable")
	}
	catalog := &databaseAuthorityCatalog{authority: authority, proof: proof}
	original, err := provider.NewWithBuiltinCA(sqlc.New(database.Pool()), encryptor, ca, builtinCAAuthorizer(catalog, options.Distribution.Network))
	if err != nil {
		return nil, err
	}
	accepted = true
	return &databaseTransferCommandResources{database: database, authority: authority, proof: proof, provider: original}, nil
}
