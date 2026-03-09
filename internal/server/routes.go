package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// NewRouter builds and returns the Chi router with all routes and middleware.
// If bootstrap is nil (e.g. in tests without DB), stub handlers are used.
// Optional handler parameters (projects, tools, audit) may be nil; routes are
// only registered when the corresponding handler is provided.
func NewRouter(
	cfg *config.Config,
	bootstrap *handler.BootstrapHandler,
	projects *handler.ProjectHandler,
	tools *handler.ToolHandler,
	audit *handler.AuditHandler,
) chi.Router {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Request-ID"},
		ExposedHeaders:   []string{"Link", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health check (with and without trailing slash)
	healthHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": version.Version,
			"time":    time.Now().UTC().Format(time.RFC3339),
		})
	})
	r.Get("/health", healthHandler)
	r.Get("/health/", healthHandler)

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		// Bootstrap
		if bootstrap != nil {
			r.Get("/bootstrap/", bootstrap.GetBootstrapStatus)
			r.Post("/bootstrap/complete/", bootstrap.CompleteBootstrap)
		} else {
			// Stub handlers for tests without DB
			r.Get("/bootstrap/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"bootstrapped":  false,
					"server_url":    "",
					"platform_name": "Astronomer",
				})
			})

			r.Post("/bootstrap/complete/", func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "Not Implemented", http.StatusNotImplemented)
			})
		}

		// Projects
		if projects != nil {
			r.Route("/projects", func(r chi.Router) {
				r.Get("/", projects.List)
				r.Post("/", projects.Create)
				r.Get("/{id}/", projects.Get)
				r.Put("/{id}/", projects.Update)
				r.Delete("/{id}/", projects.Delete)
			})

			r.Get("/clusters/{cluster_id}/projects/", projects.ListByCluster)
		}

		// Tools
		if tools != nil {
			r.Route("/tools", func(r chi.Router) {
				r.Get("/", tools.List)
				r.Get("/{id}/", tools.Get)
				r.Get("/slug/{slug}/", tools.GetBySlug)
			})
		}

		// Audit logs
		if audit != nil {
			r.Route("/audit", func(r chi.Router) {
				r.Get("/", audit.List)
				r.Get("/{id}/", audit.Get)
			})
		}
	})

	return r
}
