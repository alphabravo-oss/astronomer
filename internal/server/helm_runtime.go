package server

import (
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/helmruntime"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// Embedded agent and Charlie receive the same explicitly configured Helm
// policy; catalog/source URLs cannot broaden these constructor values.
func helmRuntimeFromConfig(cfg *config.Config) helmruntime.Config {
	routing := cfg.HelmRepositoryProxyRouting
	if routing.URL == "" && routing.HTTPURL == "" && routing.HTTPSURL == "" && routing.NoProxy == "" {
		routing = httpclient.ProxyRouting{URL: cfg.HelmRepositoryProxyURL, NoProxy: cfg.HelmRepositoryNoProxy}
	}
	return helmruntime.Config{
		Driver: cfg.HelmDriver, RegistryConfig: cfg.HelmRegistryConfig,
		RepositoryConfig: cfg.HelmRepositoryConfig, RepositoryCache: cfg.HelmRepositoryCache,
		RepositoryDestinationPolicy: cfg.HelmRepositoryDestinationPolicy,
		RepositoryProxyRouting:      routing,
		PluginsDirectory:            cfg.HelmPluginsDirectory, BurstLimit: 100,
	}
}
