package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	projectdomain "github.com/alphabravocompany/astronomer-go/internal/projects"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))
	search := strings.TrimSpace(r.URL.Query().Get("search"))

	// Scope filter — mirrors ClusterHandler.List. The collection gate admits
	// callers bound only to a cluster or a project; the page is narrowed to the
	// projects they own directly plus every project on a cluster they hold
	// projects:list over. all==true keeps the original unfiltered path.
	//
	// NarrowedClustersExcluded, unlike ClusterHandler.List: a namespace-narrowed
	// cluster binding must NOT widen to every project on that cluster. Projects
	// live inside a cluster, so widening would list a neighbouring tenant's
	// projects to a namespace-confined caller whose GET /projects/{id}/ 403s on
	// every one of those rows. That shape is not exotic — expandProjectBindings
	// emits it for every project binding when namespace-scoped RBAC is on.
	all, clusterIDs, projectIDs, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceProjects, rbac.VerbList, rbac.NarrowedClustersExcluded)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}

	var projects []sqlc.Project
	var total int64
	if all {
		projects, err = h.queries.ListProjects(r.Context(), sqlc.ListProjectsParams{
			FilterSearch: search,
			QueryLimit:   limit,
			QueryOffset:  offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list projects")
			return
		}
		if search == "" {
			total, err = h.queries.CountProjects(r.Context())
		} else {
			total, err = h.queries.CountProjectsFiltered(r.Context(), search)
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count projects")
			return
		}
	} else {
		scoped, ok := h.queries.(projectScopeQuerier)
		if !ok {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Scoped project listing is not available")
			return
		}
		projects, err = scoped.ListProjectsForScopes(r.Context(), sqlc.ListProjectsForScopesParams{
			ProjectIds:   projectIDs,
			ClusterIds:   clusterIDs,
			FilterSearch: search,
			QueryLimit:   limit,
			QueryOffset:  offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list projects")
			return
		}
		// Same predicate as the page — see CountClustersForScopes.
		total, err = scoped.CountProjectsForScopes(r.Context(), sqlc.CountProjectsForScopesParams{
			ProjectIds:   projectIDs,
			ClusterIds:   clusterIDs,
			FilterSearch: search,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count projects")
			return
		}
	}

	items, err := h.projectNavigationResponses(r.Context(), projects)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to read project namespace scopes")
		return
	}

	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
}

// Create handles POST /api/v1/projects/.
func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}

	var req CreateProjectRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid cluster_id")
		return
	}

	var createdByID pgtype.UUID
	if uid, err := uuid.Parse(user.ID); err == nil {
		createdByID = pgtype.UUID{Bytes: uid, Valid: true}
	}

	if req.Namespaces == nil {
		req.Namespaces = json.RawMessage(`[]`)
	}
	// Create seeds project_namespaces from this list (below), so it is a
	// namespace-claim path exactly like AddNamespace and gets the same two
	// guards. The cluster-authority check only fires when the body actually
	// names namespaces: creating an empty project stays a projects:create act.
	if seeded := projectdomain.Namespaces(req.Namespaces); len(seeded) > 0 {
		if !h.rejectReservedNamespaces(w, r, seeded...) {
			return
		}
		if !h.authorizeNamespaceAssignment(w, r, clusterID) {
			return
		}
	}
	project, err := h.service.Create(r.Context(), projectdomain.CreateInput{
		Name: req.Name, DisplayName: req.DisplayName, Description: req.Description,
		ClusterID: clusterID, Namespaces: req.Namespaces, ResourceQuota: req.ResourceQuota,
		LimitRange: req.LimitRange, NetworkPolicyMode: req.NetworkPolicyMode,
		CreatedByID: createdByID, PodSecurityProfile: req.PodSecurityProfile,
		ResourceQuotaCPULimit:    req.ResourceQuotaCpuLimit,
		ResourceQuotaMemoryLimit: req.ResourceQuotaMemoryLimit,
		ResourceQuotaPodCount:    req.ResourceQuotaPodCount,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}

	// Per-project cluster-density cap (migration 051, max_clusters_per_project).
	// CheckProjectClusterAdd resolves the project's cluster + effective plan
	// from the row, so it can only run once the project exists; on a breach we
	// roll the insert back before it grows a namespace/reconcile footprint.
	if h.enforcer != nil {
		if qerr := h.enforcer.CheckProjectClusterAdd(r.Context(), project.ID); qerr != nil {
			if derr := h.queries.DeleteProject(r.Context(), project.ID); derr != nil {
				h.logger().Warn("failed to roll back over-quota project", "project_id", project.ID.String(), "error", derr)
			}
			if qe, ok := quota.IsQuotaExceeded(qerr); ok {
				WriteQuotaExceeded(w, qe)
				return
			}
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.QuotaCheckError, "Failed to evaluate project cluster quota")
			return
		}
	}

	h.recordProjectAudit(r, "project.create", project, map[string]any{"clusterId": req.ClusterID, "namespaces": decodeJSONArray(req.Namespaces)})

	// Seed the project_namespaces sidecar from any namespaces specified at
	// create time. A reconcile is enqueued for each so enforcement lands
	// without an extra round-trip.
	for _, ns := range projectdomain.Namespaces(project.Namespaces) {
		if err := h.service.EnsureAndEnqueue(r.Context(), project.ID, project.ClusterID, ns); err != nil {
			h.logger().Warn("persist project reconcile intent", "project_id", project.ID, "namespace", ns, "error", err)
		}
	}

	w.Header().Set("Location", "/api/v1/projects/"+project.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, projectToResponse(project))
}

