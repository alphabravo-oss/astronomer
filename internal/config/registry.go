package config

import (
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/registry"
)

// RegistryConfig returns the validated layered-registry configuration.
func (c *Config) RegistryConfig() (registry.Config, error) {
	fallbacks, err := registry.ParseList(c.RegistryFallbacks)
	if err != nil {
		return registry.Config{}, fmt.Errorf("REGISTRY_FALLBACKS: %w", err)
	}
	local := strings.TrimSpace(c.RegistryLocal)
	if local != "" {
		if err := registry.ValidateBase(local); err != nil {
			return registry.Config{}, fmt.Errorf("REGISTRY_LOCAL: %w", err)
		}
	}
	if len(fallbacks) > 0 && local == "" {
		return registry.Config{}, fmt.Errorf("REGISTRY_FALLBACKS requires REGISTRY_LOCAL")
	}
	var plain []string
	for host := range strings.SplitSeq(c.RegistryPlainHTTP, ",") {
		if host = strings.TrimSpace(host); host != "" {
			plain = append(plain, host)
		}
	}
	return registry.Config{Local: local, Fallbacks: fallbacks, PlainHTTP: plain}, nil
}

// RuntimeImageRefs names every image/artifact reference taken from process
// configuration that the server or worker renders or pulls. It is the single
// list the registry policy is applied to and the startup resolution check
// walks.
func (c *Config) RuntimeImageRefs() map[string]*string {
	refs := map[string]*string{
		"agent_image":                  &c.AgentImageRepository,
		"kubectl_shell_image":          &c.KubectlShellImage,
		"management_backup_image":      &c.ManagementBackupImage,
		"flux_distribution_repository": &c.DeliveryFluxDistributionRepository,
		"bundle_repository":            &c.DeliveryBundleRepository,
	}
	if strings.HasPrefix(c.CatalogURL, "oci://") {
		refs["catalog_source"] = &c.CatalogURL
	}
	return refs
}

// applyRegistryPolicy validates REGISTRY_* and re-hosts every configured
// runtime reference onto the cluster-local registry. With no local registry
// (the online profile) references are left exactly as configured.
func applyRegistryPolicy(cfg *Config) error {
	rc, err := cfg.RegistryConfig()
	if err != nil {
		return err
	}
	if rc.Local == "" {
		return nil
	}
	for name, ref := range cfg.RuntimeImageRefs() {
		if *ref == "" || registry.IsLocal(*ref, rc.Local) {
			continue
		}
		localized, err := registry.Rehost(*ref, rc.Local)
		if err != nil {
			return fmt.Errorf("registry policy: %s: %w", name, err)
		}
		*ref = localized
	}
	return nil
}
