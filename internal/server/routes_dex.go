package server

import (
	"github.com/go-chi/chi/v5"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerDexRoutes(r chi.Router, deps RouterDependencies) {
	// Phase B4 — Dex routes
	// Lightweight CRUD over Dex connector + settings rows, plus an /apply
	// endpoint that renders the rows into a ConfigMap and PATCHes it into
	// the management cluster (Dex hot-reloads). RegisterAsSSO is the one-
	// click ergonomic helper that creates a `dex` row in sso_configurations
	// pointing at the configured issuer URL — A1's generic OIDC path then
	// takes over.
	if deps.DexConfig != nil {
		r.Route("/auth/dex", func(r chi.Router) {
			// SECURITY: the Dex connector/settings/apply rows become the
			// identity-provider config the whole platform trusts, so the entire
			// subtree is admin-only — gate it behind ScopeAdmin + ResourceSSO,
			// mirroring the /settings/sso routes in routes.go. Authentication is
			// already applied by the enclosing authenticated router; without this
			// scope+permission gate any authenticated user could register a
			// malicious IdP or DoS auth. requireScope only bites API tokens (JWT
			// sessions fall through to the RBAC permission check, the real gate).
			r.Use(requireScope(iauth.ScopeAdmin))
			r.Use(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSSO, rbac.VerbUpdate))
			r.Get("/connector-types/", deps.DexConfig.ListConnectorTypes)
			r.Get("/connectors/", deps.DexConfig.ListConnectors)
			r.Post("/connectors/", deps.DexConfig.CreateConnector)
			r.Get("/connectors/{id}/", deps.DexConfig.GetConnector)
			r.Patch("/connectors/{id}/", deps.DexConfig.UpdateConnector)
			r.Delete("/connectors/{id}/", deps.DexConfig.DeleteConnector)
			r.Get("/settings/", deps.DexConfig.GetSettings)
			r.Put("/settings/", deps.DexConfig.UpdateSettings)
			r.Post("/apply/", deps.DexConfig.Apply)
			r.Post("/register-as-sso/", deps.DexConfig.RegisterAsSSO)
		})
	}

}
