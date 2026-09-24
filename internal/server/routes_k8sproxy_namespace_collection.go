package server

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// An explicit subset is authorized for every actual downstream namespace.
// It never needs a cluster-wide list grant or the legacy response-filter flag.
func handleSelectedNamespaceAuthorization(w http.ResponseWriter, r *http.Request, next http.Handler, engine *rbac.Engine, native nativeAuthorizer, bindings []rbac.RoleBinding, userID string, clusterID, projectID uuid.UUID, resource rbac.Resource, verb rbac.Verb, ref map[string]string) bool {
	names, present, err := tunnel.SelectedCollectionNamespaces(r)
	if !present {
		return false
	}
	if err != nil {
		writeRouteAuthError(w, http.StatusBadRequest, "invalid_namespace_selection", err.Error())
		return true
	}
	for _, namespace := range names {
		if !engine.CheckPermission(bindings, resource, verb, clusterID, projectID, namespace) && (native == nil || !native.Allow(r.Context(), userID, clusterID.String(), namespace, ref["api_group"], ref["resource"], string(verb))) {
			writeRouteAuthError(w, http.StatusForbidden, "permission_denied", "You do not have permission to access every selected namespace")
			return true
		}
	}
	next.ServeHTTP(w, r)
	return true
}

func canonicalK8sProxyPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		canonical, err := tunnel.CanonicalK8sProxyPath(r)
		if err != nil {
			writeRouteAuthError(w, http.StatusBadRequest, "invalid_k8s_path", "Kubernetes proxy path is not canonical")
			return
		}
		next.ServeHTTP(w, tunnel.WithCanonicalK8sProxyPath(r, canonical))
	})
}
