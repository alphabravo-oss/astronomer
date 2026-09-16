package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type clusterTemplateInUseError struct{ count int64 }

func (e *clusterTemplateInUseError) Error() string {
	return fmt.Sprintf("cluster template is applied to %d cluster(s)", e.count)
}

// ────────────────────────────────────────────────────────────────────────
// Template CRUD
// ────────────────────────────────────────────────────────────────────────

// List handles GET /api/v1/cluster-templates/.
func (h *ClusterTemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.queries.ListClusterTemplates(r.Context(), sqlc.ListClusterTemplatesParams{
		Limit:  int32(queryLimit(r, 20)),
		Offset: int32(queryOffset(r)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list cluster templates")
		return
	}
	total, _ := h.queries.CountClusterTemplates(r.Context())
	resp := make([]ClusterTemplateResponse, 0, len(items))
	for _, t := range items {
		resp = append(resp, templateToResponse(t))
	}
	paging.Write(w, resp, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(resp)))
}

// Get handles GET /api/v1/cluster-templates/{id}/.
func (h *ClusterTemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster template not found")
		return
	}
	RespondJSON(w, http.StatusOK, templateToResponse(tmpl))
}

// ListBoundClusters handles GET /api/v1/cluster-templates/{id}/clusters/.
func (h *ClusterTemplateHandler) ListBoundClusters(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	if _, err := h.queries.GetClusterTemplateByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster template not found")
		return
	}
	all, allowedClusterIDs, _, err := h.authz.authorizedScopeIDs(
		r.Context(), rbac.ResourceClusters, rbac.VerbRead, rbac.NarrowedClustersWiden,
	)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	rows, err := h.queries.ListClusterTemplateBoundClusters(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters bound to template")
		return
	}
	items := make([]ClusterTemplateBoundClusterResponse, 0, len(rows))
	allowed := make(map[uuid.UUID]struct{}, len(allowedClusterIDs))
	for _, clusterID := range allowedClusterIDs {
		allowed[clusterID] = struct{}{}
	}
	for _, row := range rows {
		if !all {
			if _, ok := allowed[row.ClusterID]; !ok {
				continue
			}
		}
		item := ClusterTemplateBoundClusterResponse{
			ClusterID: row.ClusterID.String(), ClusterName: row.ClusterName,
			Status: row.Status, Message: row.LastError,
		}
		if row.AppliedAt.Valid {
			item.LastAppliedAt = row.AppliedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		items = append(items, item)
	}
	RespondJSON(w, http.StatusOK, items)
}

// Create handles POST /api/v1/cluster-templates/.
func (h *ClusterTemplateHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateClusterTemplateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}
	// T6.074 — operators may not create a template with one of the
	// reserved platform-baseline names; the platform owns those.
	if isBuiltinTemplate(req.Name) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinTemplate,
			fmt.Sprintf("%q is a reserved platform-baseline template name.", req.Name))

		return
	}
	spec := req.Spec
	if len(spec) == 0 {
		spec = json.RawMessage(`{}`)
	}
	if err := validateTemplateSpec(spec); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}

	params := sqlc.CreateClusterTemplateParams{
		Name:        req.Name,
		Description: req.Description,
		Spec:        spec,
		CreatedBy:   currentUserUUID(r),
	}
	tmpl, err := executeMutation(r, h.runTx,
		func(q ClusterTemplateMutationTx) (sqlc.ClusterTemplate, error) {
			return q.CreateClusterTemplate(r.Context(), params)
		},
		func(row sqlc.ClusterTemplate) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.cluster_template.created", resourceType: "cluster_template",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated,
				detail: map[string]any{"description": row.Description},
			}
		})
	if err != nil {
		// Unique-name conflict on cluster_templates_name_key bubbles up as
		// a 23505. Translate so the UI sees a clean 409 rather than 500.
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A template with this name already exists")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create cluster template")
		return
	}
	w.Header().Set("Location", "/api/v1/cluster-templates/"+tmpl.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, templateToResponse(tmpl))
}

