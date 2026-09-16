package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/projectquota"
	projectdomain "github.com/alphabravocompany/astronomer-go/internal/projects"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *ProjectHandler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
		return
	}

	var req UpdateProjectPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.normalizeLegacyQuotaFields()

	existing, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	if blocked, err := projectUpdateBlockedByOwnership(r.Context(), h.queries, id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to check project ownership")
		return
	} else if blocked != "" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, blocked)
		return
	}

	updated, err := h.service.UpdatePolicy(r.Context(), existing, projectdomain.PolicyInput{
		PodSecurityProfile:       req.PodSecurityProfile,
		ResourceQuotaCPULimit:    req.ResourceQuotaCpuLimit,
		ResourceQuotaMemoryLimit: req.ResourceQuotaMemoryLimit,
		ResourceQuotaPodCount:    req.ResourceQuotaPodCount,
		NetworkPolicyMode:        req.NetworkPolicyMode,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update project policy")
		return
	}
	h.recordProjectAudit(r, "project.update_policy", updated, map[string]any{
		"pod_security_profile":        updated.PodSecurityProfile,
		"resource_quota_cpu_limit":    updated.ResourceQuotaCpuLimit,
		"resource_quota_memory_limit": updated.ResourceQuotaMemoryLimit,
		"resource_quota_pod_count":    updated.ResourceQuotaPodCount,
		"resource_cap_scope":          "project",
		"allocation_strategy":         "equal-share-lexical",
		"network_policy_mode":         updated.NetworkPolicyMode,
	})
	for _, namespace := range projectdomain.Namespaces(updated.Namespaces) {
		if err := h.service.EnsureAndEnqueue(r.Context(), updated.ID, updated.ClusterID, namespace); err != nil {
			h.logger().Warn("persist project reconcile intent", "project_id", updated.ID, "namespace", namespace, "error", err)
		}
	}
	RespondJSON(w, http.StatusOK, projectToResponse(updated))
}

