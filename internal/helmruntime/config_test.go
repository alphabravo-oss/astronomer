package helmruntime

import "testing"

func TestSettingsUsesInjectedContract(t *testing.T) {
	t.Setenv("HELM_KUBECONTEXT", "ambient-context")
	t.Setenv("HELM_REGISTRY_CONFIG", "/ambient/registry.json")
	t.Setenv("HELM_CONTENT_CACHE", "/ambient/content")
	t.Setenv("HELM_COLOR", "always")

	cfg := InClusterDefaults()
	cfg.KubeContext = "injected-context"
	cfg.RegistryConfig = "/injected/registry.json"
	cfg.ContentCache = "/injected/content"
	cfg.ColorMode = "never"
	settings := cfg.Settings()

	if settings.KubeContext != "injected-context" {
		t.Fatalf("KubeContext = %q, want injected-context", settings.KubeContext)
	}
	if settings.RegistryConfig != "/injected/registry.json" {
		t.Fatalf("RegistryConfig = %q, want injected path", settings.RegistryConfig)
	}
	if settings.ContentCache != cfg.ContentCache || settings.ColorMode != cfg.ColorMode {
		t.Fatal("Helm4 cache/color escaped typed injected contract")
	}
}
