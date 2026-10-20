package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	projectdomain "github.com/alphabravocompany/astronomer-go/internal/projects"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// ProjectNamespaceRequest represents the request body for add/remove namespace.
// openapi:request ProjectNamespaceRequest
type ProjectNamespaceRequest struct {
	Namespace string `json:"namespace"`
}

// rejectReservedNamespaces writes a 403 and returns false when any candidate is
// reserved. Callers pass only namespaces they are about to ADD.
func (h *ProjectHandler) rejectReservedNamespaces(w http.ResponseWriter, r *http.Request, candidates ...string) bool {
	for _, ns := range candidates {
		if projectdomain.IsReservedNamespace(ns) {
			apiutil.WriteError(w, r, http.StatusForbidden, apiutil.Forbidden,
				"Namespace '"+ns+"' is reserved by the platform and cannot be assigned to a project.")
			return false
		}
	}
	return true
}

// authorizeNamespaceAssignment gates project→namespace membership on authority
// over the CLUSTER, not over the project.
//
// SECURITY: the route gate for add-namespace / PUT-project is projects:update,
// which the shipped project-owner and namespace-operator templates both grant
// at PROJECT scope. Assigning a namespace is not a project-scoped act, though —
// it moves a cluster resource under a grant the caller already holds, so
// gating it on the project is self-service privilege expansion: the caller
// picks the namespace their own bindings will expand into. Requiring
// clusters:update at the project's cluster means only a cluster- or
// platform-scoped principal (cluster-owner, cluster-operator, platform-admin,
// superuser) can widen a project's footprint.
//
// RemoveNamespace deliberately does NOT go through here: shedding a namespace
// only narrows the project's grants, and forcing a cluster admin into that loop
// would leave project owners unable to de-escalate their own project.
func (h *ProjectHandler) authorizeNamespaceAssignment(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID) bool {
	return h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, rbac.VerbUpdate)
}

// AddNamespace handles POST /api/v1/projects/{id}/add-namespace/.
//
// On success this writes the namespace into both the legacy projects.namespaces
// JSONB column AND the project_namespaces sidecar (used by the reconcile
// task to track per-namespace enforcement state). A project:reconcile task
// is enqueued so the agent applies the ResourceQuota / LimitRange /
// NetworkPolicy without waiting for the periodic sweep.
func (h *ProjectHandler) AddNamespace(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidID, "Invalid project ID")
		return
	}
	var req ProjectNamespaceRequest
	if err := decodeV1JSONBody(r, &req); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "Invalid JSON body")
		return
	}
	req.Namespace = strings.TrimSpace(req.Namespace)
	if req.Namespace == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "namespace is required")
		return
	}
	if !h.rejectReservedNamespaces(w, r, req.Namespace) {
		return
	}

	project, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Project not found")
		return
	}
	// Authority over the NAMESPACE (via its cluster), not over the project —
	// see authorizeNamespaceAssignment.
	if !h.authorizeNamespaceAssignment(w, r, project.ClusterID) {
		return
	}

	if err := projectdomain.ValidateNamespaceAddition(project, req.Namespace); err != nil {
		if strings.Contains(err.Error(), "already in this project") {
			apiutil.WriteError(w, r, http.StatusConflict, apiutil.Conflict, "Namespace '"+req.Namespace+"' is already in this project.")
			return
		}
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, err.Error())
		return
	}

	// Check for conflict with other projects in the same cluster.
	otherProjects, err := h.queries.ListProjectsByCluster(r.Context(), sqlc.ListProjectsByClusterParams{
		ClusterID:  project.ClusterID,
		QueryLimit: 1000,
	})
	if err == nil {
		for _, other := range otherProjects {
			if other.ID == project.ID {
				continue
			}
			for _, ns := range projectdomain.Namespaces(other.Namespaces) {
				if ns == req.Namespace {
					apiutil.WriteError(w, r, http.StatusConflict, apiutil.Conflict, "Namespace '"+req.Namespace+"' is already assigned to project '"+other.Name+"'.")
					return
				}
			}
		}
	}

	updated, txErr := executeMutation(r, h.runTx,
		func(q ProjectNamespaceTx) (sqlc.Project, error) {
			return projectdomain.AddNamespace(r.Context(), q, projectdomain.NamespaceMutation{
				ProjectID: id, ClusterID: project.ClusterID, Namespace: req.Namespace,
				IdempotencyKey: r.Header.Get("Idempotency-Key"),
			})
		},
		func(u sqlc.Project) mutationAuditEvent {
			return mutationAuditEvent{
				action: "project.add_namespace", resourceType: "project", resourceID: u.ID.String(), resourceName: u.Name,
				status: http.StatusOK, detail: map[string]any{"namespace": req.Namespace},
			}
		})
	if txErr != nil {
		switch {
		case errors.Is(txErr, audit.ErrOutboxUnavailable):
			respondTransactionalMutationError(w, r, txErr, http.StatusInternalServerError, apiutil.UpdateError, "Failed to update project")
		case errors.Is(txErr, projectdomain.ErrNamespaceAlreadyAssigned):
			apiutil.WriteError(w, r, http.StatusConflict, apiutil.Conflict, "Namespace '"+req.Namespace+"' is already in this project.")
		case errors.Is(txErr, projectdomain.ErrNamespaceOwnedElsewhere):
			apiutil.WriteError(w, r, http.StatusConflict, apiutil.Conflict, "Namespace '"+req.Namespace+"' is already assigned to another project on this cluster.")
		case errors.Is(txErr, projectdomain.ErrResourceCapAllocation):
			apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Project resource cap cannot allocate a bounded share to every namespace.")
		case errors.Is(txErr, pgx.ErrNoRows):
			apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Project not found")
		default:
			apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.UpdateError, "Failed to update project")
		}
		return
	}
	// Membership changed: flush the namespace-scoped RBAC cache so the new grant
	// is visible immediately instead of after the cache TTL.
	h.invalidateRBACCache()
	RespondJSON(w, http.StatusOK, projectToResponse(updated))
}

