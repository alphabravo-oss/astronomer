package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/go-chi/chi/v5"
)

// registerPublicRoutes owns the intentionally unauthenticated health,
// readiness, documentation, chart repository, and SCIM provisioning surface.
func registerPublicRoutes(
	r chi.Router,
	cfg *config.Config,
	deps RouterDependencies,
	rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler,
) {
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
	if deps.AdminPlatform.PlatformCharts != nil {
		r.Get("/helm-repo/astronomer/index.yaml", deps.AdminPlatform.PlatformCharts.ServeIndex)
		r.Get("/helm-repo/astronomer/"+deps.AdminPlatform.PlatformCharts.ArchiveName(), deps.AdminPlatform.PlatformCharts.ServeArchive)
		r.Get("/helm-repo/astronomer-v2/index.yaml", deps.AdminPlatform.PlatformCharts.ServeIndex)
		r.Get("/helm-repo/astronomer-v2/"+deps.AdminPlatform.PlatformCharts.ArchiveName(), deps.AdminPlatform.PlatformCharts.ServeArchive)
	}
	if deps.CoreAuth.Docs != nil {
		// Public OpenAPI + Swagger UI. Outside the JWT auth chain so
		// operators can browse the API surface before they have a
		// token (it's the entry point for figuring OUT how to get a
		// token in the first place).
		r.Get("/api/v1/openapi.yaml", deps.CoreAuth.Docs.ServeOpenAPI)
		r.Get("/api/v1/docs", deps.CoreAuth.Docs.ServeSwaggerUI)
		r.Get("/api/v1/docs/", deps.CoreAuth.Docs.ServeSwaggerUI)
	}
	if deps.CoreAuth.Readyz != nil {
		r.Handle("/readyz", deps.CoreAuth.Readyz)
		r.Handle("/readyz/", deps.CoreAuth.Readyz)
	}

	// SCIM 2.0 provisioning (migration 114). Mounted at top-level
	// `/scim/v2/*` (NOT under `/api/v1`) and OUTSIDE the JWT auth chain —
	// SCIM clients authenticate with a static bearer token validated by
	// the handler's own Auth middleware. Nil-safe.
	if deps.CoreAuth.SCIM != nil {
		r.Route("/scim/v2", func(r chi.Router) {
			r.Use(rateLimit(appmiddleware.ClassSCIM))
			r.Use(deps.CoreAuth.SCIM.Auth)
			r.Post("/Users", deps.CoreAuth.SCIM.CreateUser)
			r.Get("/Users", deps.CoreAuth.SCIM.ListUsers)
			r.Get("/Users/{id}", deps.CoreAuth.SCIM.GetUser)
			r.Put("/Users/{id}", deps.CoreAuth.SCIM.PutUser)
			r.Patch("/Users/{id}", deps.CoreAuth.SCIM.PatchUser)
			r.Delete("/Users/{id}", deps.CoreAuth.SCIM.DeleteUser)
			r.Get("/Groups", deps.CoreAuth.SCIM.ListGroups)
			r.Post("/Groups", deps.CoreAuth.SCIM.CreateGroup)
			r.Get("/Groups/{id}", deps.CoreAuth.SCIM.GetGroup)
			r.Patch("/Groups/{id}", deps.CoreAuth.SCIM.PatchGroup)
			r.Delete("/Groups/{id}", deps.CoreAuth.SCIM.DeleteGroup)
			// Discovery (read-only, static) — Azure AD/Okta probe these
			// before provisioning.
			r.Get("/ServiceProviderConfig", deps.CoreAuth.SCIM.ServiceProviderConfig)
			r.Get("/ResourceTypes", deps.CoreAuth.SCIM.ResourceTypes)
			r.Get("/Schemas", deps.CoreAuth.SCIM.Schemas)
		})
	}

}
