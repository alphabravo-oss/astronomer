package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// requireSuperuser admits only platform superusers. The rbac engine grants
// superuser solely through the binding the RBAC store synthesizes for a user
// whose is_superuser flag is set, so this is the engine's view of the same fact
// the users table holds.
func requireSuperuser(engine *rbac.Engine, querier rbac.BindingQuerier) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return unavailableSecurityDependency("RBAC authorization")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := reqctx.AuthenticatedUser(r.Context())
			if !ok || user == nil {
				apiutil.WriteError(w, r, http.StatusUnauthorized, apiutil.AuthenticationRequired, "Authentication is required to access this resource")
				return
			}
			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.InternalError, "Failed to retrieve user permissions")
				return
			}
			if !engine.CheckSuperuser(bindings) {
				apiutil.WriteError(w, r, http.StatusForbidden, apiutil.PermissionDenied, "This action requires superuser privileges")
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

	if deps.ClusterResources.Tools != nil {
		r.Route("/tools", func(r chi.Router) {
			r.Get("/controller/status/", deps.ClusterResources.Tools.ControllerStatus)
			r.Get("/operations/", deps.ClusterResources.Tools.ListOperations)
			r.Get("/operations/{id}/", deps.ClusterResources.Tools.GetOperation)
			r.With(mutationWriteScope).Post("/operations/{id}/retry/", deps.ClusterResources.Tools.RetryOperation)
			r.Get("/", deps.ClusterResources.Tools.List)
			r.Get("/{id}/", deps.ClusterResources.Tools.Get)
			r.Get("/slug/{slug}/", deps.ClusterResources.Tools.GetBySlug)
			r.Get("/{slug:[^/]+}/", deps.ClusterResources.Tools.GetBySlug)
			r.Get("/{slug}/configuration/", deps.ClusterResources.Tools.Configuration)
			r.Post("/{slug}/preview/", deps.ClusterResources.Tools.Preview)
			r.With(mutationWriteScope).Post("/{slug}/install/", deps.ClusterResources.Tools.Install)
			r.With(mutationWriteScope).Put("/{slug}/upgrade/", deps.ClusterResources.Tools.Upgrade)
			r.With(mutationWriteScope).Delete("/{slug}/uninstall/", deps.ClusterResources.Tools.Uninstall)
			r.With(mutationWriteScope).Post("/{slug}/rollback/", deps.ClusterResources.Tools.Rollback)
			r.With(mutationWriteScope).Post("/{slug}/adopt/", deps.ClusterResources.Tools.Adopt)
		})
		r.Get("/clusters/{cluster_id}/tools/status/", deps.ClusterResources.Tools.ClusterStatus)
	}

	if deps.ClusterResources.ControlPlane != nil {
		// The controllers surface is platform-admin: policy, alert
		// acknowledgement, and alertmanager silences are fleet-wide.
		// Mutations stay superuser-only; GETs require alerts:read/list so any
		// authenticated principal cannot enumerate fleet policy (SEC-05).
		r.Get("/controllers/status/", deps.ClusterResources.ControlPlane.Status)
		r.Get("/controllers/policy/", deps.ClusterResources.ControlPlane.GetPolicy)
		r.Put("/controllers/policy/", deps.ClusterResources.ControlPlane.UpdatePolicy)
		r.Get("/controllers/alerts/", deps.ClusterResources.ControlPlane.ListAlerts)
		r.Post("/controllers/alerts/{id}/acknowledge/", deps.ClusterResources.ControlPlane.AcknowledgeAlert)
		r.Get("/controllers/silences/", deps.ClusterResources.ControlPlane.ListSilences)
		r.Post("/controllers/silences/", deps.ClusterResources.ControlPlane.CreateSilence)
		r.Delete("/controllers/silences/{id}/", deps.ClusterResources.ControlPlane.DeleteSilence)
	}

	// Sprint 072 — read-only anomaly baseline inspection.
	if deps.AdminPlatform.Anomaly != nil {
		r.Route("/anomaly-baselines", func(r chi.Router) {
			r.Get("/", deps.AdminPlatform.Anomaly.List)
			r.Get("/{id}/", deps.AdminPlatform.Anomaly.Get)
		})
	}

	if deps.ClusterResources.Backups != nil {
		r.With(featureGate("feature.backups", deps.CoreAuth.SettingsCache)).Route("/backups", func(r chi.Router) {
			r.Get("/controller/status/", deps.ClusterResources.Backups.ControllerStatus)
			r.Get("/", deps.ClusterResources.Backups.ListBackups)
			// Backup/restore mutations run destructive Velero operations against a
			// managed cluster, so they carry the write-scope backstop (same
			// contract as the catalog/workload subtrees: reads + JWT sessions +
			// legacy empty-scope tokens pass; RBAC stays primary in-handler).
			r.With(mutationWriteScope).Post("/", deps.ClusterResources.Backups.CreateBackup)
			r.Get("/{id}/", deps.ClusterResources.Backups.GetBackup)
			r.With(mutationWriteScope).Delete("/{id}/", deps.ClusterResources.Backups.DeleteBackup)
			r.With(mutationWriteScope).Post("/{id}/restore/", deps.ClusterResources.Backups.CreateRestoreByBackup)
			r.Get("/restores/", deps.ClusterResources.Backups.ListRestores)
			r.Get("/restores/{id}/", deps.ClusterResources.Backups.GetRestore)
			r.Get("/storage/", deps.ClusterResources.Backups.ListStorageConfigs)
			r.With(mutationWriteScope).Post("/storage/", deps.ClusterResources.Backups.CreateStorageConfig)
			r.Get("/storage/{id}/", deps.ClusterResources.Backups.GetStorageConfig)
			r.With(mutationWriteScope).Put("/storage/{id}/", deps.ClusterResources.Backups.UpdateStorageConfig)
			r.With(mutationWriteScope).Delete("/storage/{id}/", deps.ClusterResources.Backups.DeleteStorageConfig)
			r.With(mutationWriteScope).Post("/storage/{id}/test-connection/", deps.ClusterResources.Backups.TestStorageConfig)
			r.Get("/schedules/", deps.ClusterResources.Backups.ListSchedules)
			r.With(mutationWriteScope).Post("/schedules/", deps.ClusterResources.Backups.CreateSchedule)
			r.Get("/schedules/{id}/", deps.ClusterResources.Backups.GetSchedule)
			r.With(mutationWriteScope).Put("/schedules/{id}/", deps.ClusterResources.Backups.UpdateSchedule)
			r.With(mutationWriteScope).Delete("/schedules/{id}/", deps.ClusterResources.Backups.DeleteSchedule)
			r.With(mutationWriteScope).Post("/schedules/{id}/trigger-now/", deps.ClusterResources.Backups.TriggerSchedule)
		})
	}

	if deps.AdminPlatform.Catalog != nil {
		// Per-route authorization for repository CRUD. These routes previously
		// sat behind ONLY the feature-flag gate, so a zero-grant viewer could
		// add/mutate/sync repositories. docs/security-sensitive-routes.json
		// already declares the catalog:create/update/delete requirement; these
		// gates make the code honor the doc. sync + test-connection are
		// classified as catalog:update (they mutate/probe an existing repo).
		// SEC-01: list/get carry live auth_config (redacted) and must not be
		// world-readable to every authenticated principal.
		r.With(featureGate("feature.catalog", deps.CoreAuth.SettingsCache)).Route("/catalog", func(r chi.Router) {
			r.Get("/controller/status/", deps.AdminPlatform.Catalog.ControllerStatus)
			r.Get("/operations/", deps.AdminPlatform.Catalog.ListOperations)
			r.Get("/operations/{id}/", deps.AdminPlatform.Catalog.GetOperation)
			r.With(mutationWriteScope).Post("/operations/{id}/retry/", deps.AdminPlatform.Catalog.RetryOperation)
			r.Get("/repositories/", deps.AdminPlatform.Catalog.ListRepos)
			r.With(mutationWriteScope).Post("/repositories/", deps.AdminPlatform.Catalog.CreateRepo)
			r.Get("/repositories/{id}/", deps.AdminPlatform.Catalog.GetRepo)
			r.With(mutationWriteScope).Put("/repositories/{id}/", deps.AdminPlatform.Catalog.UpdateRepo)
			r.With(mutationWriteScope).Delete("/repositories/{id}/", deps.AdminPlatform.Catalog.DeleteRepo)
			r.With(mutationWriteScope).Post("/repositories/{id}/sync/", deps.AdminPlatform.Catalog.SyncRepo)
			r.With(mutationWriteScope).Post("/repositories/{id}/test-connection/", deps.AdminPlatform.Catalog.TestRepoConnection)
			// Chart handlers perform object-level repository visibility checks.
			// The collection gate admits project-scoped catalog readers so the
			// handler can bind ?project_id to the exact tenant before returning.
			r.Get("/charts/", deps.AdminPlatform.Catalog.ListCharts)
			r.Get("/discovery/", deps.AdminPlatform.Catalog.ListCatalogUserDiscovery)
			r.Get("/applications/", deps.AdminPlatform.Catalog.ListCatalogApplications)
			r.Get("/application-sources/", deps.AdminPlatform.Catalog.ListApplicationCatalogSources)
			r.Post("/applications/preview/", deps.AdminPlatform.Catalog.PreviewCatalogInstallation)
			r.Get("/charts/{id}/", deps.AdminPlatform.Catalog.GetChart)
			r.With(mutationWriteScope).Put("/charts/{id}/favorite/", deps.AdminPlatform.Catalog.SetCatalogChartFavorite)
			r.Get("/charts/{id}/versions/", deps.AdminPlatform.Catalog.ListChartVersions)
			r.Get("/charts/{id}/readme/", deps.AdminPlatform.Catalog.GetChartReadme)
			r.Get("/charts/{id}/values/", deps.AdminPlatform.Catalog.GetChartValues)
			r.Get("/installed/", deps.AdminPlatform.Catalog.ListInstalledCharts)
			r.Get("/installed/{id}/", deps.AdminPlatform.Catalog.GetInstalledChart)
			// NEW-1: helm install/upgrade/uninstall are cluster-mutating (they
			// run helm against a managed cluster), but this Catalog subtree was
			// never wired through the GATE-0 write-scope backstop. A read-scoped
			// API token must not be able to trigger these mutations, so the
			// helm lifecycle routes carry mutationWriteScope (same contract as
			// the workload/node/resource subtrees: reads + JWT + legacy
			// empty-scope tokens pass through; RBAC stays primary underneath).
			r.With(mutationWriteScope).Post("/installed/", deps.AdminPlatform.Catalog.CreateInstalledChart)
			r.With(mutationWriteScope).Put("/installed/{id}/upgrade/", deps.AdminPlatform.Catalog.UpgradeInstalledChart)
			r.Get("/installed/{id}/upgrade-versions/", deps.AdminPlatform.Catalog.ListInstalledChartUpgradeVersions)
			r.With(mutationWriteScope).Post("/installed/{id}/rollback/", deps.AdminPlatform.Catalog.RollbackInstalledChart)
			r.With(mutationWriteScope).Delete("/installed/{id}/", deps.AdminPlatform.Catalog.DeleteInstalledChart)
			r.Get("/installed/{id}/values/", deps.AdminPlatform.Catalog.GetInstalledChartValues)
			r.Get("/installed/{id}/revisions/", deps.AdminPlatform.Catalog.ListInstalledChartRevisions)
		})

		// Sprint 082 — per-cluster Apps tab. Lives outside the
		// /catalog/ route group because the URL ("what's installed
		// on this cluster") reads more naturally as a cluster
		// concern, and the cluster-id is the natural RBAC anchor
		// (cluster:read). Same underlying installed_charts table as
		// the admin /catalog/installed/ endpoint, but the response
		// shape is enriched with joined chart metadata so the UI
		// can render the list with a single fetch.
		r.Get("/clusters/{cluster_id}/apps/", deps.AdminPlatform.Catalog.ListClusterApps)
		// Rancher-style bulk "Delete failed installs". Permission is
		// catalog:delete (the per-row uninstall affordance) rather
		// than clusters:update — the action only touches the
		// installed_charts namespace for this cluster.
		r.Delete("/clusters/{cluster_id}/apps/failed/", deps.AdminPlatform.Catalog.DeleteFailedClusterApps)
	}

	if deps.AdminPlatform.ChartRatings != nil {
		// Per-chart rating CRUD lives under /charts/{chart_id}/ratings/
		// rather than nested inside the catalog block above. Reason:
		// the catalog block is feature-gated behind feature.catalog;
		// ratings should remain visible even when the platform admin
		// has hidden the catalog UX, so they can't be lost on a feature
		// flag toggle.
		r.Route("/charts/{chart_id}/ratings", func(r chi.Router) {
			r.Post("/", deps.AdminPlatform.ChartRatings.CreateRating)
			r.Get("/", deps.AdminPlatform.ChartRatings.ListRatings)
			r.Get("/aggregate/", deps.AdminPlatform.ChartRatings.GetAggregate)
			r.Get("/mine/", deps.AdminPlatform.ChartRatings.GetMyRating)
			r.Put("/{rating_id}/", deps.AdminPlatform.ChartRatings.UpdateRating)
			r.Delete("/{rating_id}/", deps.AdminPlatform.ChartRatings.DeleteRating)
		})
		r.Route("/catalog/recommendations", func(r chi.Router) {
			r.Get("/popular/", deps.AdminPlatform.ChartRatings.PopularRecommendations)
			r.Get("/similar/{chart_id}/", deps.AdminPlatform.ChartRatings.SimilarRecommendations)
		})
	}

	if deps.ClusterResources.Logging != nil {
		r.Route("/logging", func(r chi.Router) {
			r.Get("/controller/status/", deps.ClusterResources.Logging.ControllerStatus)
			r.Get("/operations/", deps.ClusterResources.Logging.ListOperations)
			r.Get("/operations/{id}/", deps.ClusterResources.Logging.GetOperation)
			r.With(mutationWriteScope).Post("/operations/{id}/retry/", deps.ClusterResources.Logging.RetryOperation)
			r.Get("/outputs/", deps.ClusterResources.Logging.ListOutputs)
			r.With(mutationWriteScope).Post("/outputs/", deps.ClusterResources.Logging.CreateOutput)
			r.With(mutationWriteScope).Put("/outputs/{id}/", deps.ClusterResources.Logging.UpdateOutput)
			r.With(mutationWriteScope).Delete("/outputs/{id}/", deps.ClusterResources.Logging.DeleteOutput)
			r.With(mutationWriteScope).Post("/outputs/{id}/test/", deps.ClusterResources.Logging.TestOutput)
			r.With(mutationWriteScope).Post("/outputs/{id}/enable/", deps.ClusterResources.Logging.EnableOutput)
			r.With(mutationWriteScope).Post("/outputs/{id}/disable/", deps.ClusterResources.Logging.DisableOutput)
			r.Post("/outputs/{id}/query/", deps.ClusterResources.Logging.QueryOutput)
			r.With(mutationWriteScope).Post("/outputs/{id}/rotate-token/", deps.ClusterResources.Logging.RotateOutputToken)
			r.Get("/saved-searches/", deps.ClusterResources.Logging.ListSavedSearches)
			r.With(mutationWriteScope).Post("/saved-searches/", deps.ClusterResources.Logging.CreateSavedSearch)
			r.With(mutationWriteScope).Put("/saved-searches/{id}/", deps.ClusterResources.Logging.UpdateSavedSearch)
			r.With(mutationWriteScope).Delete("/saved-searches/{id}/", deps.ClusterResources.Logging.DeleteSavedSearch)
			r.Get("/pipelines/", deps.ClusterResources.Logging.ListPipelines)
			r.Get("/pipelines/{id}/", deps.ClusterResources.Logging.GetPipeline)
			r.With(mutationWriteScope).Post("/pipelines/", deps.ClusterResources.Logging.CreatePipeline)
			r.With(mutationWriteScope).Put("/pipelines/{id}/", deps.ClusterResources.Logging.UpdatePipeline)
			r.With(mutationWriteScope).Delete("/pipelines/{id}/", deps.ClusterResources.Logging.DeletePipeline)
			r.With(mutationWriteScope).Post("/pipelines/{id}/enable/", deps.ClusterResources.Logging.EnablePipeline)
			r.With(mutationWriteScope).Post("/pipelines/{id}/disable/", deps.ClusterResources.Logging.DisablePipeline)
			r.Get("/pipelines/{id}/fluentbit-config/", deps.ClusterResources.Logging.FluentbitConfig)
		})
	}

}
