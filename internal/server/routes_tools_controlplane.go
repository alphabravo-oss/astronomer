package server

import (
	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

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
		r.Get("/controllers/status/", deps.ControlPlane.Status)
		r.Get("/controllers/policy/", deps.ControlPlane.GetPolicy)
		r.Put("/controllers/policy/", deps.ControlPlane.UpdatePolicy)
		r.Get("/controllers/alerts/", deps.ControlPlane.ListAlerts)
		r.Post("/controllers/alerts/{id}/acknowledge/", deps.ControlPlane.AcknowledgeAlert)
		r.Get("/controllers/silences/", deps.ControlPlane.ListSilences)
		r.Post("/controllers/silences/", deps.ControlPlane.CreateSilence)
		r.Delete("/controllers/silences/{id}/", deps.ControlPlane.DeleteSilence)
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
			r.Post("/", deps.Backups.CreateBackup)
			// Alias for the frontend: GET /backups/runs/ lists backup runs in
			// the same shape as GET /backups/. The frontend's "runs" tab calls
			// this URL; without the alias the chi router 404s the path.
			r.Get("/runs/", deps.Backups.ListBackups)
			r.Get("/{id}/", deps.Backups.GetBackup)
			r.Delete("/{id}/", deps.Backups.DeleteBackup)
			r.Post("/{id}/restore/", deps.Backups.CreateRestoreByBackup)
			r.Get("/restores/", deps.Backups.ListRestores)
			r.Get("/storage/", deps.Backups.ListStorageConfigs)
			r.Post("/storage/", deps.Backups.CreateStorageConfig)
			r.Get("/storage/{id}/", deps.Backups.GetStorageConfig)
			r.Put("/storage/{id}/", deps.Backups.UpdateStorageConfig)
			r.Delete("/storage/{id}/", deps.Backups.DeleteStorageConfig)
			r.Post("/storage/{id}/test/", deps.Backups.TestStorageConfig)
			r.Post("/storage/{id}/test-connection/", deps.Backups.TestStorageConfig)
			// Python-named alias paths (storage-configs/) so both clients work.
			r.Get("/storage-configs/", deps.Backups.ListStorageConfigs)
			r.Post("/storage-configs/", deps.Backups.CreateStorageConfig)
			r.Get("/storage-configs/{id}/", deps.Backups.GetStorageConfig)
			r.Put("/storage-configs/{id}/", deps.Backups.UpdateStorageConfig)
			r.Delete("/storage-configs/{id}/", deps.Backups.DeleteStorageConfig)
			r.Post("/storage-configs/{id}/test-connection/", deps.Backups.TestStorageConfig)
			r.Get("/schedules/", deps.Backups.ListSchedules)
			r.Post("/schedules/", deps.Backups.CreateSchedule)
			r.Get("/schedules/{id}/", deps.Backups.GetSchedule)
			r.Put("/schedules/{id}/", deps.Backups.UpdateSchedule)
			r.Delete("/schedules/{id}/", deps.Backups.DeleteSchedule)
			r.Post("/schedules/{id}/trigger-now/", deps.Backups.TriggerSchedule)
		})
	}

	if deps.Catalog != nil {
		r.With(featureGate("feature.catalog", deps.SettingsCache)).Route("/catalog", func(r chi.Router) {
			r.Get("/controller/status/", deps.Catalog.ControllerStatus)
			r.Get("/operations/", deps.Catalog.ListOperations)
			r.Get("/operations/{id}/", deps.Catalog.GetOperation)
			r.Post("/operations/{id}/retry/", deps.Catalog.RetryOperation)
			r.Get("/repositories/", deps.Catalog.ListRepos)
			r.Post("/repositories/", deps.Catalog.CreateRepo)
			r.Get("/repositories/{id}/", deps.Catalog.GetRepo)
			r.Put("/repositories/{id}/", deps.Catalog.UpdateRepo)
			r.Delete("/repositories/{id}/", deps.Catalog.DeleteRepo)
			r.Post("/repositories/{id}/sync/", deps.Catalog.SyncRepo)
			r.Post("/repositories/{id}/test-connection/", deps.Catalog.TestRepoConnection)
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
