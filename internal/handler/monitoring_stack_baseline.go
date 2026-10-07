package handler

import (
	"context"

	"github.com/google/uuid"
)

type metricsBaselineOwnership struct {
	Detected   bool     `json:"detected"`
	Mode       string   `json:"mode"`
	Components []string `json:"components"`
	Message    string   `json:"message,omitempty"`
}

func (h *MonitoringHandler) clusterMetricsBaselineOwnership(ctx context.Context, clusterID string) metricsBaselineOwnership {
	ownership := metricsBaselineOwnership{
		Mode:       "full_stack",
		Components: []string{},
	}
	reader, ok := h.queries.(monitoringClusterIdentityReader)
	if !ok {
		return ownership
	}
	id, err := uuid.Parse(clusterID)
	if err != nil {
		return ownership
	}
	cluster, err := reader.GetClusterByID(ctx, id)
	if err != nil || !cluster.InstallBaseline.Valid || !cluster.InstallBaseline.Bool {
		return ownership
	}
	ownership.Detected = true
	ownership.Mode = "reuse"
	ownership.Components = []string{"kube-state-metrics", "prometheus-node-exporter"}
	ownership.Message = "Quick Start exporters stay Flux-managed; the full stack disables its duplicate exporters and scrapes the existing services."
	return ownership
}

func baselineMetricsServiceMonitors() []any {
	return []any{
		map[string]any{
			"name":              "astronomer-baseline-kube-state-metrics",
			"namespaceSelector": map[string]any{"matchNames": []string{"astronomer-monitoring"}},
			"selector": map[string]any{"matchLabels": map[string]any{
				"app.kubernetes.io/instance": "kube-state-metrics",
				"app.kubernetes.io/name":     "kube-state-metrics",
			}},
			"endpoints": []any{map[string]any{"port": "http", "path": "/metrics", "honorLabels": true}},
		},
		map[string]any{
			"name":              "astronomer-baseline-node-exporter",
			"namespaceSelector": map[string]any{"matchNames": []string{"astronomer-monitoring"}},
			"selector": map[string]any{"matchLabels": map[string]any{
				"app.kubernetes.io/instance": "prometheus-node-exporter",
				"app.kubernetes.io/name":     "prometheus-node-exporter",
			}},
			"endpoints": []any{map[string]any{"port": "metrics", "path": "/metrics", "honorLabels": true}},
		},
	}
}
