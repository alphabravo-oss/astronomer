// Package startup wires the layered registry policy and the egress-endpoints
// log line into the server and worker processes.
package startup

import (
	"context"
	"log/slog"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/egress"
	"github.com/alphabravocompany/astronomer-go/internal/registry"
)

// Egress builds the static out-of-cluster endpoint set for a management-plane
// process from its configuration, plus the categories configured at runtime.
func Egress(cfg *config.Config) *egress.Set {
	set := &egress.Set{}
	rc, _ := cfg.RegistryConfig()
	for _, fallback := range rc.Fallbacks {
		set.Add(fallback)
	}
	if !registry.IsLocal(cfg.DeliveryBundleRepository, rc.Local) {
		set.Add(cfg.DeliveryBundleRepository)
	}
	if !registry.IsLocal(cfg.DeliveryFluxDistributionRepository, rc.Local) {
		set.Add(cfg.DeliveryFluxDistributionRepository)
	}
	if !registry.IsLocal(cfg.CatalogURL, rc.Local) {
		set.Add(cfg.CatalogURL)
	}
	for _, endpoint := range []string{
		cfg.OTELExporterEndpoint, cfg.OIDCIssuer, cfg.DexBundledIssuerURL,
		cfg.HelmRepositoryProxyURL, cfg.DeliverySourceProxyURL,
		cfg.DeliveryDatabaseReadRegistry, cfg.DeliveryDatabaseReadRealmURL, cfg.DeliveryDatabaseUpdateRepository,
	} {
		set.Add(endpoint)
	}
	if cfg.GithubClientID != "" {
		set.Add("https://github.com")
		set.Add("https://api.github.com")
	}
	if cfg.GoogleClientID != "" {
		set.Add("https://accounts.google.com")
		set.Add("https://oauth2.googleapis.com")
	}
	for _, category := range []string{
		"smtp:platform_settings", "siem:destinations", "webhooks:subscriptions", "notifications:channels",
		"helm_repositories:database", "catalog_mirrors:ASTRONOMER_CATALOG_MIRRORS", "backup_s3:destinations",
		"monitoring_backends:database", "auth_connectors:dex", "adopted_clusters:tunnel_inbound",
	} {
		set.Dynamic(category)
	}
	return set
}

// Resolve builds the process Resolver, logs the egress line and resolves
// every configured runtime reference once, logging layer=<name> for each. A
// verified fallback hit replaces the configured reference; a fail-closed miss
// is logged at ERROR and the cluster-local reference is kept (pulls of it
// fail closed rather than reaching a public registry).
func Resolve(ctx context.Context, cfg *config.Config, logger *slog.Logger, service string) (*registry.Resolver, error) {
	rc, err := cfg.RegistryConfig()
	if err != nil {
		return nil, err
	}
	resolver, err := registry.New(rc, registry.VerifyDigestPinned, logger)
	if err != nil {
		return nil, err
	}
	Egress(cfg).Log(ctx, logger, service)
	if !resolver.Enabled() {
		logger.InfoContext(ctx, "registry resolution", "layer", registry.LayerOnline, "note", "REGISTRY_LOCAL unset: online profile, references used as rendered")
		return resolver, nil
	}
	targets := runtimeTargets(cfg)
	for _, t := range targets {
		if t.ref == "" {
			continue
		}
		resolved, err := resolver.Resolve(ctx, t.ref)
		if err != nil {
			logger.ErrorContext(ctx, "runtime reference unavailable in every configured registry layer", "name", t.name, "error", err)
			continue
		}
		if resolved.Layer == registry.LayerFallback && t.apply != nil {
			t.apply(resolved.Ref)
		}
	}
	return resolver, nil
}

type target struct {
	name, ref string
	apply     func(string)
}

// runtimeTargets lists every configured runtime reference with the setter
// that records a verified fallback location.
func runtimeTargets(cfg *config.Config) []target {
	targets := []target{{name: "server_image", ref: cfg.ServerImage}}
	if cfg.AgentImageRepository != "" {
		ref := cfg.AgentImageRepository
		if !strings.Contains(ref, "@") && cfg.AgentImageTag != "" {
			ref += ":" + cfg.AgentImageTag
		}
		targets = append(targets, target{name: "agent_image", ref: ref, apply: func(resolved string) {
			if strings.Contains(cfg.AgentImageRepository, "@") {
				cfg.AgentImageRepository = resolved
				return
			}
			cfg.AgentImageRepository = strings.TrimSuffix(resolved, ":"+cfg.AgentImageTag)
		}})
	}
	targets = append(targets,
		target{name: "kubectl_shell_image", ref: cfg.KubectlShellImage, apply: func(s string) { cfg.KubectlShellImage = s }},
		target{name: "management_backup_image", ref: cfg.ManagementBackupImage, apply: func(s string) { cfg.ManagementBackupImage = s }},
	)
	if cfg.DeliveryFluxDistributionRepository != "" && cfg.DeliveryFluxDistributionDigest != "" {
		targets = append(targets, target{name: "flux_distribution", ref: cfg.DeliveryFluxDistributionRepository + "@" + cfg.DeliveryFluxDistributionDigest, apply: func(s string) {
			cfg.DeliveryFluxDistributionRepository, _, _ = strings.Cut(s, "@")
		}})
	}
	if cfg.DeliveryBundleRepository != "" && cfg.DeliveryBundleDigest != "" {
		targets = append(targets, target{name: "builtin_bundles", ref: cfg.DeliveryBundleRepository + "@" + cfg.DeliveryBundleDigest, apply: func(s string) {
			cfg.DeliveryBundleRepository, _, _ = strings.Cut(s, "@")
		}})
	}
	if strings.HasPrefix(cfg.CatalogURL, "oci://") {
		targets = append(targets, target{name: "catalog_source", ref: cfg.CatalogURL, apply: func(s string) { cfg.CatalogURL = "oci://" + strings.TrimPrefix(s, "oci://") }})
	}
	return targets
}
