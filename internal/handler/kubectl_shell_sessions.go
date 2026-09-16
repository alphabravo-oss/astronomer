package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Open handles POST /clusters/{cluster_id}/shell/sessions/.
func (h *KubectlShellHandler) Open(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil || h.Deps.Requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ShellUnavailable, "Kubectl shell is not configured")
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	userID, ok := shellCallerUUID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	if _, err := h.Queries.GetClusterByID(r.Context(), clusterID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}

	// SECURITY (H5): the shell defaults to read-only. A write-capable /
	// cluster-admin shell is an explicit, audited opt-in (elevate=true)
	// that still requires the matching astronomer RBAC verb. An empty /
	// malformed body keeps the default least-privilege posture.
	elevate := parseShellElevation(r)
	verbs := h.effectiveVerbsFor(r, userID.String(), clusterID, elevate)

	// Every shell is confined to the caller's resource and namespace grants.
	// Missing authorization infrastructure always denies provisioning.
	// scopeNamespaces carries the caller's confined namespace allow-set into
	// kubectl.Open so it can provision per-namespace Roles (mechanism A).
	// nil for cross-namespace / superuser callers (the ClusterRole path).
	var scopeNamespaces []string
	scope, ok := h.deriveScopeForCaller(r.Context(), userID, clusterID, verbs)
	if !ok {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Kubectl shell scope could not be determined for the caller")
		return
	}
	verbs = scope.Verbs
	if !scope.AllNamespaces && !scope.Superuser {
		scopeNamespaces = scope.SortedNamespaces()
	}
	scopeDetail := map[string]any{
		"scope_enabled":     true,
		"scope_all_ns":      scope.AllNamespaces,
		"scope_namespaces":  scope.SortedNamespaces(),
		"scope_impersonate": scope.Caller.String(),
		"scope_enforcement": scopeEnforcementLabel(scope),
	}

	// A caller can ask to elevate without holding the RBAC; in that case
	// effectiveVerbsFor returns read-only. Record what was actually
	// granted, not what was requested.
	elevated := verbs.Superuser || verbs.Update || verbs.Delete

	info, err := kubectl.Open(r.Context(), h.Deps, kubectl.OpenRequest{
		UserID:     userID,
		ClusterID:  clusterID,
		Verbs:      verbs,
		Namespaces: scopeNamespaces,
		ClientIP:   reqctx.ClientIP(r),
		UserAgent:  r.UserAgent(),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ShellOpenFailed, err.Error())
		return
	}
	auditDetail := map[string]any{
		"session_id":        info.ID.String(),
		"superuser":         verbs.Superuser,
		"verbs":             verbs.Verbs(),
		"elevation_request": elevate,
		"elevated":          elevated,
	}
	for k, v := range scopeDetail {
		auditDetail[k] = v
	}
	recordAudit(r, h.Queries, "kubectl.session.opened", "cluster", clusterID.String(), "", auditDetail)
	w.Header().Set("Location", "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/"+info.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, info)
}

// Get handles GET /clusters/{cluster_id}/shell/sessions/{id}/.
func (h *KubectlShellHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ShellUnavailable, "Kubectl shell is not configured")
		return
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	count, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
	RespondJSON(w, http.StatusOK, kubectl.ToSessionInfo(row, count, h.idleTimeout()))
}

// Close handles POST /clusters/{cluster_id}/shell/sessions/{id}/close/.
func (h *KubectlShellHandler) Close(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ShellUnavailable, "Kubectl shell is not configured")
		return
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	if err := kubectl.Close(r.Context(), h.Deps, row.ID); err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ShellCloseFailed, err.Error())
		return
	}
	recordAudit(r, h.Queries, "kubectl.session.closed", "cluster", row.ClusterID.String(), "", map[string]any{
		"session_id":       row.ID.String(),
		"duration_seconds": int64(time.Since(row.StartedAt).Seconds()),
	})
	RespondJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

// loadSessionForCluster pulls the {cluster_id}/{id} pair from chi and
// returns the row only when it exists, belongs to this cluster, AND
// (for non-superuser callers) belongs to the caller. Writes the
// appropriate error response on failure.
func (h *KubectlShellHandler) loadSessionForCluster(w http.ResponseWriter, r *http.Request) (sqlc.KubectlSession, bool) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return sqlc.KubectlSession{}, false
	}
	idStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid session id")
		return sqlc.KubectlSession{}, false
	}
	row, err := h.Queries.GetKubectlSessionByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Session not found")
			return sqlc.KubectlSession{}, false
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return sqlc.KubectlSession{}, false
	}
	if row.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Session not found in this cluster")
		return sqlc.KubectlSession{}, false
	}
	callerID, ok := shellCallerUUID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return sqlc.KubectlSession{}, false
	}
	if row.UserID != callerID && !h.callerIsSuperuser(r, callerID) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.SessionNotOwned, "Session belongs to another operator")
		return sqlc.KubectlSession{}, false
	}
	return row, true
}

func (h *KubectlShellHandler) idleTimeout() time.Duration {
	if h.Deps.IdleTimeout > 0 {
		return h.Deps.IdleTimeout
	}
	return 30 * time.Minute
}

// parseShellElevation reads the opt-in write/elevation flag from the
// Open request body. The body is optional: an empty or malformed body
// (or any non-true value) keeps the default least-privilege posture so
// a normal break-glass shell is read-only. Accepts both an explicit
// boolean and the privilege-level string the SPA may send.
func parseShellElevation(r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	var body struct {
		Elevate bool   `json:"elevate"`
		Mode    string `json:"mode"`
	}
	// Best-effort: ignore decode errors (empty body, bad JSON) and treat
	// them as "no elevation requested".
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return false
	}
	if body.Elevate {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(body.Mode)) {
	case "write", "rw", "read-write", "elevated", "admin":
		return true
	}
	return false
}
