package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// RBACQuerier looks up role bindings for a user.
type RBACQuerier interface {
	GetUserBindings(ctx context.Context, userID string) ([]rbac.RoleBinding, error)
}

// RBACCacheInvalidator is implemented by RBACQuerier implementations that
// front their lookups with a cache. Mutation handlers (CreateBinding /
// DeleteBinding / UpdateRole / DeleteRole) call Invalidate after a successful
// DB write so the next authenticated request sees the change immediately
// instead of waiting for the cache TTL. InvalidateAll is used when a role
// definition changes — its rules are denormalised into every cached binding
// holding that role, and we don't keep a reverse index from role → users.
type RBACCacheInvalidator interface {
	Invalidate(userID string)
	InvalidateAll()
}

// RequirePermission creates middleware that checks if the authenticated user
// has the required permission (resource + verb) at the appropriate scope.
// Scope is determined from URL params: {cluster_id} and {project_id}.
func RequirePermission(engine *rbac.Engine, querier RBACQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := GetAuthenticatedUser(r.Context())
			if !ok || user == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "authentication_required",
						"message": "Authentication is required to access this resource",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "internal_error",
						"message": "Failed to retrieve user permissions",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			var clusterID, projectID uuid.UUID
			clusterParam := chi.URLParam(r, "cluster_id")
			if clusterParam == "" && resource == rbac.ResourceClusters {
				clusterParam = chi.URLParam(r, "id")
			}
			if cid := clusterParam; cid != "" {
				parsed, err := uuid.Parse(cid)
				if err == nil {
					clusterID = parsed
				}
			}
			projectParam := chi.URLParam(r, "project_id")
			if projectParam == "" && resource == rbac.ResourceProjects {
				projectParam = chi.URLParam(r, "id")
			}
			if pid := projectParam; pid != "" {
				parsed, err := uuid.Parse(pid)
				if err == nil {
					projectID = parsed
				}
			}
			namespace := namespaceContext(r)

			if !engine.CheckPermission(bindings, resource, verb, clusterID, projectID, namespace) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "permission_denied",
						"message": "You do not have permission to perform this action",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireListPermission gates a cluster-scoped LIST route. It behaves EXACTLY
// like RequirePermission except that, when namespaceScoped is true and the
// caller cannot pass the plain scope check for a BARE list (no ?namespace=), it
// falls back to HasAnyNamespaceAccess: a namespace- or project-scoped reader is
// allowed through to the handler, which then filters the results down to their
// authorized namespaces. When namespaceScoped is false this is byte-identical to
// RequirePermission.
//
// A request that names a specific ?namespace= is NOT given the broad fallback:
// it is evaluated by the ordinary scope check, so a crafted namespace the caller
// is not authorized for still yields a clean 403 here.
func RequireListPermission(engine *rbac.Engine, querier RBACQuerier, resource rbac.Resource, verb rbac.Verb, namespaceScoped bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := GetAuthenticatedUser(r.Context())
			if !ok || user == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "authentication_required",
						"message": "Authentication is required to access this resource",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "internal_error",
						"message": "Failed to retrieve user permissions",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			var clusterID, projectID uuid.UUID
			clusterParam := chi.URLParam(r, "cluster_id")
			if clusterParam == "" && resource == rbac.ResourceClusters {
				clusterParam = chi.URLParam(r, "id")
			}
			if cid := clusterParam; cid != "" {
				parsed, err := uuid.Parse(cid)
				if err == nil {
					clusterID = parsed
				}
			}
			projectParam := chi.URLParam(r, "project_id")
			if projectParam == "" && resource == rbac.ResourceProjects {
				projectParam = chi.URLParam(r, "id")
			}
			if pid := projectParam; pid != "" {
				parsed, err := uuid.Parse(pid)
				if err == nil {
					projectID = parsed
				}
			}
			namespace := namespaceContext(r)

			allowed := engine.CheckPermission(bindings, resource, verb, clusterID, projectID, namespace)
			// Bare-list fallback: only when the flag is on and the request did
			// not pin a specific namespace. A pinned ?namespace= is judged solely
			// by CheckPermission above so an unauthorized namespace 403s here.
			if !allowed && namespaceScoped && namespace == "" {
				allowed = engine.HasAnyNamespaceAccess(bindings, resource, verb, clusterID)
			}

			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				resp := map[string]interface{}{
					"error": map[string]string{
						"code":    "permission_denied",
						"message": "You do not have permission to perform this action",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func namespaceContext(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, key := range []string{"namespace", "namespace_name", "ns"} {
		if value := strings.TrimSpace(chi.URLParam(r, key)); value != "" {
			return value
		}
	}
	return strings.TrimSpace(r.URL.Query().Get("namespace"))
}
