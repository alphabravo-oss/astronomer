package server

import (
	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerProjectRoutes(r chi.Router, deps RouterDependencies) {
	writeProjects := requireScope(iauth.ScopeWriteProjects)

	if deps.ClusterResources.Projects != nil {
		r.With(featureGate("feature.projects", deps.CoreAuth.SettingsCache)).Route("/projects", func(r chi.Router) {
			// Collection gate: cluster-/project-scoped callers are admitted and
			// the handler filters the page. See RequireCollectionPermission.
			r.With(requireCollectionPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbList)).Get("/", deps.ClusterResources.Projects.List)
			r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbCreate)).Post("/", deps.ClusterResources.Projects.Create)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/{id}/", deps.ClusterResources.Projects.Get)
			r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Put("/{id}/", deps.ClusterResources.Projects.Update)
			r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Patch("/{id}/", deps.ClusterResources.Projects.Update)
			r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/{id}/ownership/takeover/", deps.ClusterResources.Projects.TakeoverOwnership)
			r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbDelete)).Delete("/{id}/", deps.ClusterResources.Projects.Delete)
			r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/{id}/add-namespace/", deps.ClusterResources.Projects.AddNamespace)
			r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/{id}/remove-namespace/", deps.ClusterResources.Projects.RemoveNamespace)
			// Policy PATCH is a targeted update of just the PSS + ResourceQuota
			// columns; gated on projects:update so an admin who can edit the
			// project can also retune its security posture.
			r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Patch("/{id}/policy/", deps.ClusterResources.Projects.UpdatePolicy)
			// Quota-usage is read-only and reflects current cluster state, so
			// projects:read is the right gate. Multi-cluster fanout surfaces
			// per-cluster partial failures the way resources_search does.
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/{id}/quota-usage/", deps.ClusterResources.Projects.QuotaUsage)
			// Per-project RBAC matrix: who is bound to what role on this
			// project. Read-only — bindings are created via the existing
			// /resources/rbac/ surface; this is the operator-facing
			// "members & roles" view.
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/{id}/rbac/", deps.ClusterResources.Projects.RBACMatrix)
			// T4.3 — distinct clusters the project is materialised on,
			// derived from project_namespaces. Drives the
			// frontend multi-cluster project view.
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/{id}/clusters/", deps.ClusterResources.Projects.ListClusters)
		})
		r.With(requireCollectionPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbList)).Get("/clusters/{cluster_id}/projects/", deps.ClusterResources.Projects.ListByCluster)
	}

	// Cloud credentials (migration 053). Project-scoped CRUD with the
	// /test/ endpoint that hits each provider's "validate this
	// credential" SDK call. The public /providers/ list is exposed
	// outside the project tree so the UI's "Add credential" wizard can
	// load the form-builder schema without a project id.
	if deps.ClusterResources.CloudCredentials != nil {
		r.Get("/cloud-credentials/providers/", deps.ClusterResources.CloudCredentials.ListProviders)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/cloud-credentials/", deps.ClusterResources.CloudCredentials.List)
		r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/cloud-credentials/", deps.ClusterResources.CloudCredentials.Create)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/cloud-credentials/{id}/", deps.ClusterResources.CloudCredentials.Get)
		r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Put("/projects/{project_id}/cloud-credentials/{id}/", deps.ClusterResources.CloudCredentials.Update)
		r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Patch("/projects/{project_id}/cloud-credentials/{id}/", deps.ClusterResources.CloudCredentials.Update)
		r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbDelete)).Delete("/projects/{project_id}/cloud-credentials/{id}/", deps.ClusterResources.CloudCredentials.Delete)
		r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/cloud-credentials/{id}/test/", deps.ClusterResources.CloudCredentials.Test)
	}

	// Per-project ("BYO") Helm catalogs (migration 061). Gated by the
	// project-update permission — same shape as cloud-credentials above.
	if deps.ClusterResources.ProjectCatalogs != nil {
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/catalogs/", deps.ClusterResources.ProjectCatalogs.List)
		r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/catalogs/", deps.ClusterResources.ProjectCatalogs.Create)
		r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/catalogs/{catalog_id}/subscribe/", deps.ClusterResources.ProjectCatalogs.Subscribe)
		r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbDelete)).Delete("/projects/{project_id}/catalogs/{catalog_id}/", deps.ClusterResources.ProjectCatalogs.Delete)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/catalogs/{catalog_id}/charts/", deps.ClusterResources.ProjectCatalogs.ListCharts)
	}

	// Vault integration (migration 067).
	if deps.ClusterResources.Vault != nil {
		r.Get("/admin/vault-connections/", deps.ClusterResources.Vault.List)
		r.Post("/admin/vault-connections/", deps.ClusterResources.Vault.Create)
		r.Get("/admin/vault-connections/{id}/", deps.ClusterResources.Vault.Get)
		r.Put("/admin/vault-connections/{id}/", deps.ClusterResources.Vault.Update)
		r.Delete("/admin/vault-connections/{id}/", deps.ClusterResources.Vault.Delete)
		r.Post("/admin/vault-connections/{id}/test/", deps.ClusterResources.Vault.Test)
		r.Post("/admin/vault-connections/{id}/health/", deps.ClusterResources.Vault.Health)

		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{id}/default-vault-connection/", deps.ClusterResources.Vault.GetProjectDefault)
		r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Put("/projects/{id}/default-vault-connection/", deps.ClusterResources.Vault.PutProjectDefault)
	}

}
