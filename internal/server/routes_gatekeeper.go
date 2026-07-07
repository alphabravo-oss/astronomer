package server

import (
	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
)

// registerAlertInhibitionRoutes wires the P-03 Alertmanager-style inhibition
// CRUD under /api/v1/admin/alerting/inhibitions/*. Reads need alerts:read (or
// :list); mutations additionally require the admin scope, matching the other
// /admin/* mutating surfaces.
func registerAlertInhibitionRoutes(r chi.Router, deps RouterDependencies) {
	if deps.Alerting == nil {
		return
	}
	alertsRead := requireAnyPermission(
		deps.RBACEngine,
		deps.RBACQueries,
		permissionRequirement{resource: rbac.ResourceAlerts, verb: rbac.VerbRead},
		permissionRequirement{resource: rbac.ResourceAlerts, verb: rbac.VerbList},
	)
	adminCreate := requireScope(iauth.ScopeAdmin)
	alertsCreate := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAlerts, rbac.VerbCreate)
	alertsUpdate := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAlerts, rbac.VerbUpdate)
	alertsDelete := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceAlerts, rbac.VerbDelete)

	r.Route("/admin/alerting/inhibitions", func(r chi.Router) {
		r.With(alertsRead).Get("/", deps.Alerting.ListInhibitions)
		r.With(adminCreate, alertsCreate).Post("/", deps.Alerting.CreateInhibition)
		r.With(alertsRead).Get("/{id}/", deps.Alerting.GetInhibition)
		r.With(adminCreate, alertsUpdate).Put("/{id}/", deps.Alerting.UpdateInhibition)
		r.With(adminCreate, alertsDelete).Delete("/{id}/", deps.Alerting.DeleteInhibition)
	})
}

// registerGatekeeperConstraintRoutes wires the P-04 custom Gatekeeper
// constraint authoring under /api/v1/clusters/{id}/gatekeeper/constraints/*.
// List + validate need clusters:read; apply (create) + delete require the
// write-clusters scope AND clusters:update (the same authority as editing the
// cluster). The handler re-checks clusters:update fail-closed and audits both
// mutations.
func registerGatekeeperConstraintRoutes(r chi.Router, deps RouterDependencies) {
	if deps.Gatekeeper == nil {
		return
	}
	writeClusters := requireScope(iauth.ScopeWriteClusters)
	clustersRead := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)
	clustersUpdate := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)

	r.With(clustersRead).Get("/clusters/{id}/gatekeeper/constraints/", deps.Gatekeeper.ListConstraints)
	r.With(clustersRead).Post("/clusters/{id}/gatekeeper/constraints/validate/", deps.Gatekeeper.ValidateConstraint)
	r.With(writeClusters, clustersUpdate).Post("/clusters/{id}/gatekeeper/constraints/", deps.Gatekeeper.CreateConstraint)
	r.With(writeClusters, clustersUpdate).Delete("/clusters/{id}/gatekeeper/constraints/{name}/", deps.Gatekeeper.DeleteConstraint)
}
