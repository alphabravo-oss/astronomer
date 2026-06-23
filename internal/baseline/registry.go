// Package baseline is the single source of truth for the platform baseline
// component catalog and the lifecycle path each component is delivered through.
//
// Baseline tools used to be described in two parallel catalogs that had to be
// hand-kept in sync (a drift hazard called out in the old code comments):
//
//   - server.fallbackBaselineApplicationSetComponents — what is actually
//     auto-delivered as a global ArgoCD ApplicationSet.
//   - handler.platformBaselineComponentCatalog — the full catalog used for
//     ownership / orphan detection and the cluster UI.
//
// This package collapses the "which slug, and how is it delivered" decision
// into one registry so both packages derive from a single seam. The routing is
// expressed by DeliveryPath: DefaultEnabled components ride the ArgoCD baseline
// ApplicationSet lifecycle; everything else is opt-in and delivered per cluster
// through the tool_operations path (the Tools view).
package baseline

// Path is the lifecycle path a baseline component is delivered through.
type Path string

const (
	// PathApplicationSet means the component is auto-managed on every adopted
	// cluster as a global ArgoCD ApplicationSet.
	PathApplicationSet Path = "applicationset"
	// PathToolOperation means the component is opt-in and installed per cluster
	// through the tool_operations helm path (the Tools view).
	PathToolOperation Path = "tool_operation"
)

// Component is the canonical description of a baseline platform component.
// Fields are the union of what both the server delivery path and the handler
// ownership/UI path need; consumers project it onto their own structs.
type Component struct {
	Slug               string
	Name               string
	Namespace          string
	ApplicationSetName string
	ApplicationPrefix  string
	ChartName          string
	RepoURL            string
	ValuesYAML         string
	// DefaultEnabled drives DeliveryPath: only the two metrics exporters ship on
	// by default and are auto-delivered as baseline ApplicationSets. Everything
	// else is opt-in via the tool_operations path.
	DefaultEnabled bool
}

// DeliveryPath returns the lifecycle path the component is delivered through.
func (c Component) DeliveryPath() Path {
	if c.DefaultEnabled {
		return PathApplicationSet
	}
	return PathToolOperation
}

// Registry is the single ordered catalog of baseline components.
var Registry = []Component{
	{
		Slug:               "trivy-operator",
		Name:               "Trivy Operator",
		Namespace:          "trivy-system",
		ApplicationSetName: "astronomer-baseline-trivy",
		ApplicationPrefix:  "astronomer-trivy",
	},
	{
		Slug:               "kube-state-metrics",
		Name:               "kube-state-metrics",
		Namespace:          "monitoring",
		ApplicationSetName: "astronomer-baseline-kube-state-metrics",
		ApplicationPrefix:  "astronomer-ksm",
		ChartName:          "kube-state-metrics",
		RepoURL:            "https://prometheus-community.github.io/helm-charts",
		ValuesYAML:         "metricLabelsAllowlist:\n  - pods=[*]\n  - deployments=[*]\n",
		DefaultEnabled:     true,
	},
	{
		Slug:               "prometheus-node-exporter",
		Name:               "Prometheus Node Exporter",
		Namespace:          "monitoring",
		ApplicationSetName: "astronomer-baseline-node-exporter",
		ApplicationPrefix:  "astronomer-node-exporter",
		ChartName:          "prometheus-node-exporter",
		RepoURL:            "https://prometheus-community.github.io/helm-charts",
		ValuesYAML:         "hostRootFsMount:\n  enabled: true\n",
		DefaultEnabled:     true,
	},
	{
		Slug:               "fluent-bit",
		Name:               "Fluent Bit",
		Namespace:          "logging",
		ApplicationSetName: "astronomer-baseline-fluent-bit",
		ApplicationPrefix:  "astronomer-fluent-bit",
	},
	{
		Slug:               "ingress-nginx",
		Name:               "ingress-nginx",
		Namespace:          "ingress-nginx",
		ApplicationSetName: "astronomer-baseline-ingress-nginx",
		ApplicationPrefix:  "astronomer-ingress-nginx",
	},
	{
		Slug:               "cert-manager",
		Name:               "cert-manager",
		Namespace:          "cert-manager",
		ApplicationSetName: "astronomer-baseline-cert-manager",
		ApplicationPrefix:  "astronomer-cert-manager",
	},
	{
		Slug:               "gatekeeper",
		Name:               "Gatekeeper",
		Namespace:          "gatekeeper-system",
		ApplicationSetName: "astronomer-baseline-gatekeeper",
		ApplicationPrefix:  "astronomer-gatekeeper",
	},
}

// ApplicationSetComponents returns the components routed to the ArgoCD baseline
// ApplicationSet lifecycle (the DefaultEnabled set), in catalog order.
func ApplicationSetComponents() []Component {
	out := make([]Component, 0, len(Registry))
	for _, c := range Registry {
		if c.DeliveryPath() == PathApplicationSet {
			out = append(out, c)
		}
	}
	return out
}
