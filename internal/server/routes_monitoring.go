package server

import (
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerMonitoringRoutes(r chi.Router, deps RouterDependencies) {
	if deps.ClusterResources.Monitoring != nil {
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Post("/clusters/{cluster_id}/metrics/", deps.ClusterResources.Monitoring.PrometheusQuery)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/clusters/{cluster_id}/metrics/", deps.ClusterResources.Monitoring.ListMetrics)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/clusters/{cluster_id}/metrics/summary/", deps.ClusterResources.Monitoring.ListMetrics)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/metrics/", deps.ClusterResources.Monitoring.PrometheusQueryRange)
		// /api/v1/monitoring/endpoints/ ViewSet (CRUD on monitoring backends).
		r.With(featureGate("feature.monitoring", deps.CoreAuth.SettingsCache)).Route("/monitoring", func(r chi.Router) {
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbList)).Get("/endpoints/", deps.ClusterResources.Monitoring.ListEndpoints)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbCreate)).Post("/endpoints/", deps.ClusterResources.Monitoring.CreateEndpoint)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/endpoints/{id}/", deps.ClusterResources.Monitoring.GetEndpoint)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Put("/endpoints/{id}/", deps.ClusterResources.Monitoring.UpdateEndpoint)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbDelete)).Delete("/endpoints/{id}/", deps.ClusterResources.Monitoring.DeleteEndpoint)
			// Legacy Python paths preserved as aliases that proxy to the cluster-scoped handlers.
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Post("/metrics/query/{cluster_id}/", deps.ClusterResources.Monitoring.LegacyMetricsQuery)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/metrics/cluster-overview/{cluster_id}/", deps.ClusterResources.Monitoring.LegacyClusterOverview)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/metrics/workload/{cluster_id}/{namespace}/{workload}/", deps.ClusterResources.Monitoring.LegacyWorkloadMetrics)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/metrics/node/{cluster_id}/{node}/", deps.ClusterResources.Monitoring.LegacyNodeMetrics)
		})
	}

}