// Get handles GET /api/v1/projects/{id}/.
func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
		return
	}

	project, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	items, err := h.projectNavigationResponses(r.Context(), []sqlc.Project{project})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to read project namespace scopes")
		return
	}
	RespondJSON(w, http.StatusOK, items[0])
}

// Update handles PUT /api/v1/projects/{id}/.
func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
		return
	}

	var req UpdateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.normalizeLegacyQuotaFields()

	// req.Namespaces is deliberately NOT defaulted to `[]` here: the field is
	// absent on every policy-only PATCH, and treating absent as "clear" would
	// silently unassign the project's whole namespace set. It is filled from
	// the existing row below once that row is loaded.
	if req.ResourceQuota == nil {
		req.ResourceQuota = json.RawMessage(`{}`)
	}
	if req.LimitRange == nil {
		req.LimitRange = json.RawMessage(`{}`)
	}
	if req.NetworkPolicyMode == "" {
		req.NetworkPolicyMode = "none"
	}

	// Preserve existing policy fields when the client omits them. An old
	// client (pre-040) doesn't send these columns; without this load it would
	// reset them to "" on every PUT.
	existing, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}

	// Namespace membership diff. Update is the SECOND door onto the same
	// escalation and revocation surfaces as AddNamespace/RemoveNamespace:
	//   - additions expand every member's synthetic namespace-scoped cluster
	//     bindings, so they need the same cluster authority and the same
	//     reserved-namespace denylist;
	//   - removals used to drop the namespace from the JSONB while leaving the
	//     project_namespaces sidecar row in place. That sidecar is what
	//     expandProjectBindings reads, and the reconciler re-applies rows rather
	//     than pruning them, so the revoked namespace kept minting bindings
	//     forever — a permanent revocation hole, not a TTL one.
	// Both are reconciled here, and the RBAC cache is flushed after the write
	// exactly as AddNamespace/RemoveNamespace do.
	plan, err := projectdomain.PlanUpdate(existing, projectdomain.UpdateInput{
		DisplayName: req.DisplayName, Description: req.Description,
		Namespaces: req.Namespaces, ResourceQuota: req.ResourceQuota, LimitRange: req.LimitRange,
		NetworkPolicyMode: req.NetworkPolicyMode, PodSecurityProfile: req.PodSecurityProfile,
		ResourceQuotaCPULimit:    req.ResourceQuotaCpuLimit,
		ResourceQuotaMemoryLimit: req.ResourceQuotaMemoryLimit,
		ResourceQuotaPodCount:    req.ResourceQuotaPodCount,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	req.Namespaces = plan.Params.Namespaces
	added, removed := plan.Added, plan.Removed
	if len(added) > 0 {
		// Only ADDITIONS need cluster authority, mirroring
		// AddNamespace/RemoveNamespace: shedding a namespace narrows the
		// project's grants and stays a project-owner action.
		if !h.rejectReservedNamespaces(w, r, added...) {
			return
		}
		if !h.authorizeNamespaceAssignment(w, r, existing.ClusterID) {
			return
		}
	}
	if blocked, err := projectUpdateBlockedByOwnership(r.Context(), h.queries, id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to check project ownership")
		return
	} else if blocked != "" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, blocked)
		return
	}
	project, _, err := h.service.Update(r.Context(), existing, projectdomain.UpdateInput{
		DisplayName: req.DisplayName, Description: req.Description,
		Namespaces: req.Namespaces, ResourceQuota: req.ResourceQuota, LimitRange: req.LimitRange,
		NetworkPolicyMode: req.NetworkPolicyMode, PodSecurityProfile: req.PodSecurityProfile,
		ResourceQuotaCPULimit:    req.ResourceQuotaCpuLimit,
		ResourceQuotaMemoryLimit: req.ResourceQuotaMemoryLimit,
		ResourceQuotaPodCount:    req.ResourceQuotaPodCount,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	h.recordProjectAudit(r, "project.update", project, map[string]any{"namespaces": decodeJSONArray(req.Namespaces)})

	// Re-enqueue every namespace currently on the project so any quota /
	// limit / network-policy changes from this Update propagate. Cheap:
	// asynq dedupes by payload; the periodic sweep also re-converges.
	for _, ns := range projectdomain.Namespaces(project.Namespaces) {
		if err := h.service.EnsureAndEnqueue(r.Context(), project.ID, project.ClusterID, ns); err != nil {
			h.logger().Warn("persist project reconcile intent", "project_id", project.ID, "namespace", ns, "error", err)
		}
	}
	// Prune the sidecar rows for namespaces this Update dropped. Without this
	// the row survives and expandProjectBindings keeps minting a synthetic
	// namespace-scoped cluster binding for it forever — nothing converges it,
	// because the reconciler re-applies project_namespaces rows and never
	// deletes them.
	for _, ns := range removed {
		if err := h.service.CleanupAndEnqueue(r.Context(), project.ID, project.ClusterID, ns); err != nil {
			h.logger().Warn("failed to prune project namespace on update",
				"project_id", project.ID.String(), "namespace", ns, "error", err)
		}
	}
	if len(added) > 0 || len(removed) > 0 {
		// Membership changed: flush the RBAC binding cache so the grant or the
		// revocation takes effect on the next request, not after the TTL.
		h.invalidateRBACCache()
	}

	RespondJSON(w, http.StatusOK, projectToResponse(project))
}

func projectUpdateBlockedByOwnership(ctx context.Context, q any, id uuid.UUID) (string, error) {
	ownershipQ, ok := q.(projectOwnershipQuerier)
	if !ok {
		return "", nil
	}
	ownership, err := ownershipQ.GetProjectOwnership(ctx, id)
	if err != nil {
		return "", err
	}
	if ownership.ManagedBy != "crd" {
		return "", nil
	}
	return fmt.Sprintf("Project is managed by CRD %s/%s %s/%s; edit the Kubernetes resource or transfer ownership before using this API.",
		ownership.ExternalRefApiVersion,
		ownership.ExternalRefKind,
		ownership.ExternalRefNamespace,
		ownership.ExternalRefName,
	), nil
}

// TakeoverOwnership handles POST /api/v1/projects/{id}/ownership/takeover/.
//
// This is the explicit UI/API ownership transfer path for CRD-owned projects.
// Ordinary updates remain blocked until the operator chooses this endpoint.
