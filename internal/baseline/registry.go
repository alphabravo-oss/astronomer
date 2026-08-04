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
	// ChartVersion is the compiled-in floor pin for the ApplicationSet path.
	// ArgoCD reads a Helm `targetRevision` as a semver CONSTRAINT, so an exact
	// version here means exactly that version and nothing else. It exists so a
	// baseline component can never resolve to "whatever upstream published
	// last" — the operator-facing pin is cluster_tools (charts[].version, else
	// version_constraint; migration 143 seeds it), and this is the last-resort
	// default when that row is absent or blank.
	//
	// The shipped values are the versions the old "*" resolved to on
	// 2026-07-28, deliberately: pinning must change the MECHANISM without
	// moving already-deployed state, and prune+selfHeal are on, so pinning to
	// anything older would roll every adopted cluster backwards on the next
	// reconcile. Bumping these is an ordinary reviewed code change.
	ChartVersion string
	ValuesYAML   string
	// DefaultEnabled drives DeliveryPath: only the two metrics exporters ship on
	// by default and are auto-delivered as baseline ApplicationSets. Everything
	// else is opt-in via the tool_operations path.
	DefaultEnabled bool
	// RequiresClusterRBAC marks a component whose chart creates cluster-scoped
	// RBAC (ClusterRole/ClusterRoleBinding). ArgoCD applies the baseline through
	// the managed cluster's AGENT SA, and the `operator` profile is read-only on
	// cluster-scoped RBAC (get/list/watch) — only `admin` can create it. So a
	// RequiresClusterRBAC component targeted at an operator cluster would install
	// its namespaced bits but leave the ClusterRole/Binding permanently
	// OutOfSync. The baseline ApplicationSet narrows such components to `admin`
	// only (node-exporter and the rest stay operator+admin). ponytail: set it
	// wherever it's verified true; kube-state-metrics is the one confirmed case.
	RequiresClusterRBAC bool
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
		Namespace:          "astronomer-trivy-system",
		ApplicationSetName: "astronomer-baseline-trivy",
		ApplicationPrefix:  "astronomer-trivy",
		// trivy-operator ships a cluster-scoped ClusterRole+ClusterRoleBinding
		// (it scans workloads across every namespace) plus its report CRDs — the
		// operator agent SA can't create those, so scope it to admin only.
		RequiresClusterRBAC: true,
	},
	{
		Slug:               "kube-state-metrics",
		Name:               "kube-state-metrics",
		Namespace:          "astronomer-monitoring",
		ApplicationSetName: "astronomer-baseline-kube-state-metrics",
		ApplicationPrefix:  "astronomer-ksm",
		ChartName:          "kube-state-metrics",
		RepoURL:            "https://prometheus-community.github.io/helm-charts",
		ChartVersion:       "8.0.0",
		ValuesYAML:         "metricLabelsAllowlist:\n  - pods=[*]\n  - deployments=[*]\n",
		DefaultEnabled:     true,
		// ksm ships a cluster-scoped ClusterRole+ClusterRoleBinding so it can read
		// every namespace — the operator agent SA can't create those, so scope
		// this component to admin-profile clusters only.
		RequiresClusterRBAC: true,
	},
	{
		Slug:               "prometheus-node-exporter",
		Name:               "Prometheus Node Exporter",
		Namespace:          "astronomer-monitoring",
		ApplicationSetName: "astronomer-baseline-node-exporter",
		ApplicationPrefix:  "astronomer-node-exporter",
		ChartName:          "prometheus-node-exporter",
		RepoURL:            "https://prometheus-community.github.io/helm-charts",
		ChartVersion:       "4.56.1",
		ValuesYAML:         "hostRootFsMount:\n  enabled: true\n",
		DefaultEnabled:     true,
	},
	{
		Slug:               "fluent-bit",
		Name:               "Fluent Bit",
		Namespace:          "astronomer-logging",
		ApplicationSetName: "astronomer-baseline-fluent-bit",
		ApplicationPrefix:  "astronomer-fluent-bit",
	},
	{
		Slug:               "ingress-nginx",
		Name:               "ingress-nginx",
		Namespace:          "astronomer-ingress-nginx",
		ApplicationSetName: "astronomer-baseline-ingress-nginx",
		ApplicationPrefix:  "astronomer-ingress-nginx",
		// ingress-nginx ships a cluster-scoped ClusterRole+ClusterRoleBinding (it
		// watches Ingress resources across every namespace) plus its admission
		// ValidatingWebhookConfiguration — cluster-scoped, so scope to admin only.
		RequiresClusterRBAC: true,
	},
	{
		Slug:               "cert-manager",
		Name:               "cert-manager",
		Namespace:          "astronomer-cert-manager",
		ApplicationSetName: "astronomer-baseline-cert-manager",
		ApplicationPrefix:  "astronomer-cert-manager",
		// cert-manager ships cluster-scoped ClusterRoles+ClusterRoleBindings for
		// its controller/cainjector/webhook plus mutating+validating webhook
		// configs — the operator agent SA can't create those, so admin only.
		RequiresClusterRBAC: true,
	},
	{
		Slug:               "gatekeeper",
		Name:               "Gatekeeper",
		Namespace:          "astronomer-gatekeeper-system",
		ApplicationSetName: "astronomer-baseline-gatekeeper",
		ApplicationPrefix:  "astronomer-gatekeeper",
		// gatekeeper ships a cluster-scoped ClusterRole+ClusterRoleBinding plus
		// mutating+validating admission webhook configs — cluster-scoped, so the
		// operator agent SA can't create them; scope to admin only.
		RequiresClusterRBAC: true,
	},
}

// ComponentBySlug returns the registry Component with the given slug. The
// second return is false when no component matches, so callers can distinguish
// a genuine miss from the zero-value Component (whose RequiresClusterRBAC would
// otherwise read as false and silently skip the cluster-RBAC pre-flight).
func ComponentBySlug(slug string) (Component, bool) {
	for _, c := range Registry {
		if c.Slug == slug {
			return c, true
		}
	}
	return Component{}, false
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
