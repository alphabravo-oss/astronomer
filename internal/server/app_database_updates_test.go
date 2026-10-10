//go:build unix

package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/provider"
)

func databaseStartupConfigFixture(t *testing.T) (*config.Config, []byte) {
	t.Helper()
	directory := t.TempDir()
	payload := []byte("uninterpreted original operator endorsement")
	path := filepath.Join(directory, "mirror.json")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	return &config.Config{DeliveryDatabaseUpdatesEnabled: true, DeliveryEnabled: true,
		ReleaseManifestPath:      filepath.Join(directory, "release.json"),
		DeliveryBundleRepository: "registry.corp/distribution", DeliveryBundleDigest: "sha256:" + strings.Repeat("1", 64), DeliveryBundleCatalogDigest: "sha256:" + strings.Repeat("2", 64),
		DeliveryDatabaseScannerVersion: "0.70.0", DeliveryDatabaseScannerDigest: "sha256:" + strings.Repeat("3", 64),
		DeliveryBundleCertificateIdentity: "https://github.com/AlphaBravoCompany/astronomer/.github/workflows/release.yaml@refs/tags/v1.2.1", DeliveryBundleOIDCIssuer: "https://token.actions.githubusercontent.com",
		DeliverySourceTrustDirectory: directory, DeliveryDatabaseRegistryRootsRef: "registry",
		DeliveryDatabaseUpdateRepository: "registry.corp/database-updates", DeliveryDatabaseCacheDirectory: directory,
		DistributionMirrorPlanPath: path, DistributionMirrorSignaturePath: filepath.Join(directory, "mirror.sig"), DistributionMirrorKeyRef: "operator",
		DistributionDestinationPolicy: `{"restricted":true,"origins":[{"url":"https://registry.corp","cidrs":["10.42.0.0/16"]}]}`}, payload
}

