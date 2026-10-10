package server

import (
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/helmruntime"
)

// Embedded agent and Charlie receive the same explicitly configured Helm
// policy; catalog/source URLs cannot broaden these constructor values.
func helmRuntimeFromConfig(cfg *config.Config) helmruntime.Config {
	return helmruntime.Config{
		Driver: cfg.HelmDriver, RegistryConfig: cfg.HelmRegistryConfig,
		RepositoryConfig: cfg.HelmRepositoryConfig, RepositoryCache: cfg.HelmRepositoryCache,
		RepositoryDestinationPolicy: cfg.HelmRepositoryDestinationPolicy,
		PluginsDirectory:            cfg.HelmPluginsDirectory, BurstLimit: 100,
	}
}
