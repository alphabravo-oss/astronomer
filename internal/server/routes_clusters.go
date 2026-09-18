package server

import (
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerClusterRoutes(r chi.Router, deps RouterDependencies) {
	writeClusters := requireScope(iauth.ScopeWriteClusters)

	if deps.ClusterResources.Clusters != nil {
		r.Route("/clusters", func(r chi.Router) {
			// Every {id} below is a CLUSTER id. Say so, once, for the whole
			// subtree: the permission gates cannot infer it, and the ones that
			// check a resource other than clusters — GET /{id}/health/ and the
			// eight /{id}/monitoring/... routes, all rbac.ResourceMonitoring —
			// were otherwise evaluated at uuid.Nil, i.e. as a GLOBAL check, which
			// refused a caller whose monitoring grant is scoped to this very
			// cluster. A global monitoring grant reached every cluster before
			// this line and still does — that is what global means; see
			// appmiddleware.ClusterScopeFromIDParam. Must precede the route
			// registrations below (chi rejects Use after Handle).
			r.Use(appmiddleware.ClusterScopeFromIDParam)
			// Collection gate: cluster-scoped callers are admitted and the
			// handler filters the page to their clusters. See
			// RequireCollectionPermission.
			r.With(requireCollectionPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbList)).Get("/summary/", deps.ClusterResources.Clusters.Summary)
			r.With(requireCollectionPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbList)).Get("/", deps.ClusterResources.Clusters.List)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbCreate)).Post("/", deps.ClusterResources.Clusters.Create)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/", deps.ClusterResources.Clusters.Get)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/", deps.ClusterResources.Clusters.Update)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Patch("/{id}/", deps.ClusterResources.Clusters.Update)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/ownership/takeover/", deps.ClusterResources.Clusters.TakeoverOwnership)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbDelete)).Delete("/{id}/", deps.ClusterResources.Clusters.Delete)
			// Cluster decommission status — poll endpoint paired with the
			// DELETE handler's 202 Accepted response. Returns the latest
			// cluster_decommissions row's phase progress so the operator can
			// follow the reconciler.
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/decommission/", deps.ClusterResources.Clusters.GetDecommission)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/health/", deps.ClusterResources.Clusters.GetHealth)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/conditions/", deps.ClusterResources.Clusters.ListConditions)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/condition-remediation/", deps.ClusterResources.Clusters.ListConditionRemediation)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/register/", deps.ClusterResources.Clusters.GenerateRegistrationToken)
			// Durable agent-token lifecycle (task A2). Rotate triggers a
			// grace rotation on the agent's next connect; Revoke denies the
			// token outright (operator must re-import). Both gated
			// writeClusters + VerbUpdate, audited.
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/agent-token/rotate/", deps.ClusterResources.Clusters.RotateAgentToken)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/agent-token/revoke/", deps.ClusterResources.Clusters.RevokeAgentToken)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/registry/", deps.ClusterResources.Clusters.GetRegistryConfig)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/registry/", deps.ClusterResources.Clusters.UpdateRegistryConfig)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/{id}/registry/", deps.ClusterResources.Clusters.DeleteRegistryConfig)
			// D3 (H3): GET /manifest/ MINTS a live 1h registration token as a
			// side effect (body + X-Astronomer-Registration-Token header), so it
			// is a credential-issuing write, not a read — gate it like POST
			// /register/ (writeClusters + VerbUpdate) to close the read→credential
			// escalation. Read-only callers that only want to preview the manifest
			// shape use the placeholder paths, which persist no usable token.
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/{id}/manifest/", deps.ClusterResources.Clusters.GetManifest)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Post("/{id}/generate-kubeconfig/", deps.ClusterResources.Clusters.GenerateKubeconfig)
			// A portable member-cluster credential bypasses the Astronomer proxy
			// after download. Require cluster update authority and the write/CSRF
			// gate even though the issued Kubernetes identity is read-only.
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/generate-direct-kubeconfig/", deps.ClusterResources.Clusters.GenerateDirectKubeconfig)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/kubeconfig-preview/", deps.ClusterResources.Clusters.PreviewKubeconfig)
			// Serve the cluster-detail metrics charts from the Monitoring handler,
			// which returns real Prometheus time-series (with a synthetic-series
			// fallback when no backend is configured) in the shape the charts
			// expect. The scalar Clusters.GetMetrics returned no series, so the
			// charts rendered empty. Fall back to it only if Monitoring is unwired.
			clusterMetricsHandler := deps.ClusterResources.Clusters.GetMetrics
			clusterMetricsSummaryHandler := deps.ClusterResources.Clusters.GetMetricsSummary
			if deps.ClusterResources.Monitoring != nil {
				clusterMetricsHandler = deps.ClusterResources.Monitoring.ListMetrics
				clusterMetricsSummaryHandler = deps.ClusterResources.Monitoring.ListMetrics
			}
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/metrics/", clusterMetricsHandler)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/metrics/summary/", clusterMetricsSummaryHandler)
			// Wizard endpoints — migration 078 / sprint 22.
			if deps.ClusterResources.ClusterRegistration != nil {
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/{id}/registration/status/", deps.ClusterResources.ClusterRegistration.GetStatus)
				r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/registration/options/", deps.ClusterResources.ClusterRegistration.PutOptions)
				r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/registration/confirm/", deps.ClusterResources.ClusterRegistration.PostConfirm)
				r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/registration/retry/{step_id}/", deps.ClusterResources.ClusterRegistration.PostRetry)
				r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/registration/cancel/", deps.ClusterResources.ClusterRegistration.PostCancel)
			}
			if deps.ClusterResources.Logging != nil {
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceLogging, rbac.VerbRead)).Get("/{id}/logging/outputs/attach-astronomer/", deps.ClusterResources.Logging.GetAstronomerAttachStatus)
				r.With(writeClusters).Post("/{id}/logging/outputs/attach-astronomer/", deps.ClusterResources.Logging.AttachAstronomerLogs)
				r.With(writeClusters).Post("/{id}/logging/outputs/{output_id}/rotate-token/", deps.ClusterResources.Logging.RotateOutputToken)
			}
			if deps.ClusterResources.Monitoring != nil {
				clusterGrafana := http.HandlerFunc(deps.ClusterResources.Monitoring.ProxyClusterGrafana)
				for _, path := range []string{"/{id}/observability/grafana", "/{id}/observability/grafana/", "/{id}/observability/grafana/*"} {
					for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions} {
						r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Method(method, path, clusterGrafana)
					}
					for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
						r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Method(method, path, clusterGrafana)
					}
				}
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/monitoring/config/", deps.ClusterResources.Monitoring.GetClusterConfig)
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Put("/{id}/monitoring/config/", deps.ClusterResources.Monitoring.UpdateClusterConfig)
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Get("/{id}/monitoring/stack/status/", deps.ClusterResources.Monitoring.GetStackStatus)
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbRead)).Post("/{id}/monitoring/stack/preview/", deps.ClusterResources.Monitoring.PreviewStack)
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbCreate)).Post("/{id}/monitoring/stack/install/", deps.ClusterResources.Monitoring.InstallStack)
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Put("/{id}/monitoring/stack/upgrade/", deps.ClusterResources.Monitoring.UpgradeStack)
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbUpdate)).Post("/{id}/monitoring/stack/replace/", deps.ClusterResources.Monitoring.ReplaceStack)
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceMonitoring, rbac.VerbDelete)).Delete("/{id}/monitoring/stack/uninstall/", deps.ClusterResources.Monitoring.UninstallStack)
			}
		})
	}

}
