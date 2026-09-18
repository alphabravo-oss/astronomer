package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
)

// effectiveVerbsFor derives the EffectiveVerbs bundle for the operator
// against this cluster.
//
// SECURITY (H5): a break-glass shell defaults to READ-ONLY
// (get/list/watch) for every operator. Opening the shell only requires
// the clusters:update RBAC gate enforced by the route middleware, but a
// debug session does not need cluster-wide write by default. A
// write-capable (or cluster-admin) shell is a *deliberate*, audited
// opt-in: the caller must explicitly request elevation AND hold the
// matching astronomer RBAC verb.
//
//   - elevate=false (default) ........... read-only (get/list/watch)
//   - elevate=true + clusters:update .... + create/update/patch
//   - elevate=true + clusters:delete .... + delete
//   - elevate=true + superuser .......... cluster-admin
//
// On bindings-lookup failure we fail CLOSED to read-only — never silently
// grant write.
func (h *KubectlShellHandler) effectiveVerbsFor(r *http.Request, userID string, clusterID uuid.UUID, elevate bool) kubectl.EffectiveVerbs {
	// Least privilege baseline. Opening the shell already proved
	// clusters:update at the route gate, but read-only is all a default
	// debug session gets.
	v := kubectl.EffectiveVerbs{Read: true}
	if h.Bindings == nil || h.RBACEngine == nil {
		// Can't prove sensitive reads or elevation RBAC — stay on the
		// non-sensitive read-only resource allow-list.
		return v
	}
	bindings, err := h.Bindings.GetUserBindings(r.Context(), userID)
	if err != nil {
		return v
	}
	// These are candidate capabilities only; deriveCallerScope filters every
	// emitted Kubernetes rule through exact resource/verb/namespace grants.
	v.ReadSecrets = true
	v.ExecPods = true
	if !elevate {
		return v
	}
	if h.RBACEngine.CheckSuperuser(bindings) {
		v.Superuser = true
		return v
	}
	// Elevation requires the matching write RBAC; without clusters:update
	// the request stays read-only even though it was asked for.
	if h.RBACEngine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbUpdate, clusterID, uuid.Nil) {
		v.Update = true
	}
	if h.RBACEngine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbDelete, clusterID, uuid.Nil) {
		v.Delete = true
	}
	return v
}

func (h *KubectlShellHandler) callerIsSuperuser(r *http.Request, callerID uuid.UUID) bool {
	if h.Queries == nil {
		return false
	}
	u, err := h.Queries.GetUserByID(r.Context(), callerID)
	if err != nil {
		return false
	}
	return u.IsSuperuser
}

func (h *KubectlShellHandler) gateSuperuser(w http.ResponseWriter, r *http.Request) bool {
	callerID, ok := shellCallerUUID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return false
	}
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ShellUnavailable, "Kubectl shell is not configured")
		return false
	}
	user, err := h.Queries.GetUserByID(r.Context(), callerID)
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Caller not found")
		return false
	}
	if !user.IsSuperuser {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Kubectl shell admin views require superuser privileges")
		return false
	}
	return true
}

// shellCallerUUID resolves the authenticated user's UUID from the request
// context. Returns (uuid.Nil, false) on any failure.
func shellCallerUUID(r *http.Request) (uuid.UUID, bool) {
	u, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || u == nil {
		return uuid.UUID{}, false
	}
	parsed, err := uuid.Parse(u.ID)
	if err != nil {
		return uuid.UUID{}, false
	}
	return parsed, true
}
