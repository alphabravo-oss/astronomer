package builtinbundles

// ImageScanningAnnotation is the canonical per-cluster registration preference.
// Absence means enabled, independently of the metrics baseline. Changing
// it after registration does not uninstall an existing Flux-managed scanner.
const ImageScanningAnnotation = "astronomer.io/image-scanning"

// ForRegistration returns an independent catalog with the explicit scanner
// opt-out applied. All other baseline components retain their release defaults.
func (c Catalog) ForRegistration(installMetrics, imageScanningDisabled bool) Catalog {
	c.Components = append([]Component(nil), c.Components...)
	for i := range c.Components {
		if c.Components[i].Slug == "trivy-operator" {
			c.Components[i].DefaultEnabled = !imageScanningDisabled
		} else {
			c.Components[i].DefaultEnabled = c.Components[i].DefaultEnabled && installMetrics
		}
	}
	return c
}
