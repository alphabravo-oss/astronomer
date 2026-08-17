// Package baseline records security-relevant properties of optional platform
// tools. Delivery metadata belongs to immutable delivery bundles; this small
// registry exists only for preflight checks that must run before a Helm tool
// install is queued.
package baseline

type Component struct {
	Slug                string
	RequiresClusterRBAC bool
}

var Registry = []Component{
	{Slug: "trivy-operator", RequiresClusterRBAC: true},
	{Slug: "kube-state-metrics", RequiresClusterRBAC: true},
	{Slug: "prometheus-node-exporter"},
	{Slug: "fluent-bit"},
	{Slug: "ingress-nginx", RequiresClusterRBAC: true},
	{Slug: "cert-manager", RequiresClusterRBAC: true},
	{Slug: "gatekeeper", RequiresClusterRBAC: true},
}

func ComponentBySlug(slug string) (Component, bool) {
	for _, component := range Registry {
		if component.Slug == slug {
			return component, true
		}
	}
	return Component{}, false
}
