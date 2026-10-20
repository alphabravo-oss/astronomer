package server

import (
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerMonitoringRoutes(r chi.Router, deps RouterDependencies) {
	if deps.ClusterResources.Monitoring != nil {
		r.Post("/clusters/{cluster_id}/metrics/", deps.ClusterResources.Monitoring.PrometheusQuery)
		r.Get("/clusters/{cluster_id}/metrics/", deps.ClusterResources.Monitoring.ListMetrics)
		r.Get("/clusters/{cluster_id}/metrics/summary/", deps.ClusterResources.Monitoring.ListMetrics)
		r.Get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/metrics/", deps.ClusterResources.Monitoring.PrometheusQueryRange)
		// /api/v1/monitoring/endpoints/ ViewSet (CRUD on monitoring backends).
		r.With(featureGate("feature.monitoring", deps.CoreAuth.SettingsCache)).Route("/monitoring", func(r chi.Router) {
			r.Get("/endpoints/", deps.ClusterResources.Monitoring.ListEndpoints)
			r.Post("/endpoints/", deps.ClusterResources.Monitoring.CreateEndpoint)
			r.Get("/endpoints/{id}/", deps.ClusterResources.Monitoring.GetEndpoint)
			r.Put("/endpoints/{id}/", deps.ClusterResources.Monitoring.UpdateEndpoint)
			r.Delete("/endpoints/{id}/", deps.ClusterResources.Monitoring.DeleteEndpoint)
			// Legacy Python paths preserved as aliases that proxy to the cluster-scoped handlers.
			r.Post("/metrics/query/{cluster_id}/", deps.ClusterResources.Monitoring.LegacyMetricsQuery)
			r.Get("/metrics/cluster-overview/{cluster_id}/", deps.ClusterResources.Monitoring.LegacyClusterOverview)
			r.Get("/metrics/workload/{cluster_id}/{namespace}/{workload}/", deps.ClusterResources.Monitoring.LegacyWorkloadMetrics)
			r.Get("/metrics/node/{cluster_id}/{node}/", deps.ClusterResources.Monitoring.LegacyNodeMetrics)
		})
	}

}