func TestDatabaseStartupConfigurationIsDataBeforeRegistryAndSnapshot(t *testing.T) {
	cfg, payload := databaseStartupConfigFixture(t)
	// No vendor/operator key, CA, release or signature file exists, and a frozen
	// trust directory cannot be allocated. Configuration must not load/verify or
	// construct a network client before the reduce-only retained-row phase.
	t.Setenv("TMPDIR", filepath.Join(cfg.DeliverySourceTrustDirectory, "missing"))
	options, err := databaseStartupOptionsForBuild(t.Context(), cfg, "1.2.1", strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	binding := options.Distribution.UpdateBinding
	if binding == nil || binding.CandidateSourceCommit != strings.Repeat("a", 40) || binding.BaseDistributionDigest != cfg.DeliveryBundleDigest || binding.CatalogDigest != cfg.DeliveryBundleCatalogDigest || binding.BaseRelease != "1.2.1" || binding.Profile != "builtins" || binding.Scanner.ImageDigest != cfg.DeliveryDatabaseScannerDigest || binding.Scanner.Version != cfg.DeliveryDatabaseScannerVersion || binding.Scanner.MainSchema != 2 || binding.Scanner.JavaSchema != 1 || binding.MirrorEndorsementDigest != fmt.Sprintf("sha256:%x", sha256.Sum256(payload)) {
		t.Fatal("startup binding differs from exact process configuration")
	}
	if options.Distribution.Client != nil || options.Distribution.MirrorPlan != nil || options.Distribution.Development || options.Distribution.Policy.AllowUnsigned || options.Distribution.Policy.Provider != model.SignatureCosignKeyless || options.Distribution.Policy.Identity != cfg.DeliveryBundleCertificateIdentity || options.Registry == nil || options.Mirror == nil {
		t.Fatal("configuration manufactured runtime authentication or transport")
	}
	cfg.DeliveryDatabaseScannerDigest = "sha256:" + strings.Repeat("4", 64)
	if binding.Scanner.ImageDigest == cfg.DeliveryDatabaseScannerDigest {
		t.Fatal("constructor retained mutable config")
	}
	options.Registry.Network.Origins[0].URL = "https://changed.corp"
	if options.Distribution.Network.Origins[0].URL != "https://registry.corp" {
		t.Fatal("registry configuration aliases distribution destinations")
	}
}

func TestDatabaseStartupRefusesIncompleteOrAlternateAuthority(t *testing.T) {
	mutations := map[string]func(*config.Config){
		"disabled":             func(c *config.Config) { c.DeliveryDatabaseUpdatesEnabled = false },
		"delivery-disabled":    func(c *config.Config) { c.DeliveryEnabled = false },
		"release-absent":       func(c *config.Config) { c.ReleaseManifestPath = "" },
		"parent-tag":           func(c *config.Config) { c.DeliveryBundleDigest = "latest" },
		"catalog-invalid":      func(c *config.Config) { c.DeliveryBundleCatalogDigest = "sha256:bad" },
		"scanner-tag":          func(c *config.Config) { c.DeliveryDatabaseScannerDigest = "latest" },
		"scanner-version":      func(c *config.Config) { c.DeliveryDatabaseScannerVersion = "next" },
		"unsigned-vendor":      func(c *config.Config) { c.DeliveryBundleCertificateIdentity = "" },
		"system-root-fallback": func(c *config.Config) { c.DeliverySourceCAFile = "alternate.pem" },
		"proxy":                func(c *config.Config) { c.DeliverySourceProxyURL = "https://proxy.corp" },
		"public-default":       func(c *config.Config) { c.DistributionDestinationPolicy = "" },
		"http-registry": func(c *config.Config) {
			c.DistributionDestinationPolicy = `{"restricted":true,"origins":[{"url":"http://registry.corp","cidrs":["10.42.0.0/16"]}]}`
		},
		"feed-missing":             func(c *config.Config) { c.DeliveryDatabaseUpdateRepository = "" },
		"feed-tag":                 func(c *config.Config) { c.DeliveryDatabaseUpdateRepository += ":current" },
		"cache-relative":           func(c *config.Config) { c.DeliveryDatabaseCacheDirectory = "cache" },
		"cache-unclean":            func(c *config.Config) { c.DeliveryDatabaseCacheDirectory += "/../cache" },
		"roots-missing":            func(c *config.Config) { c.DeliveryDatabaseRegistryRootsRef = "" },
		"roots-path":               func(c *config.Config) { c.DeliveryDatabaseRegistryRootsRef = "../outside" },
		"mirror-signature-missing": func(c *config.Config) { c.DistributionMirrorSignaturePath = "" },
		"mirror-key-missing":       func(c *config.Config) { c.DistributionMirrorKeyRef = "" },
		"mirror-file-missing":      func(c *config.Config) { c.DistributionMirrorPlanPath += ".absent" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			cfg, _ := databaseStartupConfigFixture(t)
			mutate(cfg)
			if _, err := databaseStartupOptionsForBuild(t.Context(), cfg, "1.2.1", strings.Repeat("a", 40)); err == nil {
				t.Fatal("incomplete or alternate startup authority accepted")
			}
		})
	}
	cfg, _ := databaseStartupConfigFixture(t)
	for _, commit := range []string{"unknown", strings.Repeat("a", 39), strings.Repeat("A", 40)} {
		if _, err := databaseStartupOptionsForBuild(t.Context(), cfg, "1.2.1", commit); err == nil {
			t.Fatal("unstamped source accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := databaseStartupOptionsForBuild(ctx, cfg, "1.2.1", strings.Repeat("a", 40)); !errors.Is(err, context.Canceled) {
		t.Fatal("startup configuration lost cancellation")
	}
}

func TestDatabaseStartupContinuationOnlyRecognizesExactReduceOnlyResults(t *testing.T) {
	for _, err := range []error{distribution.ErrNoConfirmedDatabaseRestartTrustLoss, provider.ErrDatabaseRestartBaseAbsent, fmt.Errorf("wrapped: %w", provider.ErrDatabaseRestartBaseAbsent)} {
		if !databaseStartupCanLoad(t.Context(), err) {
			t.Fatal("exact healthy/absent classification rejected")
		}
	}
	for _, err := range []error{nil, context.Canceled, context.DeadlineExceeded, errors.New("database startup current base absent"), errors.New("permission or source error")} {
		if databaseStartupCanLoad(t.Context(), err) {
			t.Fatal("unknown/lost/error result granted loading")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if databaseStartupCanLoad(ctx, provider.ErrDatabaseRestartBaseAbsent) || databaseStartupCanLoad(nil, distribution.ErrNoConfirmedDatabaseRestartTrustLoss) {
		t.Fatal("context failure granted bootstrap")
	}
}
