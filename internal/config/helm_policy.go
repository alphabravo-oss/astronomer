package config

import "github.com/alphabravocompany/astronomer-go/internal/httpclient"

// Normalize the two independent trust inputs without sharing their authority.
func normalizeHelmAndDeliveryTrust(cfg *Config) error {
	if err := normalizeDeliveryFluxTrust(cfg); err != nil {
		return err
	}
	_, err := httpclient.ParseDestinationPolicy(cfg.HelmRepositoryDestinationPolicy)
	return err
}
