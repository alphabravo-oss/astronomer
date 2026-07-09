package server

import (
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// requireSuperuser returns middleware that admits only superusers, reusing the
// shared handler.RequireSuperuser gate. Used for platform-admin surfaces whose
// handlers carry no per-request authorization of their own (e.g. the
// control-plane controllers routes).
func requireSuperuser(deps RouterDependencies) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := handler.RequireSuperuser(w, r, deps.AuthQueries, handler.SuperuserGateConfig{
				ForbiddenMessage: "This action requires superuser privileges",
			}); !ok {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerToolsControlPlaneRoutes(r chi.Router, deps RouterDependencies) {
	mutationWriteScope := appmiddleware.RequireWriteScopeForMutations(iauth.ScopeWriteClusters)

	if deps.Tools != nil {
		r.Route("/tools", func(r chi.Router) {
			r.Get("/controller/status/", deps.Tools.ControllerStatus)
			r.Get("/operations/", deps.Tools.ListOperations)
			r.Get("/operations/{id}/", deps.Tools.GetOperation)
			r.Post("/operations/{id}/retry/", deps.Tools.RetryOperation)
			r.Get("/", deps.Tools.List)
			r.Get("/{id}/", deps.Tools.Get)
			r.Get("/slug/{slug}/", deps.Tools.GetBySlug)
			r.Get("/{slug:[^/]+}/", deps.Tools.GetBySlug)
			r.Post("/{slug}/preview/", deps.Tools.Preview)
			r.Post("/{slug}/install/", deps.Tools.Install)
			r.Put("/{slug}/upgrade/", deps.Tools.Upgrade)
			r.Delete("/{slug}/uninstall/", deps.Tools.Uninstall)
			r.Post("/{slug}/adopt/", deps.Tools.Adopt)
		})
		r.Get("/clusters/{cluster_id}/tools/status/", deps.Tools.ClusterStatus)
	}

	if deps.ControlPlane != nil {
		// The controllers surface is platform-admin: policy, alert
		// acknowledgement, and alertmanager silences are fleet-wide.
		// Mutations stay superuser-only; GETs require alerts:read/list so any
		// authenticated principal cannot enumerate fleet policy (SEC-05).
		superuserOnly := requireSuperuser(deps)
		alertsRead := requireAnyPermission(deps.RBACEngine, deps.RBACQueries,
			permissionRequirement{resource: rbac.ResourceAlerts, verb: rbac.VerbRead},
			permissionRequirement{resource: rbac.ResourceAlerts, verb: rbac.VerbList},
		)
		r.With(alertsRead).Get("/controllers/status/", deps.ControlPlane.Status)
		r.With(alertsRead).Get("/controllers/policy/", deps.ControlPlane.GetPolicy)
		r.With(superuserOnly).Put("/controllers/policy/", deps.ControlPlane.UpdatePolicy)
		r.With(alertsRead).Get("/controllers/alerts/", deps.ControlPlane.ListAlerts)
		r.With(superuserOnly).Post("/controllers/alerts/{id}/acknowledge/", deps.ControlPlane.AcknowledgeAlert)
		r.With(alertsRead).Get("/controllers/silences/", deps.ControlPlane.ListSilences)
		r.With(superuserOnly).Post("/controllers/silences/", deps.ControlPlane.CreateSilence)
		r.With(superuserOnly).Delete("/controllers/silences/{id}/", deps.ControlPlane.DeleteSilence)
	}

	// Sprint 072 — read-only anomaly baseline inspection.
	if deps.Anomaly != nil {
		r.Route("/anomaly-baselines", func(r chi.Router) {
			r.Get("/", deps.Anomaly.List)
			r.Get("/{id}/", deps.Anomaly.Get)
		})
	}

	if deps.ArgoCD != nil {
		r.With(featureGate("feature.argocd", deps.SettingsCache)).Route("/argocd", func(r chi.Router) {
			r.Get("/controller/status/", deps.ArgoCD.ControllerStatus)
			r.Get("/operations/", deps.ArgoCD.ListOperations)
			r.Get("/operations/{id}/", deps.ArgoCD.GetOperation)
			r.Post("/operations/{id}/retry/", deps.ArgoCD.RetryOperation)
			r.Get("/instances/", deps.ArgoCD.ListInstances)
			r.Post("/instances/", deps.ArgoCD.CreateInstance)
			r.Get("/instances/{id}/", deps.ArgoCD.GetInstance)
			r.Put("/instances/{id}/", deps.ArgoCD.UpdateInstance)
			r.Delete("/instances/{id}/", deps.ArgoCD.DeleteInstance)
			r.Get("/instances/{id}/applications/", deps.ArgoCD.LiveApplications)
			r.Get("/instances/{id}/cached-applications/", deps.ArgoCD.ListAppsByInstance)
			r.Get("/instances/{id}/health/", deps.ArgoCD.InstanceHealth)
			r.Get("/instances/{id}/orphan-report/", deps.ArgoCD.InstanceOrphanReport)
			r.Get("/applications/", deps.ArgoCD.ListAllApps)
			r.Get("/applications/{id}/", deps.ArgoCD.GetApp)
			r.Post("/applications/{id}/sync/", deps.ArgoCD.SyncApp)
			r.Get("/applications/{id}/history/", deps.ArgoCD.AppHistory)
			r.Get("/applications/{id}/manifests/", deps.ArgoCD.AppManifests)
			r.Post("/applications/{id}/refresh/", deps.ArgoCD.RefreshApp)
			r.Post("/instances/{id}/applications/{name}/sync/", deps.ArgoCD.SyncAppByName)
			r.Get("/clusters/{cluster_id}/ownership/", deps.ArgoCD.ClusterOwnership)
			r.Post("/clusters/{cluster_id}/ownership/{component_slug}/decision/", deps.ArgoCD.SetClusterOwnershipDecision)

			// Phase B1 — ArgoCD lifecycle additions.
			// Application / AppProject / ApplicationSet CRUD, cluster
			// registration into upstream ArgoCD, and repo credential
			// management. All endpoints write through to the upstream
			// instance using the typed client in internal/handler/argocd.
			r.Post("/instances/{id}/applications/", deps.ArgoCD.CreateApplication)
			r.Patch("/instances/{id}/applications/{name}/", deps.ArgoCD.PatchApplication)
			r.Delete("/instances/{id}/applications/{name}/", deps.ArgoCD.DeleteApplication)
			r.Get("/instances/{id}/projects/", deps.ArgoCD.ListProjects)
			r.Post("/instances/{id}/projects/", deps.ArgoCD.CreateProject)
			r.Patch("/instances/{id}/projects/{name}/", deps.ArgoCD.PatchProject)
			r.Delete("/instances/{id}/projects/{name}/", deps.ArgoCD.DeleteProject)
			r.Get("/instances/{id}/applicationsets/", deps.ArgoCD.ListApplicationSets)
			r.Post("/instances/{id}/applicationsets/", deps.ArgoCD.CreateApplicationSet)
			r.Delete("/instances/{id}/applicationsets/{name}/", deps.ArgoCD.DeleteApplicationSet)
			r.Get("/instances/{id}/clusters/", deps.ArgoCD.ListManagedClusters)
			r.Post("/instances/{id}/clusters/{cluster_id}/register/", deps.ArgoCD.RegisterManagedCluster)
			r.Post("/instances/{id}/clusters/{cluster_id}/refresh-labels/", deps.ArgoCD.RefreshManagedClusterLabels)
			r.Delete("/instances/{id}/clusters/{cluster_id}/register/", deps.ArgoCD.UnregisterManagedCluster)
			r.Get("/instances/{id}/repos/", deps.ArgoCD.ListRepos)
			r.Post("/instances/{id}/repos/", deps.ArgoCD.CreateRepo)
			r.Delete("/instances/{id}/repos/", deps.ArgoCD.DeleteRepo)
			r.Post("/instances/{id}/repos/test/", deps.ArgoCD.TestRepo)
		})
	}

	if deps.Backups != nil {
		r.With(featureGate("feature.backups", deps.SettingsCache)).Route("/backups", func(r chi.Router) {
			r.Get("/controller/status/", deps.Backups.ControllerStatus)
			r.Get("/", deps.Backups.ListBackups)
			// Backup/restore mutations run destructive Velero operations against a
			// managed cluster, so they carry the write-scope backstop (same
			// contract as the catalog/workload subtrees: reads + JWT sessions +
			// legacy empty-scope tokens pass; RBAC stays primary in-handler).
			r.With(mutationWriteScope).Post("/", deps.Backups.CreateBackup)
			// Alias for the frontend: GET /backups/runs/ lists backup runs in
			// the same shape as GET /backups/. The frontend's "runs" tab calls
			// this URL; without the alias the chi router 404s the path.
			r.Get("/runs/", deps.Backups.ListBackups)
			r.Get("/{id}/", deps.Backups.GetBackup)
			r.With(mutationWriteScope).Delete("/{id}/", deps.Backups.DeleteBackup)
			r.With(mutationWriteScope).Post("/{id}/restore/", deps.Backups.CreateRestoreByBackup)
			r.Get("/restores/", deps.Backups.ListRestores)
			r.Get("/storage/", deps.Backups.ListStorageConfigs)
			r.With(mutationWriteScope).Post("/storage/", deps.Backups.CreateStorageConfig)
			r.Get("/storage/{id}/", deps.Backups.GetStorageConfig)
			r.With(mutationWriteScope).Put("/storage/{id}/", deps.Backups.UpdateStorageConfig)
			r.With(mutationWriteScope).Delete("/storage/{id}/", deps.Backups.DeleteStorageConfig)
			r.With(mutationWriteScope).Post("/storage/{id}/test/", deps.Backups.TestStorageConfig)
			r.With(mutationWriteScope).Post("/storage/{id}/test-connection/", deps.Backups.TestStorageConfig)
			// Python-named alias paths (storage-configs/) so both clients work.
			r.Get("/storage-configs/", deps.Backups.ListStorageConfigs)
			r.With(mutationWriteScope).Post("/storage-configs/", deps.Backups.CreateStorageConfig)
			r.Get("/storage-configs/{id}/", deps.Backups.GetStorageConfig)
			r.With(mutationWriteScope).Put("/storage-configs/{id}/", deps.Backups.UpdateStorageConfig)
			r.With(mutationWriteScope).Delete("/storage-configs/{id}/", deps.Backups.DeleteStorageConfig)
			r.With(mutationWriteScope).Post("/storage-configs/{id}/test-connection/", deps.Backups.TestStorageConfig)
			r.Get("/schedules/", deps.Backups.ListSchedules)
			r.With(mutationWriteScope).Post("/schedules/", deps.Backups.CreateSchedule)
			r.Get("/schedules/{id}/", deps.Backups.GetSchedule)
			r.With(mutationWriteScope).Put("/schedules/{id}/", deps.Backups.UpdateSchedule)
			r.With(mutationWriteScope).Delete("/schedules/{id}/", deps.Backups.DeleteSchedule)
			r.With(mutationWriteScope).Post("/schedules/{id}/trigger-now/", deps.Backups.TriggerSchedule)
		})
	}

	if deps.Catalog != nil {
		// Per-route authorization for repository CRUD. These routes previously
		// sat behind ONLY the feature-flag gate, so a zero-grant viewer could
		// add/mutate/sync repositories. docs/security-sensitive-routes.json
		// already declares the catalog:create/update/delete requirement; these
		// gates make the code honor the doc. sync + test-connection are
		// classified as catalog:update (they mutate/probe an existing repo).
		catalogCreate := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCatalog, rbac.VerbCreate)
		catalogUpdate := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCatalog, rbac.VerbUpdate)
		catalogDelete := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCatalog, rbac.VerbDelete)
		// SEC-01: list/get carry live auth_config (redacted) and must not be
		// world-readable to every authenticated principal.
		catalogRead := requireAnyPermission(deps.RBACEngine, deps.RBACQueries,
			permissionRequirement{resource: rbac.ResourceCatalog, verb: rbac.VerbRead},
			permissionRequirement{resource: rbac.ResourceCatalog, verb: rbac.VerbList},
		)
		r.With(featureGate("feature.catalog", deps.SettingsCache)).Route("/catalog", func(r chi.Router) {
			r.Get("/controller/status/", deps.Catalog.ControllerStatus)
			r.Get("/operations/", deps.Catalog.ListOperations)
			r.Get("/operations/{id}/", deps.Catalog.GetOperation)
			r.Post("/operations/{id}/retry/", deps.Catalog.RetryOperation)
			r.With(catalogRead).Get("/repositories/", deps.Catalog.ListRepos)
			r.With(catalogCreate).Post("/repositories/", deps.Catalog.CreateRepo)
			r.With(catalogRead).Get("/repositories/{id}/", deps.Catalog.GetRepo)
			r.With(catalogUpdate).Put("/repositories/{id}/", deps.Catalog.UpdateRepo)
			r.With(catalogDelete).Delete("/repositories/{id}/", deps.Catalog.DeleteRepo)
			r.With(catalogUpdate).Post("/repositories/{id}/sync/", deps.Catalog.SyncRepo)
			r.With(catalogUpdate).Post("/repositories/{id}/test-connection/", deps.Catalog.TestRepoConnection)
			r.Get("/charts/", deps.Catalog.ListCharts)
			r.Get("/charts/{id}/", deps.Catalog.GetChart)
			r.Get("/charts/{id}/versions/", deps.Catalog.ListChartVersions)
			r.Get("/charts/{id}/readme/", deps.Catalog.GetChartReadme)
			r.Get("/charts/{id}/values/", deps.Catalog.GetChartValues)
			r.Get("/installed/", deps.Catalog.ListInstalledCharts)
			// NEW-1: helm install/upgrade/uninstall are cluster-mutating (they
			// run helm against a managed cluster), but this Catalog subtree was
			// never wired through the GATE-0 write-scope backstop. A read-scoped
			// API token must not be able to trigger these mutations, so the
			// helm lifecycle routes carry mutationWriteScope (same contract as
			// the workload/node/resource subtrees: reads + JWT + legacy
			// empty-scope tokens pass through; RBAC stays primary underneath).
			r.With(mutationWriteScope).Post("/installed/", deps.Catalog.CreateInstalledChart)
			r.With(mutationWriteScope).Put("/installed/{id}/upgrade/", deps.Catalog.UpgradeInstalledChart)
			r.With(mutationWriteScope).Post("/installed/{id}/rollback/", deps.Catalog.RollbackInstalledChart)
			r.With(mutationWriteScope).Delete("/installed/{id}/", deps.Catalog.DeleteInstalledChart)
			r.Get("/installed/{id}/values/", deps.Catalog.GetInstalledChartValues)
			r.Get("/installed/{id}/revisions/", deps.Catalog.ListInstalledChartRevisions)
		})

		// Sprint 082 — per-cluster Apps tab. Lives outside the
		// /catalog/ route group because the URL ("what's installed
		// on this cluster") reads more naturally as a cluster
		// concern, and the cluster-id is the natural RBAC anchor
		// (cluster:read). Same underlying installed_charts table as
		// the admin /catalog/installed/ endpoint, but the response
		// shape is enriched with joined chart metadata so the UI
		// can render the list with a single fetch.
		appsClusterRead := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)
		r.With(appsClusterRead).Get("/clusters/{cluster_id}/apps/", deps.Catalog.ListClusterApps)
		// Rancher-style bulk "Delete failed installs". Permission is
		// catalog:delete (the per-row uninstall affordance) rather
		// than clusters:update — the action only touches the
		// installed_charts namespace for this cluster.
		appsCatalogDelete := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceCatalog, rbac.VerbDelete)
		r.With(appsCatalogDelete).Delete("/clusters/{cluster_id}/apps/failed/", deps.Catalog.DeleteFailedClusterApps)
	}

	if deps.ChartRatings != nil {
		// Per-chart rating CRUD lives under /charts/{chart_id}/ratings/
		// rather than nested inside the catalog block above. Reason:
		// the catalog block is feature-gated behind feature.catalog;
		// ratings should remain visible even when the platform admin
		// has hidden the catalog UX, so they can't be lost on a feature
		// flag toggle.
		r.Route("/charts/{chart_id}/ratings", func(r chi.Router) {
			r.Post("/", deps.ChartRatings.CreateRating)
			r.Get("/", deps.ChartRatings.ListRatings)
			r.Get("/aggregate/", deps.ChartRatings.GetAggregate)
			r.Get("/mine/", deps.ChartRatings.GetMyRating)
			r.Put("/{rating_id}/", deps.ChartRatings.UpdateRating)
			r.Delete("/{rating_id}/", deps.ChartRatings.DeleteRating)
		})
		r.Route("/catalog/recommendations", func(r chi.Router) {
			r.Get("/popular/", deps.ChartRatings.PopularRecommendations)
			r.Get("/similar/{chart_id}/", deps.ChartRatings.SimilarRecommendations)
		})
	}

	if deps.Logging != nil {
		r.Route("/logging", func(r chi.Router) {
			r.Get("/controller/status/", deps.Logging.ControllerStatus)
			r.Get("/operations/", deps.Logging.ListOperations)
			r.Get("/operations/{id}/", deps.Logging.GetOperation)
			r.Post("/operations/{id}/retry/", deps.Logging.RetryOperation)
			r.Get("/outputs/", deps.Logging.ListOutputs)
			r.Post("/outputs/", deps.Logging.CreateOutput)
			r.Put("/outputs/{id}/", deps.Logging.UpdateOutput)
			r.Delete("/outputs/{id}/", deps.Logging.DeleteOutput)
			r.Post("/outputs/{id}/test/", deps.Logging.TestOutput)
			r.Post("/outputs/{id}/enable/", deps.Logging.EnableOutput)
			r.Post("/outputs/{id}/disable/", deps.Logging.DisableOutput)
			r.Post("/outputs/{id}/query/", deps.Logging.QueryOutput)
			r.Get("/pipelines/", deps.Logging.ListPipelines)
			r.Post("/pipelines/", deps.Logging.CreatePipeline)
			r.Put("/pipelines/{id}/", deps.Logging.UpdatePipeline)
			r.Delete("/pipelines/{id}/", deps.Logging.DeletePipeline)
			r.Post("/pipelines/{id}/enable/", deps.Logging.EnablePipeline)
			r.Post("/pipelines/{id}/disable/", deps.Logging.DisablePipeline)
			r.Get("/pipelines/{id}/fluentbit-config/", deps.Logging.FluentbitConfig)
		})
	}

}
