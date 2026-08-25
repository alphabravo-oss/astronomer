package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/go-chi/chi/v5"
)

// registerPublicRoutes owns the intentionally unauthenticated health,
// readiness, documentation, chart repository, and SCIM provisioning surface.
func registerPublicRoutes(r chi.Router, cfg *config.Config, deps RouterDependencies) {
	// Health check (with and without trailing slash)
	healthHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": version.Version,
			"time":    time.Now().UTC().Format(time.RFC3339),
		})
	})
	r.Get("/health", healthHandler)
	r.Get("/health/", healthHandler)
	if deps.PlatformCharts != nil {
		r.Get("/helm-repo/astronomer/index.yaml", deps.PlatformCharts.ServeIndex)
		r.Get("/helm-repo/astronomer/"+deps.PlatformCharts.ArchiveName(), deps.PlatformCharts.ServeArchive)
		r.Get("/helm-repo/astronomer-v2/index.yaml", deps.PlatformCharts.ServeIndex)
		r.Get("/helm-repo/astronomer-v2/"+deps.PlatformCharts.ArchiveName(), deps.PlatformCharts.ServeArchive)
	}
	if deps.Docs != nil {
		// Public OpenAPI + Swagger UI. Outside the JWT auth chain so
		// operators can browse the API surface before they have a
		// token (it's the entry point for figuring OUT how to get a
		// token in the first place).
		r.Get("/api/v1/openapi.yaml", deps.Docs.ServeOpenAPI)
		r.Get("/api/v1/docs", deps.Docs.ServeSwaggerUI)
		r.Get("/api/v1/docs/", deps.Docs.ServeSwaggerUI)
	}
	if deps.Readyz != nil {
		r.Handle("/readyz", deps.Readyz)
		r.Handle("/readyz/", deps.Readyz)
	}

	// SCIM 2.0 provisioning (migration 114). Mounted at top-level
	// `/scim/v2/*` (NOT under `/api/v1`) and OUTSIDE the JWT auth chain —
	// SCIM clients authenticate with a static bearer token validated by
	// the handler's own Auth middleware. Nil-safe.
	if deps.SCIM != nil {
		r.Route("/scim/v2", func(r chi.Router) {
			r.Use(deps.SCIM.Auth)
			r.Post("/Users", deps.SCIM.CreateUser)
			r.Get("/Users", deps.SCIM.ListUsers)
			r.Get("/Users/{id}", deps.SCIM.GetUser)
			r.Put("/Users/{id}", deps.SCIM.PutUser)
			r.Patch("/Users/{id}", deps.SCIM.PatchUser)
			r.Delete("/Users/{id}", deps.SCIM.DeleteUser)
			r.Get("/Groups", deps.SCIM.ListGroups)
			r.Post("/Groups", deps.SCIM.CreateGroup)
			r.Get("/Groups/{id}", deps.SCIM.GetGroup)
			r.Patch("/Groups/{id}", deps.SCIM.PatchGroup)
			r.Delete("/Groups/{id}", deps.SCIM.DeleteGroup)
			// Discovery (read-only, static) — Azure AD/Okta probe these
			// before provisioning.
			r.Get("/ServiceProviderConfig", deps.SCIM.ServiceProviderConfig)
			r.Get("/ResourceTypes", deps.SCIM.ResourceTypes)
			r.Get("/Schemas", deps.SCIM.Schemas)
		})
	}

}
