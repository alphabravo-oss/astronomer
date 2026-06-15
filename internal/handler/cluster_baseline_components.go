package handler

import (
	"context"
	"encoding/json"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type baselineComponentCatalogItem struct {
	Slug               string
	Name               string
	Namespace          string
	ApplicationSetName string
	ApplicationPrefix  string
	// DefaultEnabled mirrors the server-side baseline policy: only the two
	// metrics exporters (kube-state-metrics, prometheus-node-exporter) ship on
	// by default; everything else is opt-in. Kept in sync with
	// fallbackBaselineApplicationSetComponents in
	// internal/server/baseline_appsets.go.
	DefaultEnabled bool
}

var platformBaselineComponentCatalog = []baselineComponentCatalogItem{
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
		DefaultEnabled:     true,
	},
	{
		Slug:               "prometheus-node-exporter",
		Name:               "Prometheus Node Exporter",
		Namespace:          "monitoring",
		ApplicationSetName: "astronomer-baseline-node-exporter",
		ApplicationPrefix:  "astronomer-node-exporter",
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

type baselineSettingReader interface {
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
}

// baselineComponentEnabledForCluster reports whether a baseline component is
// installed on adopted clusters: the per-component platform setting
// argocd.baseline.<slug> overrides, otherwise its DefaultEnabled. Mirrors
// server.baselineComponentEnabled so the UI shows exactly what's provisioned.
func baselineComponentEnabledForCluster(ctx context.Context, settings baselineSettingReader, item baselineComponentCatalogItem) bool {
	if settings == nil {
		return item.DefaultEnabled
	}
	row, err := settings.GetPlatformSetting(ctx, "argocd.baseline."+item.Slug)
	if err != nil || len(row.Value) == 0 {
		return item.DefaultEnabled
	}
	var enabled bool
	if err := json.Unmarshal(row.Value, &enabled); err != nil {
		return item.DefaultEnabled
	}
	return enabled
}

// baselineComponentOwnership lists only the baseline components actually managed
// on the cluster — the opt-in infra (ingress-nginx, cert-manager, gatekeeper) is
// excluded unless explicitly enabled, so the UI doesn't claim to deploy what it
// doesn't. settings may be nil (falls back to the default-on set).
func baselineComponentOwnership(ctx context.Context, settings baselineSettingReader, managedBy string) []ClusterBaselineComponentOwner {
	if managedBy == "" {
		managedBy = "unknown"
	}
	components := make([]ClusterBaselineComponentOwner, 0, len(platformBaselineComponentCatalog))
	for _, item := range platformBaselineComponentCatalog {
		if !baselineComponentEnabledForCluster(ctx, settings, item) {
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
