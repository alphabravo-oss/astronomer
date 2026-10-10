package config

import "github.com/alphabravocompany/astronomer-go/internal/httpclient"

// Normalize the two independent trust inputs without sharing their authority.
func normalizeHelmAndDeliveryTrust(cfg *Config) error {
	if err := normalizeDeliveryFluxTrust(cfg); err != nil {
		return err
	}
	policy, err := httpclient.ParseDestinationPolicy(cfg.HelmRepositoryDestinationPolicy)
	if err != nil {
		return err
	}
	routing := cfg.HelmRepositoryProxyRouting
	if routing.URL == "" && routing.HTTPURL == "" && routing.HTTPSURL == "" && routing.NoProxy == "" {
		routing = httpclient.ProxyRouting{URL: cfg.HelmRepositoryProxyURL, NoProxy: cfg.HelmRepositoryNoProxy}
	}
	return httpclient.ValidateProxyRouting(routing, policy)
}
