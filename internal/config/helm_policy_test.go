package config

import (
	"os"
	"testing"
)

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
	for _, proxy := range []string{"", "https://proxy.internal.example:8443", "https://user:fixture@proxy.internal.example"} {
		cfg := &Config{HelmRepositoryProxyURL: proxy}
		if err := normalizeHelmAndDeliveryTrust(cfg); err != nil {
			t.Fatal("valid operator proxy shape rejected")
		}
		if cfg.HelmRepositoryDestinationPolicy != "" || cfg.DeliverySourceProxyURL != "" || len(cfg.DeliveryFluxDistributionKeyring) != 0 {
			t.Fatal("Helm proxy configuration widened another authority")
		}
	}
	for _, proxy := range []string{"http://proxy.internal.example:3128", "https://proxy.internal.example/path", "https://proxy.internal.example?"} {
		if err := normalizeHelmAndDeliveryTrust(&Config{HelmRepositoryProxyURL: proxy}); err == nil {
			t.Fatal("unsupported Helm proxy configuration admitted")
		}
	}
	// Configuration validates shape only. Constructor and every actual dial
	// must still check independent destination authority and resolved addresses.
}

func TestHelmProxyEnvironmentIsCapturedOnceWithPrivatePolicyAndExplicitBypass(t *testing.T) {
	value, present := os.LookupEnv("HELM_REPOSITORY_NO_PROXY")
	if err := os.Unsetenv("HELM_REPOSITORY_NO_PROXY"); err != nil {
		t.Fatal("fixture Helm bypass reset failed")
	}
	t.Cleanup(func() {
		var err error
		if present {
			err = os.Setenv("HELM_REPOSITORY_NO_PROXY", value)
		} else {
			err = os.Unsetenv("HELM_REPOSITORY_NO_PROXY")
		}
		if err != nil {
			t.Error("fixture Helm bypass restoration failed")
		}
	})
	for key, value := range map[string]string{"HTTP_PROXY": "http://proxy.internal:3128", "HTTPS_PROXY": "https://secure-proxy.internal:8443", "NO_PROXY": ".svc,.cluster.local", "http_proxy": "http://lower.internal:3128", "https_proxy": "https://lower.internal:8443", "no_proxy": "lower.internal"} {
		t.Setenv(key, value)
	}
	cfg := &Config{HelmRepositoryDestinationPolicy: `{"origins":[{"url":"http://proxy.internal:3128","cidrs":["10.0.0.0/8"]}]}`}
	t.Setenv("HELM_REPOSITORY_PROXY_URL", "")
	t.Setenv("HELM_REPOSITORY_DESTINATION_POLICY", cfg.HelmRepositoryDestinationPolicy)
	captureHelmOperatorProxy(cfg, false)
	loaded, err := Load()
	if err != nil || loaded.HelmRepositoryProxyRouting != cfg.HelmRepositoryProxyRouting {
		t.Fatal("real configuration load did not retain captured operator routes")
	}
	if cfg.HelmRepositoryProxyRouting.HTTPURL != "http://proxy.internal:3128" || cfg.HelmRepositoryProxyRouting.HTTPSURL != "https://secure-proxy.internal:8443" || cfg.HelmRepositoryProxyRouting.NoProxy != ".svc,.cluster.local" {
		t.Fatal("standard proxy environment capture changed")
	}
	if err := normalizeHelmAndDeliveryTrust(cfg); err != nil {
		t.Fatal("configured private operator routing was rejected")
	}
	t.Setenv("HTTP_PROXY", "http://replacement.invalid:3128")
	if cfg.HelmRepositoryProxyRouting.HTTPURL != "http://proxy.internal:3128" {
		t.Fatal("captured route changed after configuration load")
	}
	cfg.HelmRepositoryProxyURL = "https://explicit-proxy.internal:8443"
	cfg.HelmRepositoryNoProxy = ""
	t.Setenv("HELM_REPOSITORY_NO_PROXY", "")
	t.Setenv("HELM_REPOSITORY_PROXY_URL", cfg.HelmRepositoryProxyURL)
	captureHelmOperatorProxy(cfg, false)
	loaded, err = Load()
	if err != nil || loaded.HelmRepositoryProxyRouting != cfg.HelmRepositoryProxyRouting {
		t.Fatal("real configuration load lost explicit empty bypass precedence")
	}
	if cfg.HelmRepositoryProxyRouting.URL != cfg.HelmRepositoryProxyURL || cfg.HelmRepositoryProxyRouting.HTTPURL != "" || cfg.HelmRepositoryProxyRouting.HTTPSURL != "" || cfg.HelmRepositoryProxyRouting.NoProxy != "" {
		t.Fatal("explicit Helm configuration lost precedence")
	}
	if cfg.DeliverySourceProxyURL != "" {
		t.Fatal("Helm routing populated unrelated delivery authority")
	}
}
