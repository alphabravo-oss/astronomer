package config

import (
	"os"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// Called only by Load, after Viper resolves the explicit Helm configuration.
// Standard operator routes are captured once, never inferred as origin/CIDR
// authority. An explicitly empty Helm bypass has precedence over NO_PROXY.
func captureHelmOperatorProxy(cfg *Config, explicitBypass bool) {
	// Viper normally ignores empty environment values. LookupEnv retains the
	// explicit empty Helm bypass override rather than silently inheriting NO_PROXY.
	if value, configured := os.LookupEnv("HELM_REPOSITORY_NO_PROXY"); configured {
		cfg.HelmRepositoryNoProxy = value
		explicitBypass = true
	}
	var bypass *string
	if explicitBypass {
		bypass = &cfg.HelmRepositoryNoProxy
	}
	cfg.HelmRepositoryProxyRouting = httpclient.CaptureOperatorProxyRouting(cfg.HelmRepositoryProxyURL, bypass,
		httpclient.ProxyRouting{HTTPURL: os.Getenv("HTTP_PROXY"), HTTPSURL: os.Getenv("HTTPS_PROXY"), NoProxy: os.Getenv("NO_PROXY")},
		httpclient.ProxyRouting{HTTPURL: os.Getenv("http_proxy"), HTTPSURL: os.Getenv("https_proxy"), NoProxy: os.Getenv("no_proxy")})
}
