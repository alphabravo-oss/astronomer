package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *ToolHandler) List(w http.ResponseWriter, r *http.Request) {
	tools, err := h.queries.ListClusterTools(r.Context(), sqlc.ListClusterToolsParams{
		Limit:  int32(queryLimit(r, 20)),
		Offset: int32(queryOffset(r)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list tools")
		return
	}
	total, _ := h.queries.CountClusterTools(r.Context())
	items := make([]ToolResponse, 0, len(tools))
	for _, t := range tools {
		items = append(items, toolToResponse(t))
	}
	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
}

func (h *ToolHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err == nil {
		tool, err := h.queries.GetClusterToolByID(r.Context(), id)
		if err == nil {
			RespondJSON(w, http.StatusOK, toolToResponse(tool))
			return
		}
	}
	h.GetBySlug(w, r)
}

func (h *ToolHandler) GetBySlug(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	tool, err := h.queries.GetToolBySlug(r.Context(), slug)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Tool not found")
		return
	}
	RespondJSON(w, http.StatusOK, toolToResponse(tool))
}

func (h *ToolHandler) Preview(w http.ResponseWriter, r *http.Request) {
	_, req, plan, _, err := h.resolveAction(r)
	if err != nil {
		if errors.Is(err, errToolNotFound) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Tool not found")
			return
		}
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	charts := make([]map[string]any, 0, len(plan))
	for _, release := range plan {
		charts = append(charts, map[string]any{"chart_name": release.ChartName, "chart_version": release.Version, "namespace": release.Namespace, "release_name": release.ReleaseName, "values_yaml": release.ValuesYAML})
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"charts": charts,
		"preset": req.Preset,
	})
}

func (h *ToolHandler) Install(w http.ResponseWriter, r *http.Request) {
	tool, req, plan, _, err := h.resolveAction(r)
	if err != nil {
		if errors.Is(err, errToolNotFound) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Tool not found")
			return
		}
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	if tool.Slug == DexToolSlug {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "Dex is bundled with the Astronomer management chart; enable dex.enabled and use the Auth settings workflow")
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbCreate) {
		return
	}
	restoreToolActionRequestBody(r, req)
	// Migration 057: maintenance window gate. Look up the cluster's
	// labels for selector matching.
	if blocked := h.checkToolMaintenanceWindow(w, r, clusterID, "tool.install"); blocked {
		return
	}
	releaseName := req.ReleaseName
	if releaseName == "" {
		releaseName = tool.Slug
	}
	if _, err := h.findInstalledTool(r.Context(), clusterID, tool.Slug); err == nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Tool is already installed on cluster")
		return
	} else if !errors.Is(err, errInstalledChartNotFound) {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to lookup installed tool")
		return
	}
	if msg, ok := h.checkToolScope(r.Context(), tool.Slug, clusterID); !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.WrongClusterScope, msg)
		return
	}
	// Migration 067 — the values blob keeps its ${vault://...} markers in
	// both the enqueued payload and the installed_charts row (written by
	// the worker), so a rotated secret takes effect on next upgrade and no
	// cleartext secret is persisted to tool_operations.payload. Resolution
	// happens at execution time inside the reconciler (sendHelmRaw); the
	// resolved plaintext only exists in-memory on the wire. Tools install
	// at cluster scope; unqualified vault refs require the explicit
	// "${vault://<connection>/...}" form.
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	op, err := h.createAuditedToolOperation(r, "tool_installation", operationTargetKey(clusterID, tool.Slug), "install", toolOperationEnvelope{
		ClusterID: req.ClusterID,
		ToolSlug:  tool.Slug,
		Preset:    req.Preset,
		Releases:  plan,
	}, currentUserUUID(r), mutationAuditEvent{action: "tool.install", resourceType: "tool", resourceID: tool.ID.String(), resourceName: tool.Slug, status: http.StatusAccepted, detail: map[string]any{
		"cluster_id": req.ClusterID, "release_name": releaseName, "releases": toolPlanAudit(toolOperationEnvelope{Releases: plan}), "preset": req.Preset,
	}})
	if err != nil {
		respondToolMutationError(w, r, err, apierror.EnqueueError, "Failed to enqueue tool installation")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/tools/operations/"+op.ID.String()+"/", toolOperationResponse(op))
}

