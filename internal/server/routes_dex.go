package server

import "github.com/go-chi/chi/v5"

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