// QuotaUsage handles GET /api/v1/projects/{id}/quota-usage/.
//
// For each (cluster, namespace) pair owned by the project, fan out to the
// agent and fetch the live ResourceQuota.status.used + spec.hard for the
// managed astronomer-project-quota object. Errors are surfaced per-cluster
// in the same shape resources_search.go uses so a single broken tunnel
// doesn't kill the whole response.
func (h *ProjectHandler) QuotaUsage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
		return
	}
	if h.requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TunnelUnavailable, "Cluster tunnel is not configured")
		return
	}

	project, err := h.queries.GetProjectByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}

	rows, err := h.queries.ListProjectNamespaces(r.Context(), project.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list project namespaces")
		return
	}

	type quotaItem struct {
		ClusterID   string                     `json:"cluster_id"`
		ClusterName string                     `json:"cluster_name"`
		Namespace   string                     `json:"namespace"`
		Used        map[string]any             `json:"used"`
		Hard        map[string]any             `json:"hard"`
		Allocation  ProjectResourceCapResponse `json:"allocation"`
	}
	type quotaErr struct {
		ClusterID   string `json:"cluster_id"`
		ClusterName string `json:"cluster_name"`
		Namespace   string `json:"namespace"`
		Error       string `json:"error"`
	}

	items := make([]quotaItem, 0, len(rows))
	errs := make([]quotaErr, 0)
	namespaces := make([]string, 0, len(rows))
	for _, row := range rows {
		namespaces = append(namespaces, row.Namespace)
	}
	projectCap := projectquota.Cap{CPU: project.ResourceQuotaCpuLimit, Memory: project.ResourceQuotaMemoryLimit, Pods: project.ResourceQuotaPodCount}
	allocations, allocationErr := projectquota.Allocate(projectCap, namespaces)
	if allocationErr != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Project resource cap is invalid")
		return
	}
	clusterNameCache := map[uuid.UUID]string{}
	for _, row := range rows {
		clusterName, ok := clusterNameCache[row.ClusterID]
		if !ok {
			cluster, cerr := h.queries.GetClusterByID(r.Context(), row.ClusterID)
			if cerr == nil {
				clusterName = clusterDisplayName(cluster)
			} else {
				clusterName = row.ClusterID.String()
			}
			clusterNameCache[row.ClusterID] = clusterName
		}

		path := "/api/v1/namespaces/" + row.Namespace + "/resourcequotas/astronomer-project-quota"
		resp, derr := h.requester.Do(r.Context(), row.ClusterID.String(), http.MethodGet, path, nil, requestHeaders(""))
		if derr != nil {
			errs = append(errs, quotaErr{
				ClusterID:   row.ClusterID.String(),
				ClusterName: clusterName,
				Namespace:   row.Namespace,
				Error:       derr.Error(),
			})
			continue
		}
		// 404 = no quota object yet. Surface it as an empty result rather
		// than as a hard error — the project may simply have unbounded
		// policy in this namespace.
		if resp.StatusCode == http.StatusNotFound {
			items = append(items, quotaItem{
				ClusterID:   row.ClusterID.String(),
				ClusterName: clusterName,
				Namespace:   row.Namespace,
				Used:        map[string]any{},
				Hard:        map[string]any{},
				Allocation:  projectResourceCapResponse(allocations[row.Namespace]),
			})
			continue
		}
		if resp.StatusCode >= http.StatusBadRequest {
			errs = append(errs, quotaErr{
				ClusterID:   row.ClusterID.String(),
				ClusterName: clusterName,
				Namespace:   row.Namespace,
				Error:       fmt.Sprintf("agent returned %d", resp.StatusCode),
			})
			continue
		}
		body, derr := decodeResponseBody(resp)
		if derr != nil {
			errs = append(errs, quotaErr{
				ClusterID:   row.ClusterID.String(),
				ClusterName: clusterName,
				Namespace:   row.Namespace,
				Error:       "decode body: " + derr.Error(),
			})
			continue
		}
		var doc struct {
			Spec struct {
				Hard map[string]any `json:"hard"`
			} `json:"spec"`
			Status struct {
				Used map[string]any `json:"used"`
				Hard map[string]any `json:"hard"`
			} `json:"status"`
		}
		if len(body) > 0 {
			if uerr := json.Unmarshal(body, &doc); uerr != nil {
				errs = append(errs, quotaErr{
					ClusterID:   row.ClusterID.String(),
					ClusterName: clusterName,
					Namespace:   row.Namespace,
					Error:       "unmarshal quota: " + uerr.Error(),
				})
				continue
			}
		}
		hard := doc.Status.Hard
		if hard == nil {
			hard = doc.Spec.Hard
		}
		if hard == nil {
			hard = map[string]any{}
		}
		used := doc.Status.Used
		if used == nil {
			used = map[string]any{}
		}
		items = append(items, quotaItem{
			ClusterID:   row.ClusterID.String(),
			ClusterName: clusterName,
			Namespace:   row.Namespace,
			Used:        used,
			Hard:        hard,
			Allocation:  projectResourceCapResponse(allocations[row.Namespace]),
		})
	}
	allocated := projectquota.Cap{}
	remaining := projectCap
	if len(namespaces) > 0 {
		// Allocate conserves every bounded dimension exactly, including lexical
		// remainder distribution. No namespace means no allocation.
		allocated = projectCap
		remaining = projectquota.Cap{}
	}

	RespondJSON(w, http.StatusOK, map[string]any{
		"results": items,
		"errors":  errs,
		"project_cap": ProjectResourceQuotaSummaryResponse{
			Total:     projectResourceCapResponse(projectCap),
			Allocated: projectResourceCapResponse(allocated),
			Remaining: projectResourceCapResponse(remaining),
		},
	})
}

// Delete handles DELETE /api/v1/projects/{id}/.
