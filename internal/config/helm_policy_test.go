package config

import "testing"

func TestHelmRepositoryPolicyIsValidatedIndependentlyOfDeliveryTrust(t *testing.T) {
	for _, policy := range []string{"", `{"origins":[{"url":"https://10.42.0.12:8443","cidrs":["10.42.0.12/32"]}]}`} {
		cfg := &Config{HelmRepositoryDestinationPolicy: policy}
		if err := normalizeHelmAndDeliveryTrust(cfg); err != nil {
			t.Fatal("valid independent Helm policy rejected")
		}
		if len(cfg.DeliveryFluxDistributionKeyring) != 0 {
			t.Fatal("Helm repository authority populated delivery signing trust")
		}
	}
	for _, policy := range []string{`{"restricted":true}`, `{"origins":[],"origins":[]}`} {
		if err := normalizeHelmAndDeliveryTrust(&Config{HelmRepositoryDestinationPolicy: policy}); err == nil {
			t.Fatal("invalid operator Helm policy admitted")
		}
	}
	if err := normalizeHelmAndDeliveryTrust(&Config{DeliveryFluxDistributionPublicKey: "invalid", HelmRepositoryDestinationPolicy: ""}); err == nil {
		t.Fatal("valid Helm policy bypassed delivery signing-trust validation")
	}
}

func TestHelmRepositoryProxyConfigurationIsTypedAndSeparate(t *testing.T) {
	for _, proxy := range []string{"", "https://proxy.internal.example:8443"} {
		cfg := &Config{HelmRepositoryProxyURL: proxy}
		if err := normalizeHelmAndDeliveryTrust(cfg); err != nil {
			t.Fatal("valid operator proxy shape rejected")
		}
		if cfg.HelmRepositoryDestinationPolicy != "" || cfg.DeliverySourceProxyURL != "" || len(cfg.DeliveryFluxDistributionKeyring) != 0 {
			t.Fatal("Helm proxy configuration widened another authority")
		}
	}
	for _, proxy := range []string{"http://proxy.internal.example:3128", "https://user:fixture@proxy.internal.example", "https://proxy.internal.example/path", "https://proxy.internal.example?"} {
		if err := normalizeHelmAndDeliveryTrust(&Config{HelmRepositoryProxyURL: proxy}); err == nil {
			t.Fatal("unsupported Helm proxy configuration admitted")
		}
	}
	// Configuration validates shape only. Constructor and every actual dial
	// must still check independent destination authority and resolved addresses.
}
