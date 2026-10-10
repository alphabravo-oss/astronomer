package config

import "github.com/alphabravocompany/astronomer-go/internal/httpclient"

// Normalize the two independent trust inputs without sharing their authority.
func normalizeHelmAndDeliveryTrust(cfg *Config) error {
	if err := normalizeDeliveryFluxTrust(cfg); err != nil {
		return err
	}
	if _, err := httpclient.ParseDestinationPolicy(cfg.HelmRepositoryDestinationPolicy); err != nil {
		return err
	}
	return httpclient.ValidateProxyOrigin(cfg.HelmRepositoryProxyURL)
}
