package server

import (
	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerClusterRoutes(r chi.Router, deps RouterDependencies) {
	writeClusters := requireScope(iauth.ScopeWriteClusters)

	if deps.Clusters != nil {
		r.Route("/clusters", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbList)).Get("/", deps.Clusters.List)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbCreate)).Post("/", deps.Clusters.Create)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/", deps.Clusters.Get)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/", deps.Clusters.Update)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Patch("/{id}/", deps.Clusters.Update)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/ownership/takeover/", deps.Clusters.TakeoverOwnership)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbDelete)).Delete("/{id}/", deps.Clusters.Delete)
			// Cluster decommission status — poll endpoint paired with the
			// DELETE handler's 202 Accepted response. Returns the latest
			// cluster_decommissions row's phase progress so the operator can
			// follow the reconciler.
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/decommission/", deps.Clusters.GetDecommission)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/health/", deps.Clusters.GetHealth)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/conditions/", deps.Clusters.ListConditions)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/condition-remediation/", deps.Clusters.ListConditionRemediation)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/register/", deps.Clusters.GenerateRegistrationToken)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/registry/", deps.Clusters.GetRegistryConfig)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/registry/", deps.Clusters.UpdateRegistryConfig)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/{id}/registry/", deps.Clusters.DeleteRegistryConfig)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/manifest/", deps.Clusters.GetManifest)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/kubeconfig/", deps.Clusters.GetKubeconfig)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Post("/{id}/generate-kubeconfig/", deps.Clusters.GenerateKubeconfig)
			// Underscore alias the Next.js frontend currently calls. Both shapes
			// route to the same handler so older callers keep working.
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Post("/{id}/generate_kubeconfig/", deps.Clusters.GenerateKubeconfig)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/kubeconfig-preview/", deps.Clusters.PreviewKubeconfig)
			// Serve the cluster-detail metrics charts from the Monitoring handler,
			// which returns real Prometheus time-series (with a synthetic-series
			// fallback when no backend is configured) in the shape the charts
			// expect. The scalar Clusters.GetMetrics returned no series, so the
			// charts rendered empty. Fall back to it only if Monitoring is unwired.
			clusterMetricsHandler := deps.Clusters.GetMetrics
			clusterMetricsSummaryHandler := deps.Clusters.GetMetricsSummary
			if deps.Monitoring != nil {
				clusterMetricsHandler = deps.Monitoring.ListMetrics
				clusterMetricsSummaryHandler = deps.Monitoring.ListMetrics
			}
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/metrics/", clusterMetricsHandler)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/metrics/summary/", clusterMetricsSummaryHandler)
			// Wizard endpoints — migration 078 / sprint 22.
			if deps.ClusterRegistration != nil {
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/registration/status/", deps.ClusterRegistration.GetStatus)
				r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/registration/options/", deps.ClusterRegistration.PutOptions)
				r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/registration/confirm/", deps.ClusterRegistration.PostConfirm)
				r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/registration/retry/{step_id}/", deps.ClusterRegistration.PostRetry)
				r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/registration/cancel/", deps.ClusterRegistration.PostCancel)
			}
			if deps.Monitoring != nil {
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/monitoring/config/", deps.Monitoring.GetClusterConfig)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Put("/{id}/monitoring/config/", deps.Monitoring.UpdateClusterConfig)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/monitoring/stack/status/", deps.Monitoring.GetStackStatus)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Post("/{id}/monitoring/stack/preview/", deps.Monitoring.PreviewStack)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbCreate)).Post("/{id}/monitoring/stack/install/", deps.Monitoring.InstallStack)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Put("/{id}/monitoring/stack/upgrade/", deps.Monitoring.UpgradeStack)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Post("/{id}/monitoring/stack/replace/", deps.Monitoring.ReplaceStack)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceMonitoring, rbac.VerbDelete)).Delete("/{id}/monitoring/stack/uninstall/", deps.Monitoring.UninstallStack)
			}
		})
	}

}
