package helmruntime

import "testing"

func TestSettingsUsesInjectedContract(t *testing.T) {
	t.Setenv("HELM_KUBECONTEXT", "ambient-context")
	t.Setenv("HELM_REGISTRY_CONFIG", "/ambient/registry.json")

	cfg := InClusterDefaults()
	cfg.KubeContext = "injected-context"
	cfg.RegistryConfig = "/injected/registry.json"
	settings := cfg.Settings()

	if settings.KubeContext != "injected-context" {
		t.Fatalf("KubeContext = %q, want injected-context", settings.KubeContext)
	}
	if settings.RegistryConfig != "/injected/registry.json" {
		t.Fatalf("RegistryConfig = %q, want injected path", settings.RegistryConfig)
	}
}