// RemoveNamespace handles POST /api/v1/projects/{id}/remove-namespace/.
//
// The membership state, sidecar deletion, cleanup intent, remaining namespace
// rebalance intents, and audit evidence commit atomically. Workers need only
// the task payload to remove the managed objects; they do not rely on a stale
// sidecar row surviving the mutation.
func (h *ProjectHandler) RemoveNamespace(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidID, "Invalid project ID")
		return
	}
	var req ProjectNamespaceRequest
	if err := decodeV1JSONBody(r, &req); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "Invalid JSON body")
		return
	}
	req.Namespace = strings.TrimSpace(req.Namespace)
	if req.Namespace == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "namespace is required")
		return
	}

	project, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Project not found")
		return
	}

	namespaces := projectdomain.Namespaces(project.Namespaces)
	found := false
	for _, ns := range namespaces {
		if ns == req.Namespace {
			found = true
		}
	}
	if !found {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Namespace '"+req.Namespace+"' is not in this project.")
		return
	}

	updated, txErr := executeMutation(r, h.runTx,
		func(q ProjectNamespaceTx) (sqlc.Project, error) {
			return projectdomain.RemoveNamespace(r.Context(), q, projectdomain.NamespaceMutation{
				ProjectID: id, ClusterID: project.ClusterID, Namespace: req.Namespace,
				IdempotencyKey: r.Header.Get("Idempotency-Key"),
			})
		},
		func(u sqlc.Project) mutationAuditEvent {
			return mutationAuditEvent{
				action: "project.remove_namespace", resourceType: "project", resourceID: u.ID.String(), resourceName: u.Name,
				status: http.StatusOK, detail: map[string]any{"namespace": req.Namespace},
			}
		})
	if txErr != nil {
		switch {
		case errors.Is(txErr, audit.ErrOutboxUnavailable):
			respondTransactionalMutationError(w, r, txErr, http.StatusInternalServerError, apiutil.UpdateError, "Failed to update project")
		case errors.Is(txErr, projectdomain.ErrNamespaceNotAssigned):
			apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Namespace '"+req.Namespace+"' is not in this project.")
		case errors.Is(txErr, pgx.ErrNoRows):
			apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Project not found")
		default:
			apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.UpdateError, "Failed to update project")
		}
		return
	}
	// Membership changed: flush the namespace-scoped RBAC cache so the revoked
	// grant stops authorizing immediately instead of after the cache TTL.
	h.invalidateRBACCache()
	RespondJSON(w, http.StatusOK, projectToResponse(updated))
}

func (h *ProjectHandler) recordProjectAudit(r *http.Request, action string, project sqlc.Project, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	recordAudit(r, h.queries, action, "project", project.ID.String(), project.Name, detail)
}

func decodeJSONArray(raw json.RawMessage) []any {
	if len(raw) == 0 {
		return []any{}
	}
	var items []any
	if json.Unmarshal(raw, &items) != nil {
		return []any{}
	}
	return items
}
