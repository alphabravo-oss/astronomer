package server

import (
	"context"
	"errors"
	"regexp"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/builtin"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/provider"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

var databaseConfiguredDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var databaseConfiguredCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)
var databaseConfiguredVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)

// This private configuration constructor cannot be selected by a feed, catalog,
// HTTP request or caller-supplied authentication result. Public fingerprinting
// of mirror bytes is comparison DATA only; authentication occurs after the
// restart reduction phase, through the same canonical mirror verifier.
func databaseStartupOptions(ctx context.Context, cfg *config.Config) (distribution.DatabaseRestartInspectionOptions, error) {
	return databaseStartupOptionsForBuild(ctx, cfg, version.Version, version.GitCommit)
}

// Build DATA is supplied only by the private process adapter above. This helper
// permits pure characterization without mutating global version variables.
func databaseStartupOptionsForBuild(ctx context.Context, cfg *config.Config, release, commit string) (distribution.DatabaseRestartInspectionOptions, error) {
	var result distribution.DatabaseRestartInspectionOptions
	if ctx == nil || cfg == nil || !cfg.DeliveryDatabaseUpdatesEnabled || !cfg.DeliveryEnabled || cfg.ReleaseManifestPath == "" || cfg.DeliveryBundleRepository == "" || !databaseConfiguredVersion.MatchString(release) || !databaseConfiguredCommit.MatchString(commit) || !databaseConfiguredDigest.MatchString(cfg.DeliveryBundleDigest) || !databaseConfiguredDigest.MatchString(cfg.DeliveryBundleCatalogDigest) || !databaseConfiguredDigest.MatchString(cfg.DeliveryDatabaseScannerDigest) || !databaseConfiguredVersion.MatchString(cfg.DeliveryDatabaseScannerVersion) {
		return result, errors.New("database startup selected release or scanner configuration incomplete")
	}
	if err := databaseRefreshSettings(cfg); err != nil {
		return result, err
	}
	if cfg.DeliverySourceCAFile != "" || cfg.DeliverySourceProxyURL != "" {
		return result, errors.New("database startup requires its exact mounted registry roots and direct destination policy")
	}
	network, err := httpclient.ParseDestinationPolicy(cfg.DistributionDestinationPolicy)
	if err != nil || !network.Restricted {
		return result, errors.New("database startup requires explicit restricted HTTPS registry authority")
	}
	if cfg.DistributionMirrorPlanPath == "" || cfg.DistributionMirrorSignaturePath == "" || cfg.DistributionMirrorKeyRef == "" || cfg.DeliveryDatabaseRegistryRootsRef == "" {
		return result, errors.New("database startup mirror or registry configuration incomplete")
	}
	identity, err := resolver.FingerprintMountedPublicMetadata(ctx, cfg.DistributionMirrorPlanPath)
	if err != nil {
		return result, err
	}
	binding := distribution.DatabaseUpdateBinding{BaseRelease: release, BaseDistributionDigest: cfg.DeliveryBundleDigest, MirrorEndorsementDigest: identity, CatalogDigest: cfg.DeliveryBundleCatalogDigest, Profile: "builtins", CandidateSourceCommit: commit, Scanner: distribution.DatabaseScannerCompatibility{ImageDigest: cfg.DeliveryDatabaseScannerDigest, Version: cfg.DeliveryDatabaseScannerVersion, MainSchema: 2, JavaSchema: 1}}
	result.Distribution = distribution.Options{Repository: cfg.DeliveryBundleRepository, Digest: cfg.DeliveryBundleDigest, CatalogDigest: cfg.DeliveryBundleCatalogDigest, Release: release, Profile: "builtins", UpdateBinding: &binding, Policy: model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: cfg.DeliveryBundleCertificateIdentity, Issuer: cfg.DeliveryBundleOIDCIssuer}, TrustDirectory: cfg.DeliverySourceTrustDirectory, Network: network}
	read, err := databaseReadRegistryOptions(cfg)
	if err != nil {
		return distribution.DatabaseRestartInspectionOptions{}, err
	}
	result.Registry = &resolver.DatabaseRegistryTrustOptions{TrustDirectory: cfg.DeliverySourceTrustDirectory, RootsRef: cfg.DeliveryDatabaseRegistryRootsRef, Network: network.Clone(), Read: read}
	result.Mirror = &distribution.DatabaseRestartMirrorReference{PayloadPath: cfg.DistributionMirrorPlanPath, TrustDirectory: cfg.DeliverySourceTrustDirectory, KeyRef: cfg.DistributionMirrorKeyRef}
	// Configuration validation allocates no frozen trust, Loader or HTTP client.
	if _, err := distribution.NewDatabaseRestartInspector(result); err != nil {
		return distribution.DatabaseRestartInspectionOptions{}, err
	}
	return result, nil
}

