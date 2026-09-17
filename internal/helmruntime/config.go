// Package helmruntime turns validated process configuration into Helm SDK
// settings. Callers load environment variables at their composition boundary;
// Helm consumers receive this value and never consult process-global state.
package helmruntime

import "helm.sh/helm/v3/pkg/cli"

const (
	defaultRegistryConfig   = "/tmp/helm/config/registry/config.json"
	defaultRepositoryConfig = "/tmp/helm/config/repositories.yaml"
	defaultRepositoryCache  = "/tmp/helm/cache/repository"
	defaultPluginsDirectory = "/tmp/helm/data/plugins"
)

// Config is the complete Helm SDK process contract used by the agent and
// Charlie runtime. It deliberately mirrors the mutable parts of
// cli.EnvSettings so every value can be injected instead of read ambiently.
type Config struct {
	Driver                    string
	Namespace                 string
	KubeConfig                string
	KubeContext               string
	KubeToken                 string
	KubeAsUser                string
	KubeAsGroups              []string
	KubeAPIServer             string
	KubeCAFile                string
	KubeInsecureSkipTLSVerify bool
	KubeTLSServerName         string
	Debug                     bool
	RegistryConfig            string
	RepositoryConfig          string
	RepositoryCache           string
	PluginsDirectory          string
	MaxHistory                int
	BurstLimit                int
	QPS                       float32
}

// InClusterDefaults returns writable paths and no external kubeconfig or
// impersonation. This is the safe contract for embedded in-cluster runtimes.
func InClusterDefaults() Config {
	return Config{
		RegistryConfig:   defaultRegistryConfig,
		RepositoryConfig: defaultRepositoryConfig,
		RepositoryCache:  defaultRepositoryCache,
		PluginsDirectory: defaultPluginsDirectory,
		BurstLimit:       100,
	}
}

// FromSettings captures Helm's standard environment adapter once, while the
// caller is loading typed process configuration.
func FromSettings(settings *cli.EnvSettings, driver string) Config {
	if settings == nil {
		return InClusterDefaults()
	}
	return Config{
		Driver:                    driver,
		Namespace:                 settings.Namespace(),
		KubeConfig:                settings.KubeConfig,
		KubeContext:               settings.KubeContext,
		KubeToken:                 settings.KubeToken,
		KubeAsUser:                settings.KubeAsUser,
		KubeAsGroups:              append([]string(nil), settings.KubeAsGroups...),
		KubeAPIServer:             settings.KubeAPIServer,
		KubeCAFile:                settings.KubeCaFile,
		KubeInsecureSkipTLSVerify: settings.KubeInsecureSkipTLSVerify,
		KubeTLSServerName:         settings.KubeTLSServerName,
		Debug:                     settings.Debug,
		RegistryConfig:            settings.RegistryConfig,
		RepositoryConfig:          settings.RepositoryConfig,
		RepositoryCache:           settings.RepositoryCache,
		PluginsDirectory:          settings.PluginsDirectory,
		MaxHistory:                settings.MaxHistory,
		BurstLimit:                settings.BurstLimit,
		QPS:                       settings.QPS,
	}
}

// Settings creates Helm's required EnvSettings object, then overwrites every
// environment-derived field with the injected contract before it can be used.
func (c Config) Settings() *cli.EnvSettings {
	settings := cli.New()
	settings.SetNamespace(c.Namespace)
	settings.KubeConfig = c.KubeConfig
	settings.KubeContext = c.KubeContext
	settings.KubeToken = c.KubeToken
	settings.KubeAsUser = c.KubeAsUser
	settings.KubeAsGroups = append([]string(nil), c.KubeAsGroups...)
	settings.KubeAPIServer = c.KubeAPIServer
	settings.KubeCaFile = c.KubeCAFile
	settings.KubeInsecureSkipTLSVerify = c.KubeInsecureSkipTLSVerify
	settings.KubeTLSServerName = c.KubeTLSServerName
	settings.Debug = c.Debug
	settings.RegistryConfig = defaultString(c.RegistryConfig, defaultRegistryConfig)
	settings.RepositoryConfig = defaultString(c.RepositoryConfig, defaultRepositoryConfig)
	settings.RepositoryCache = defaultString(c.RepositoryCache, defaultRepositoryCache)
	settings.PluginsDirectory = defaultString(c.PluginsDirectory, defaultPluginsDirectory)
	settings.MaxHistory = c.MaxHistory
	settings.BurstLimit = c.BurstLimit
	if settings.BurstLimit <= 0 {
		settings.BurstLimit = 100
	}
	settings.QPS = c.QPS
	return settings
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
