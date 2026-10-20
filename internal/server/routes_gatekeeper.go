package server

import (
	"github.com/go-chi/chi/v5"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
)

// registerAlertInhibitionRoutes wires the P-03 Alertmanager-style inhibition
// CRUD under /api/v1/admin/alerting/inhibitions/*. Reads need alerts:read (or
// :list); mutations additionally require the admin scope, matching the other
// /admin/* mutating surfaces.
func registerAlertInhibitionRoutes(r chi.Router, deps RouterDependencies) {
	if deps.AdminPlatform.Alerting == nil {
		return
	}
	adminCreate := requireScope(iauth.ScopeAdmin)

	r.Route("/admin/alerting/inhibitions", func(r chi.Router) {
		r.Get("/", deps.AdminPlatform.Alerting.ListInhibitions)
		r.With(adminCreate).Post("/", deps.AdminPlatform.Alerting.CreateInhibition)
		r.Get("/{id}/", deps.AdminPlatform.Alerting.GetInhibition)
		r.With(adminCreate).Put("/{id}/", deps.AdminPlatform.Alerting.UpdateInhibition)
		r.With(adminCreate).Delete("/{id}/", deps.AdminPlatform.Alerting.DeleteInhibition)
	})
}

// registerGatekeeperConstraintRoutes wires the P-04 custom Gatekeeper
// constraint authoring under /api/v1/clusters/{id}/gatekeeper/constraints/*.
// List + validate need clusters:read; apply (create) + delete require the
// write-clusters scope AND clusters:update (the same authority as editing the
// cluster). The handler re-checks clusters:update fail-closed and audits both
// mutations.
func registerGatekeeperConstraintRoutes(r chi.Router, deps RouterDependencies) {
	if deps.ClusterResources.Gatekeeper == nil {
		return
	}
	writeClusters := requireScope(iauth.ScopeWriteClusters)

	r.Get("/clusters/{id}/gatekeeper/constraints/", deps.ClusterResources.Gatekeeper.ListConstraints)
	r.Post("/clusters/{id}/gatekeeper/constraints/validate/", deps.ClusterResources.Gatekeeper.ValidateConstraint)
	r.With(writeClusters).Post("/clusters/{id}/gatekeeper/constraints/", deps.ClusterResources.Gatekeeper.CreateConstraint)
	r.With(writeClusters).Delete("/clusters/{id}/gatekeeper/constraints/{name}/", deps.ClusterResources.Gatekeeper.DeleteConstraint)
}
