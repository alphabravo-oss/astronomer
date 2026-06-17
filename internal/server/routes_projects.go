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

	if deps.Projects != nil {
		r.With(featureGate("feature.projects", deps.SettingsCache)).Route("/projects", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbList)).Get("/", deps.Projects.List)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbCreate)).Post("/", deps.Projects.Create)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/{id}/", deps.Projects.Get)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Put("/{id}/", deps.Projects.Update)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Patch("/{id}/", deps.Projects.Update)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/{id}/ownership/takeover/", deps.Projects.TakeoverOwnership)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbDelete)).Delete("/{id}/", deps.Projects.Delete)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/{id}/add-namespace/", deps.Projects.AddNamespace)
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/{id}/remove-namespace/", deps.Projects.RemoveNamespace)
			// Policy PATCH is a targeted update of just the PSS + ResourceQuota
			// columns; gated on projects:update so an admin who can edit the
			// project can also retune its security posture.
			r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Patch("/{id}/policy/", deps.Projects.UpdatePolicy)
			// Quota-usage is read-only and reflects current cluster state, so
			// projects:read is the right gate. Multi-cluster fanout surfaces
			// per-cluster partial failures the way resources_search does.
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/{id}/quota-usage/", deps.Projects.QuotaUsage)
			// Per-project RBAC matrix: who is bound to what role on this
			// project. Read-only — bindings are created via the existing
			// /resources/rbac/ surface; this is the operator-facing
			// "members & roles" view.
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/{id}/rbac/", deps.Projects.RBACMatrix)
			// T4.3 — distinct clusters the project is materialised on,
			// derived from project_namespaces. Drives the
			// frontend multi-cluster project view.
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/{id}/clusters/", deps.Projects.ListClusters)
		})
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbList)).Get("/clusters/{cluster_id}/projects/", deps.Projects.ListByCluster)
	}

	// Cloud credentials (migration 053). Project-scoped CRUD with the
	// /test/ endpoint that hits each provider's "validate this
	// credential" SDK call. The public /providers/ list is exposed
	// outside the project tree so the UI's "Add credential" wizard can
	// load the form-builder schema without a project id.
	if deps.CloudCredentials != nil {
		r.Get("/cloud-credentials/providers/", deps.CloudCredentials.ListProviders)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/cloud-credentials/", deps.CloudCredentials.List)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/cloud-credentials/", deps.CloudCredentials.Create)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/cloud-credentials/{id}/", deps.CloudCredentials.Get)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Put("/projects/{project_id}/cloud-credentials/{id}/", deps.CloudCredentials.Update)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Patch("/projects/{project_id}/cloud-credentials/{id}/", deps.CloudCredentials.Update)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbDelete)).Delete("/projects/{project_id}/cloud-credentials/{id}/", deps.CloudCredentials.Delete)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/cloud-credentials/{id}/test/", deps.CloudCredentials.Test)
	}

	// Per-project ("BYO") Helm catalogs (migration 061). Gated by the
	// project-update permission — same shape as cloud-credentials above.
	if deps.ProjectCatalogs != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/catalogs/", deps.ProjectCatalogs.List)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/catalogs/", deps.ProjectCatalogs.Create)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Post("/projects/{project_id}/catalogs/{catalog_id}/subscribe/", deps.ProjectCatalogs.Subscribe)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbDelete)).Delete("/projects/{project_id}/catalogs/{catalog_id}/", deps.ProjectCatalogs.Delete)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{project_id}/catalogs/{catalog_id}/charts/", deps.ProjectCatalogs.ListCharts)
	}

	// Vault integration (migration 067).
	if deps.Vault != nil {
		r.Get("/admin/vault-connections/", deps.Vault.List)
		r.Post("/admin/vault-connections/", deps.Vault.Create)
		r.Get("/admin/vault-connections/{id}/", deps.Vault.Get)
		r.Put("/admin/vault-connections/{id}/", deps.Vault.Update)
		r.Delete("/admin/vault-connections/{id}/", deps.Vault.Delete)
		r.Post("/admin/vault-connections/{id}/test/", deps.Vault.Test)
		r.Post("/admin/vault-connections/{id}/health/", deps.Vault.Health)

		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/projects/{id}/default-vault-connection/", deps.Vault.GetProjectDefault)
		r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbUpdate)).Put("/projects/{id}/default-vault-connection/", deps.Vault.PutProjectDefault)
	}

}
