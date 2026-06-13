package handler

type baselineComponentCatalogItem struct {
	Slug               string
	Name               string
	Namespace          string
	ApplicationSetName string
}

var platformBaselineComponentCatalog = []baselineComponentCatalogItem{
	{
		Slug:               "trivy-operator",
		Name:               "Trivy Operator",
		Namespace:          "trivy-system",
		ApplicationSetName: "astronomer-baseline-trivy",
	},
	{
		Slug:               "kube-state-metrics",
		Name:               "kube-state-metrics",
		Namespace:          "monitoring",
		ApplicationSetName: "astronomer-baseline-kube-state-metrics",
	},
	{
		Slug:               "prometheus-node-exporter",
		Name:               "Prometheus Node Exporter",
		Namespace:          "monitoring",
		ApplicationSetName: "astronomer-baseline-node-exporter",
	},
	{
		Slug:               "fluent-bit",
		Name:               "Fluent Bit",
		Namespace:          "logging",
		ApplicationSetName: "astronomer-baseline-fluent-bit",
	},
	{
		Slug:               "cert-manager",
		Name:               "cert-manager",
		Namespace:          "cert-manager",
		ApplicationSetName: "astronomer-baseline-cert-manager",
	},
}

func baselineComponentOwnership(managedBy string) []ClusterBaselineComponentOwner {
	if managedBy == "" {
		managedBy = "unknown"
	}
	components := make([]ClusterBaselineComponentOwner, 0, len(platformBaselineComponentCatalog))
	for _, item := range platformBaselineComponentCatalog {
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
