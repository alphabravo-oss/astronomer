package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/toolui"
)

func newToolUIGateway(cfg *config.Config, deps RouterDependencies) *toolui.Gateway {
	if cfg.ToolUIDomain == "" {
		return nil
	}
	backend, err := auth.NewToolUIGrantBackend(cfg.RedisURL)
	if err != nil {
		backend = nil // Keep host dispatch fail-closed even if storage is unavailable.
	}
	gateway, err := toolui.New(cfg.ServerURL, cfg.ToolUIDomain, deps.CoreAuth.JWT, deps.CoreAuth.Encryptor, backend, func(w http.ResponseWriter, r *http.Request, target toolui.Target) {
		serveIsolatedTool(w, r, target, deps)
	})
	if err != nil {
		return nil
	}
	return gateway
}

func validateToolUIConfig(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	return toolui.Validate(cfg.ServerURL, cfg.ToolUIDomain)
}

// These wrappers replace downstream responses on the old main-origin URLs.
// Authorization middleware still runs before the launch page is issued.
func toolUIEntry(gateway *toolui.Gateway, kind string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := toolui.Target{Kind: kind}
		switch kind {
		case "service":
			target.Cluster = chi.URLParam(r, "cluster_id")
			target.Namespace = chi.URLParam(r, "namespace")
			target.ServicePort = chi.URLParam(r, "service_port")
		case "cluster-grafana":
			target.Cluster = chi.URLParam(r, "id")
		}
		path := target.Prefix() + strings.TrimPrefix(chi.URLParam(r, "*"), "/")
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		gateway.Launch(w, r, target, path)
	})
}

// A dedicated handler chain, with no control-plane router to fall through to.
// A tool's /api or /internal is always a downstream path, never a product API.
func serveIsolatedTool(w http.ResponseWriter, r *http.Request, target toolui.Target, deps RouterDependencies) {
	params := chi.NewRouteContext()
	params.URLParams.Add("cluster_id", target.Cluster)
	params.URLParams.Add("id", target.Cluster)
	params.URLParams.Add("namespace", target.Namespace)
	params.URLParams.Add("service_port", target.ServicePort)
	path := r.URL.Path
	if strings.HasPrefix(path, target.Prefix()) {
		path = strings.TrimPrefix(path, target.Prefix())
	}
	params.URLParams.Add("*", strings.TrimPrefix(path, "/"))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, params))
	var endpoint http.Handler
	switch target.Kind {
	case "service":
		if deps.ClusterResources.ServiceProxy != nil {
			endpoint = requireServiceProxyScope()(deps.ClusterResources.ServiceProxy)
			endpoint = authorizeToolUI(deps, "TOOLUI service", endpoint)
		}
	case "grafana":
		if deps.ClusterResources.Monitoring != nil {
			endpoint = http.HandlerFunc(deps.ClusterResources.Monitoring.ProxyGrafana)
		}
	case "cluster-grafana":
		if deps.ClusterResources.Monitoring != nil {
			endpoint = http.HandlerFunc(deps.ClusterResources.Monitoring.ProxyClusterGrafana)
			key := "TOOLUI cluster-grafana read"
			if r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
				key = "TOOLUI cluster-grafana update"
				endpoint = requireScope(auth.ScopeWriteClusters)(endpoint)
			}
			endpoint = authorizeToolUI(deps, key, endpoint)
		}
	}
	if endpoint == nil {
		http.NotFound(w, r)
		return
	}
	if deps.CoreAuth.Queries != nil {
		endpoint = appmiddleware.AuditLogWithWriter(slog.Default(), deps.CoreAuth.Queries)(endpoint)
	}
	if deps.CoreAuth.JWT == nil {
		unavailableSecurityDependency("JWT authentication")(endpoint).ServeHTTP(w, r)
		return
	}
	appmiddleware.RequireAuthWithQueries(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries)(endpoint).ServeHTTP(w, r)
}

// authorizeToolUI applies the toolUIPermissions row for an isolated tool host.
// These requests never reach the root router, so the route authorizer cannot
// resolve them by pattern; the row is chosen by the tool kind instead.
func authorizeToolUI(deps RouterDependencies, key string, endpoint http.Handler) http.Handler {
	if deps.CoreAuth.authz == nil {
		return unavailableSecurityDependency("route authorization")(endpoint)
	}
	return deps.CoreAuth.authz.enforceFixed(key, endpoint)
}