// Update handles PUT /api/v1/cluster-templates/{id}/.
func (h *ClusterTemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	// T6.074 — refuse mutations to platform-baseline templates so an
	// upgrade doesn't have to handle a half-renamed builtin. The row
	// is loaded here (not inside UpdateClusterTemplate) so we can
	// pre-empt the SQL UPDATE and return a clean 403.
	if existing, gerr := h.queries.GetClusterTemplateByID(r.Context(), id); gerr == nil {
		if isBuiltinTemplate(existing.Name) {
			RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinTemplate,
				fmt.Sprintf("%q is a platform-baseline template and cannot be edited.", existing.Name))

			return
		}
	}
	var req CreateClusterTemplateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}
	// Also refuse renaming AWAY from a builtin (defence in depth — the
	// existing.Name check above catches renaming a builtin; this catches
	// renaming a non-builtin TO a reserved name).
	if isBuiltinTemplate(req.Name) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinTemplate,
			fmt.Sprintf("%q is a reserved platform-baseline template name.", req.Name))

		return
	}
	spec := req.Spec
	if len(spec) == 0 {
		spec = json.RawMessage(`{}`)
	}
	if err := validateTemplateSpec(spec); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	params := sqlc.UpdateClusterTemplateParams{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Spec:        spec,
	}
	tmpl, err := executeMutation(r, h.runTx,
		func(q ClusterTemplateMutationTx) (sqlc.ClusterTemplate, error) {
			return q.UpdateClusterTemplate(r.Context(), params)
		},
		func(row sqlc.ClusterTemplate) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.cluster_template.updated", resourceType: "cluster_template",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusOK,
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster template not found")
			return
		}
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A template with this name already exists")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update cluster template")
		return
	}
	RespondJSON(w, http.StatusOK, templateToResponse(tmpl))
}

// isBuiltinTemplate returns true for the well-known templates the
// platform ships and reconciles on its own. Operators are allowed to
// reapply them, inspect them, and bind them to clusters — but Update
// and Delete are refused so an upgrade doesn't have to deal with a
// half-renamed or missing baseline template. The set is small and
// closed; extending it requires a code change. (T6.074)
func isBuiltinTemplate(name string) bool {
	switch name {
	case "platform-baseline", "platform-default":
		return true
	}
	return false
}

// Delete handles DELETE /api/v1/cluster-templates/{id}/. Refuses to
// remove a template that's still applied to at least one cluster — the
// operator must detach those bindings first. We do the count-first check
// (instead of relying on the FK violation) so the 409 body can include
// the exact reason without parsing pgconn error codes.
func (h *ClusterTemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid template ID")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster template not found")
		return
	}
	if isBuiltinTemplate(tmpl.Name) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.BuiltinTemplate,
			fmt.Sprintf("%q is a platform-baseline template and cannot be deleted.", tmpl.Name))

		return
	}
	count, err := h.queries.CountClusterTemplateApplicationsByTemplate(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to count template applications")
		return
	}
	if count > 0 {
		RespondRequestError(w, r, http.StatusConflict, apierror.TemplateInUse,
			fmt.Sprintf("Template is applied to %d cluster(s); detach it from those clusters before deleting.", count))

		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q ClusterTemplateMutationTx) (struct{}, error) {
			// Repeat the in-use check inside the transaction so a concurrent
			// binding cannot race the audit decision. The FK remains the final
			// integrity guard.
			applications, countErr := q.CountClusterTemplateApplicationsByTemplate(r.Context(), id)
			if countErr != nil {
				return struct{}{}, countErr
			}
			if applications > 0 {
				return struct{}{}, &clusterTemplateInUseError{count: applications}
			}
			return struct{}{}, q.DeleteClusterTemplate(r.Context(), id)
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.cluster_template.deleted", resourceType: "cluster_template",
				resourceID: tmpl.ID.String(), resourceName: tmpl.Name, status: http.StatusNoContent,
			}
		})
	if err != nil {
		var inUse *clusterTemplateInUseError
		if errors.As(err, &inUse) {
			RespondRequestError(w, r, http.StatusConflict, apierror.TemplateInUse,
				fmt.Sprintf("Template is applied to %d cluster(s); detach it from those clusters before deleting.", inUse.count))
			return
		}
		// Belt-and-suspenders: the count check above closes the race for
		// normal traffic, but a concurrent POST to /clusters/{id}/template/
		// could insert a binding between count and delete. Treat the FK
		// violation as the same 409.
		if isFKRestrictViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.TemplateInUse, "Template is in use; detach from clusters first.")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete cluster template")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ────────────────────────────────────────────────────────────────────────
// Error classification helpers
// ────────────────────────────────────────────────────────────────────────

// isUniqueViolation returns true for Postgres unique_violation (23505).
// Matches the pattern used by ToolHandler.EnsureInstalled.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	// Fallback: some pooled/wrapped error paths don't preserve *pgconn.PgError
	// for errors.As, so match the stable SQLSTATE text (cf. db.isUndefinedTable
	// which matches 42P01 by string for the same reason).
	return strings.Contains(err.Error(), "SQLSTATE 23505")
}

// isFKRestrictViolation returns true for PostgreSQL 23503 when a deliberate
// ON DELETE RESTRICT policy requires the caller to detach dependants first.
func isFKRestrictViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}