func (h *ToolHandler) Upgrade(w http.ResponseWriter, r *http.Request) {
	tool, req, plan, _, err := h.resolveAction(r)
	if err != nil {
		if errors.Is(err, errToolNotFound) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Tool not found")
			return
		}
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	restoreToolActionRequestBody(r, req)
	// Migration 057: maintenance window gate.
	if blocked := h.checkToolMaintenanceWindow(w, r, clusterID, "tool.upgrade"); blocked {
		return
	}
	existing, err := h.findInstalledTool(r.Context(), clusterID, tool.Slug)
	if err != nil {
		if errors.Is(err, errInstalledChartNotFound) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installed tool not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to lookup installed tool")
		return
	}
	releaseName := existing.ReleaseName
	if len(plan) == 1 {
		if req.ReleaseName != "" && req.ReleaseName != releaseName {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Upgrade cannot change the installed release name")
			return
		}
		plan[0].ReleaseName = releaseName
		plan[0].Namespace = existing.Namespace
	}
	// Migration 067 — the values blob keeps its ${vault://...} markers in
	// both the payload and the installed_charts row; the reconciler
	// (sendHelmRaw) resolves them in-memory at execution time so no
	// cleartext secret is persisted.
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	op, err := h.createAuditedToolOperation(r, "tool_installation", operationTargetKey(clusterID, tool.Slug), "upgrade", toolOperationEnvelope{
		ClusterID: req.ClusterID,
		ToolSlug:  tool.Slug,
		Preset:    req.Preset,
		Releases:  plan,
	}, currentUserUUID(r), mutationAuditEvent{action: "tool.upgrade", resourceType: "tool", resourceID: tool.ID.String(), resourceName: tool.Slug, status: http.StatusAccepted, detail: map[string]any{
		"cluster_id": req.ClusterID, "release_name": releaseName, "releases": toolPlanAudit(toolOperationEnvelope{Releases: plan}), "preset": req.Preset,
	}})
	if err != nil {
		respondToolMutationError(w, r, err, apierror.EnqueueError, "Failed to enqueue tool upgrade")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/tools/operations/"+op.ID.String()+"/", toolOperationResponse(op))
}

func (h *ToolHandler) Uninstall(w http.ResponseWriter, r *http.Request) {
	h.queueToolReversal(w, r, "uninstall", rbac.VerbDelete)
}

func (h *ToolHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	h.queueToolReversal(w, r, "rollback", rbac.VerbUpdate)
}

func (h *ToolHandler) queueToolReversal(w http.ResponseWriter, r *http.Request, operation string, verb rbac.Verb) {
	slug := chi.URLParam(r, "slug")
	tool, err := h.queries.GetToolBySlug(r.Context(), slug)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Tool not found")
		return
	}
	var req toolUninstallRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, verb) {
		return
	}
	restoreToolActionRequestBody(r, toolActionRequest{ClusterID: req.ClusterID})
	// Migration 057: maintenance window gate.
	if blocked := h.checkToolMaintenanceWindow(w, r, clusterID, "tool."+operation); blocked {
		return
	}
	// The durable plan also owns releases created just before a process crash,
	// even when no installed_charts row was committed. Execution verifies the
	// exact Helm operation marker before removing such a release.
	previous, err := h.queries.GetLatestToolOperationForTarget(r.Context(), sqlc.GetLatestToolOperationForTargetParams{TargetType: "tool_installation", TargetKey: operationTargetKey(clusterID, slug)})
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Installed tool has no durable release plan")
		return
	}
	env, err := resetToolPlan(previous.Payload)
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, err.Error())
		return
	}
	if previous.Status == "running" || previous.Status == "pending" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Wait for the active tool operation to finish")
		return
	}
	if operation == "rollback" {
		if previous.OperationType != "install" && previous.OperationType != "upgrade" {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Only an install or upgrade plan can be rolled back")
			return
		}
		var source toolOperationEnvelope
		if err := json.Unmarshal(previous.Payload, &source); err != nil {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Invalid source release plan")
			return
		}
		for i := range env.Releases {
			env.Releases[i].RollbackRevision = source.Releases[i].PreviousRevision
			env.Releases[i].ValuesYAML = source.Releases[i].PreviousValuesYAML
			if source.Releases[i].State == "pending" || (previous.OperationType == "install" && source.Releases[i].PreviousRevision > 0) {
				env.Releases[i].State = "completed"
			}
		}
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	op, err := h.createAuditedToolOperation(r, "tool_installation", operationTargetKey(clusterID, slug), operation, env, currentUserUUID(r), mutationAuditEvent{action: "tool." + operation, resourceType: "tool", resourceID: tool.ID.String(), resourceName: slug, status: http.StatusAccepted, detail: map[string]any{
		"cluster_id": req.ClusterID, "releases": toolPlanAudit(env), "source_operation_id": previous.ID.String(),
	}})
	if err != nil {
		respondToolMutationError(w, r, err, apierror.EnqueueError, "Failed to enqueue tool "+operation)
		return
	}
	RespondAcceptedOperation(w, "/api/v1/tools/operations/"+op.ID.String()+"/", toolOperationResponse(op))
}

func (h *ToolHandler) Adopt(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	tool, err := h.queries.GetToolBySlug(r.Context(), slug)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Tool not found")
		return
	}
	var req toolActionRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbCreate) {
		return
	}
	if req.ReleaseName == "" {
		req.ReleaseName = tool.Slug
	}
	plan, err := buildToolReleasePlan(tool, req.ReleaseName, "")
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	op, err := h.createAuditedToolOperation(r, "tool_installation", operationTargetKey(clusterID, slug), "adopt", toolOperationEnvelope{
		ClusterID: req.ClusterID,
		ToolSlug:  slug,
		Releases:  plan,
	}, currentUserUUID(r), mutationAuditEvent{action: "tool.adopt", resourceType: "tool", resourceID: tool.ID.String(), resourceName: slug, status: http.StatusAccepted, detail: map[string]any{
		"cluster_id": req.ClusterID, "releases": toolPlanAudit(toolOperationEnvelope{Releases: plan}),
	}})
	if err != nil {
		respondToolMutationError(w, r, err, apierror.EnqueueError, "Failed to enqueue tool adoption")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/tools/operations/"+op.ID.String()+"/", toolOperationResponse(op))
}
