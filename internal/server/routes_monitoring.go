package server

import (
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerMonitoringRoutes(r chi.Router, deps RouterDependencies) {
	if deps.Monitoring != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Post("/clusters/{cluster_id}/metrics/", deps.Monitoring.PrometheusQuery)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/clusters/{cluster_id}/metrics/", deps.Monitoring.ListMetrics)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/clusters/{cluster_id}/metrics/summary/", deps.Monitoring.ListMetrics)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/metrics/", deps.Monitoring.PrometheusQueryRange)
		// /api/v1/monitoring/endpoints/ ViewSet (CRUD on monitoring backends).
		r.With(featureGate("feature.monitoring", deps.SettingsCache)).Route("/monitoring", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbList)).Get("/endpoints/", deps.Monitoring.ListEndpoints)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbCreate)).Post("/endpoints/", deps.Monitoring.CreateEndpoint)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/endpoints/{id}/", deps.Monitoring.GetEndpoint)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Put("/endpoints/{id}/", deps.Monitoring.UpdateEndpoint)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbDelete)).Delete("/endpoints/{id}/", deps.Monitoring.DeleteEndpoint)
			// Legacy Python paths preserved as aliases that proxy to the cluster-scoped handlers.
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Post("/metrics/query/{cluster_id}/", deps.Monitoring.LegacyMetricsQuery)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/metrics/cluster-overview/{cluster_id}/", deps.Monitoring.LegacyClusterOverview)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/metrics/workload/{cluster_id}/{namespace}/{workload}/", deps.Monitoring.LegacyWorkloadMetrics)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/metrics/node/{cluster_id}/{node}/", deps.Monitoring.LegacyNodeMetrics)
		})
	}

}