func (c *productionComposition) prepareDeliveryCatalog(ctx context.Context, cfg *config.Config) (builtin.CatalogLoader, error) {
 if err:=validateOwnedDatabaseScanSettings(cfg);err!=nil{return nil,err}
	if !cfg.DeliveryDatabaseUpdatesEnabled {
		ca, err := readBuiltinCA(cfg.DeliverySourceCAFile)
		if err != nil {
			return nil, err
		}
		loader, err := newDistributionLoader(cfg, ca)
		if err != nil {
			return nil, err
		}
		if err := c.configureBuiltinCA(cfg, loader, ca); err != nil {
			return nil, err
		}
		return loader, nil
	}
	options, err := databaseStartupOptions(ctx, cfg)
	if err != nil {
		return nil, err
	}
	revoker, err := provider.NewDatabaseRestartRevoker(c.database.Pool(), options)
	if err != nil {
		return nil, err
	}
	_, err = revoker.RevokeLostCurrent(ctx)
	if err == nil {
		return nil, errors.New("database startup current trust lost; durable base permanently revoked")
	}
	if !databaseStartupCanLoad(ctx, err) {
		return nil, err
	}
	// Only healthy retained material or an actually absent locked base proceeds.
	// Unknown/error states never trigger normal-load fallback or a promotion.
	return c.prepareOwnedDatabaseCatalog(ctx, cfg, options, revoker)
}

func (c *productionComposition) prepareOwnedDatabaseCatalog(ctx context.Context, cfg *config.Config, options distribution.DatabaseRestartInspectionOptions, revoker *provider.DatabaseRestartRevoker) (builtin.CatalogLoader, error) {
	mirror, err := distributionMirrorPlanWithContext(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if mirror == nil || mirror.Identity() != options.Distribution.UpdateBinding.MirrorEndorsementDigest {
		return nil, errors.New("database startup mirror changed during authentication")
	}
	options.Distribution.MirrorPlan = mirror
	authority, err := distribution.NewPinnedDatabaseAuthority(ctx, options.Distribution, *options.Registry)
	if err != nil {
		return nil, err
	}
	configured := false
	defer func() {
		if !configured {
			authority.Close()
		}
	}()
	baseOwner, err := provider.NewDatabaseBaseOwner(ctx, c.database.Pool(), authority)
	if err != nil {
		return nil, err
	}
	if _, err := baseOwner.Initialize(ctx); err != nil {
		return nil, err
	}
	proof, err := authority.Base(ctx)
	if err != nil {
		return nil, err
	}
	loader := &databaseAuthorityCatalog{authority: authority, proof: proof}
	ca, err := authority.RegistryCertificateAuthorities(ctx, proof)
	if err != nil {
		return nil, err
	}
	defer clear(ca)
	original, err := provider.NewWithBuiltinCA(c.queries, c.encryptor, ca, builtinCAAuthorizer(loader, options.Distribution.Network))
	if err != nil {
		return nil, err
	}
	owned, err := provider.NewWithDatabaseAuthority(ctx, c.database.Pool(), original, authority)
	if err != nil {
		return nil, err
	}
	refresh, err := provider.NewDatabaseRefreshOwner(ctx, c.database.Pool(), original, authority, cfg.DeliveryDatabaseUpdateRepository, cfg.DeliveryDatabaseCacheDirectory, revoker)
	if err != nil {
		return nil, err
	}
	if err:=c.configureOwnedDatabaseScan(ctx,cfg,original,authority,proof);err!=nil{return nil,err}
	c.databaseRefreshOwner = refresh
	c.hub.SetDeliveryStateProvider(owned)
	c.databaseAuthority = authority
	configured = true
	return loader, nil
}

// Both first boot and later provisioner reconciles read the same current
// physical authority, not the private Loader's cached catalog alone.
type databaseAuthorityCatalog struct {
	authority *distribution.DatabaseAuthority
	proof     *distribution.VerifiedDatabaseAuthorityBase
}

func (catalog *databaseAuthorityCatalog) Load(ctx context.Context) (builtinbundles.Catalog, error) {
	if catalog == nil {
		return builtinbundles.Catalog{}, errors.New("database startup catalog unavailable")
	}
	return catalog.authority.Catalog(ctx, catalog.proof)
}

func (c *productionComposition) closeUntransferredDatabaseAuthority() {
 if c.databaseScanMaterial!=nil {c.databaseScanMaterial.Close();c.databaseScanMaterial=nil}
 if c.databaseScanPull!=nil {c.databaseScanPull.Close();c.databaseScanPull=nil}
	if c.databaseAuthority != nil {
		c.databaseAuthority.Close()
		c.databaseAuthority = nil
	}
}
func (c *productionComposition) transferDatabaseAuthority(server *Server) {
	if c.databaseAuthority == nil {
		return
	}
	authority := c.databaseAuthority
 material,pull:=c.databaseScanMaterial,c.databaseScanPull
 c.databaseScanMaterial=nil;c.databaseScanPull=nil
	server.addResourceCloser("database physical authority", func(context.Context) error {
		if material!=nil {material.Close()};if pull!=nil {pull.Close()}
		authority.Close()
		return nil
	})
	c.databaseAuthority = nil
}

// Only the exact reduce-only result classes may continue to authenticated
// loading. Context always takes precedence; arbitrary database/network errors
// cannot become a bootstrap absence or a healthy-trust fallback.
func databaseStartupCanLoad(ctx context.Context, err error) bool {
	return ctx != nil && ctx.Err() == nil && (errors.Is(err, distribution.ErrNoConfirmedDatabaseRestartTrustLoss) || errors.Is(err, provider.ErrDatabaseRestartBaseAbsent))
}
