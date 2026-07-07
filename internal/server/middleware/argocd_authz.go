package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// LocalClusterResolver returns the management ("local") cluster's id. It is
// resolved lazily, per-request, because the local-cluster row is bootstrapped
// after the router is built. Returning uuid.Nil (with a nil error) means "no
// local cluster row yet" — the same not-configured signal the ArgoCD UI token
// source uses; the authz check then falls back to global-scope evaluation so
// only global/superuser grants pass. A non-nil error is treated as fail-closed.
type LocalClusterResolver func(ctx context.Context) (uuid.UUID, error)

// ArgoCDAuthz authorizes requests to the ArgoCD UI reverse proxy AFTER
// authentication (AuthBrowserOrBearer) has established WHO the caller is. The
// proxy injects the shared upstream ArgoCD admin token into every forwarded
// request, so without this gate any authenticated principal — including a
// read-only viewer with zero grants — is transparently logged into ArgoCD as
// admin (broken access control / vertical privilege escalation).
//
// Method → permission mapping, anchored to the local cluster scope:
//
//   - GET/HEAD reads                              → argocd:read
//   - POST/PUT/PATCH/DELETE + ArgoCD action paths → clusters:update
//     (ArgoCD issues sync/rollback/terminate as POST, but a GET to an action
//     path is still treated as a mutation out of caution)
//
// Fail-closed: if the RBAC engine or binding querier is nil (misconfiguration),
// every request is denied rather than silently opening the admin proxy. The
// denial response mirrors AuthBrowserOrBearer's HTML-vs-JSON split: a JSON 403
// for XHR/WebSocket callers and an HTML permission page for browser navigation.
func ArgoCDAuthz(engine *rbac.Engine, querier RBACQuerier, localCluster LocalClusterResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fail closed on misconfiguration — never pass through to the
			// admin-token-injecting proxy without an authorization decision.
			if engine == nil || querier == nil {
				writeArgoCDForbidden(w, r)
				return
			}

			user, ok := GetAuthenticatedUser(r.Context())
			if !ok || user == nil {
				writeArgoCDForbidden(w, r)
				return
			}

			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				writeArgoCDForbidden(w, r)
				return
			}

			// Anchor the check to the local cluster the proxy targets. A DB
			// error resolving it is fail-closed; a not-yet-bootstrapped local
			// cluster (uuid.Nil) leaves the check at global scope so only
			// global/superuser grants pass.
			var clusterID uuid.UUID
			if localCluster != nil {
				cid, cerr := localCluster(r.Context())
				if cerr != nil {
					writeArgoCDForbidden(w, r)
					return
				}
				clusterID = cid
			}

			resource, verb := argoCDProxyPermission(r)
			if !engine.CheckPermission(bindings, resource, verb, clusterID, uuid.Nil) {
				writeArgoCDForbidden(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// argoCDProxyPermission maps an inbound ArgoCD-proxy request to the RBAC
// resource+verb it requires. Mutating HTTP methods and ArgoCD action paths
// (sync/rollback/terminate) require clusters:update; everything else is a read.
func argoCDProxyPermission(r *http.Request) (rbac.Resource, rbac.Verb) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		if isArgoCDMutatingPath(r.URL.Path) {
			return rbac.ResourceClusters, rbac.VerbUpdate
		}
		return rbac.ResourceArgoCD, rbac.VerbRead
	default:
		return rbac.ResourceClusters, rbac.VerbUpdate
	}
}

// isArgoCDMutatingPath reports whether the request path is an ArgoCD action
// endpoint that changes cluster state even though it may arrive as a GET.
// ArgoCD exposes these as `.../applications/{name}/sync`, `/rollback`,
// `/terminate`, etc.
func isArgoCDMutatingPath(path string) bool {
	p := strings.TrimRight(path, "/")
	for _, suffix := range []string{"/sync", "/rollback", "/terminate", "/resource-action"} {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}

// writeArgoCDForbidden emits a 403 in the shape the caller expects: an HTML
// permission page for top-level browser navigation, a JSON body otherwise
// (XHR / WebSocket). Mirrors the HTML-vs-JSON split in AuthBrowserOrBearer.
func writeArgoCDForbidden(w http.ResponseWriter, r *http.Request) {
	if wantsHTMLRedirect(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(argoCDForbiddenHTML))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    "permission_denied",
			"message": "You do not have permission to access ArgoCD",
		},
	})
}

const argoCDForbiddenHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Access denied</title></head>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;color:#1f2937">
<h1 style="font-size:1.25rem">403 — Access denied</h1>
<p>You do not have permission to access the ArgoCD console. Ask an administrator for an ArgoCD or cluster grant.</p>
<p><a href="/dashboard">Back to dashboard</a></p>
</body>
</html>`
