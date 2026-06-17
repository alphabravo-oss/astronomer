package server

import (
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerDashboardRoutes(r chi.Router, deps RouterDependencies) {
	// Dashboard widgets — public render endpoints (migration 058).
	// Three scopes; per-cluster and per-project are RBAC-gated on
	// the parent resource:read verb so an operator who can view a
	// cluster automatically gets its widgets (no separate
	// dashboards:read role needed). The global endpoint requires
	// auth only — anyone logged in can see the platform overview.
	if deps.Dashboards != nil {
		r.Get("/dashboards/global/", deps.Dashboards.RenderGlobal)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/dashboards/clusters/{id}/", deps.Dashboards.RenderCluster)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceProjects, rbac.VerbRead)).Get("/dashboards/projects/{id}/", deps.Dashboards.RenderProject)
	}

}
