package handler

import "github.com/alphabravocompany/astronomer-go/internal/baseline"

// baselineComponentCatalogItem is the handler-side projection of a
// baseline.Component. Both this catalog and the server's delivery components are
// now derived from the single baseline.Registry seam, so the DefaultEnabled set
// (which components ride the ApplicationSet path vs. the opt-in tool_operations
// path) is defined once instead of hand-kept in sync across packages.
type baselineComponentCatalogItem struct {
	Slug               string
	Name               string
	Namespace          string
	ApplicationSetName string
	ApplicationPrefix  string
	DefaultEnabled     bool
}

var platformBaselineComponentCatalog = baselineComponentCatalogFromRegistry()

func baselineComponentCatalogFromRegistry() []baselineComponentCatalogItem {
	out := make([]baselineComponentCatalogItem, 0, len(baseline.Registry))
	for _, c := range baseline.Registry {
		out = append(out, baselineComponentCatalogItem{
			Slug:               c.Slug,
			Name:               c.Name,
			Namespace:          c.Namespace,
			ApplicationSetName: c.ApplicationSetName,
			ApplicationPrefix:  c.ApplicationPrefix,
			DefaultEnabled:     c.DefaultEnabled,
		})
	}
	return out
}

// baselineComponentOwnership lists the baseline components Astronomer
// auto-manages on adopted clusters — only the DefaultEnabled set (the two
// metrics exporters). The opt-in components (trivy, fluent-bit, ingress-nginx,
// cert-manager, gatekeeper) are installed per-cluster from the Tools view and
// are NOT global baseline appsets, so the cluster page never claims to deploy
// them. The full catalog is retained for orphan/ownership detection elsewhere.
func baselineComponentOwnership(managedBy string) []ClusterBaselineComponentOwner {
	if managedBy == "" {
		managedBy = "unknown"
	}
	components := make([]ClusterBaselineComponentOwner, 0, len(platformBaselineComponentCatalog))
	for _, item := range platformBaselineComponentCatalog {
		if !item.DefaultEnabled {
			continue
		}
		components = append(components, ClusterBaselineComponentOwner{
			Slug:               item.Slug,
			Name:               item.Name,
			Namespace:          item.Namespace,
			ApplicationSetName: item.ApplicationSetName,
			ManagedBy:          managedBy,
		})
	}
	return components
}
