package server

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func TestHelmRuntimeInjectsCapturedProxyRoutesWithoutAmbientReads(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://ambient.invalid:3128")
	cfg := &config.Config{HelmRepositoryProxyRouting: httpclient.ProxyRouting{HTTPURL: "http://captured-http.internal:3128", HTTPSURL: "https://captured-https.internal:8443", NoProxy: ".svc"}}
	runtime := helmRuntimeFromConfig(cfg)
	if runtime.RepositoryProxyRouting != cfg.HelmRepositoryProxyRouting || runtime.RepositoryProxyURL != "" {
		t.Fatal("server/Charlie/embedded-agent proxy injection changed")
	}
	cfg.HelmRepositoryProxyRouting.HTTPURL = "http://replacement.invalid:3128"
	if runtime.RepositoryProxyRouting.HTTPURL != "http://captured-http.internal:3128" {
		t.Fatal("caller mutation widened injected proxy routes")
	}
}
