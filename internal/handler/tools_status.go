package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func (h *ToolHandler) ClusterStatus(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	tools, err := h.queries.ListEnabledTools(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list tools")
		return
	}
	bySlug := map[string][]sqlc.InstalledChart{}
	for offset := int32(0); ; offset += 200 {
		installed, err := h.queries.ListInstalledChartsByCluster(r.Context(), sqlc.ListInstalledChartsByClusterParams{ClusterID: clusterID, Limit: 200, Offset: offset})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list installed tool releases")
			return
		}
		for _, item := range installed {
			if item.ToolSlug.Valid {
				bySlug[item.ToolSlug.String] = append(bySlug[item.ToolSlug.String], item)
			}
		}
		if len(installed) < 200 {
			break
		}
	}
	// Scope the in-flight operation lookup to this cluster's tool targets
	// instead of scanning the newest 200 pending + 200 running ops
	// globally (which silently dropped in-flight ops on busy fleets). Each
	// enabled tool's latest op is fetched by its indexed (target_type,
	// target_key) tuple; only pending/running ops surface as a live badge.
	opBySlug := map[string]sqlc.ToolOperation{}
	for _, tool := range tools {
		op, err := h.queries.GetLatestToolOperationForTarget(r.Context(), sqlc.GetLatestToolOperationForTargetParams{
			TargetType: "tool_installation",
			TargetKey:  operationTargetKey(clusterID, tool.Slug),
		})
		if err != nil {
			continue
		}
		opBySlug[tool.Slug] = op
	}
	statuses := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		status := map[string]any{
			"slug":         tool.Slug,
			"name":         tool.Name,
			"status":       "not_installed",
			"release_name": nil,
			"namespace":    nil,
			"preset_used":  nil,
			"error":        nil,
		}
		if rows := bySlug[tool.Slug]; len(rows) > 0 {
			item := rows[0]
			status["status"], status["error"] = installedToolPlanStatus(tool, rows, opBySlug[tool.Slug])
			status["release_name"] = item.ReleaseName
			status["namespace"] = item.Namespace
			if item.PresetUsed.Valid {
				status["preset_used"] = item.PresetUsed.String
			}
		}
		if op, ok := opBySlug[tool.Slug]; ok {
			status["operation"] = toolOperationResponse(op)
			if op.Status == "failed" {
				status["status"] = "failed"
				status["error"] = op.ErrorMessage
			}
			if op.Status != OpStatusPending && op.Status != OpStatusRunning {
				statuses = append(statuses, status)
				continue
			}
			switch op.OperationType {
			case "install", "adopt":
				status["status"] = "installing"
			case "upgrade", "rollback":
				status["status"] = "upgrading"
			case "uninstall":
				status["status"] = "uninstalling"
			}
		}
		statuses = append(statuses, status)
	}
	// Per-cluster tool status is a complete scan of enabled tools.
	paging.Write(w, statuses, paging.Exact(len(statuses), len(statuses), 0, len(statuses)))
}

func (h *ToolHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load tool operations")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *ToolHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	ops, err := h.queries.ListToolOperations(ctx, sqlc.ListToolOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	opSummary := summarizeOperations(ctx, ops, operationStatusSummaryConfig[sqlc.ToolOperation]{
		Status:    func(op sqlc.ToolOperation) string { return op.Status },
		CreatedAt: func(op sqlc.ToolOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.ToolOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > time.Minute
		},
		Include: func(_ context.Context, op sqlc.ToolOperation) bool {
			if !restricted {
				return true
			}
			clusterID, err := toolOperationClusterID(op)
			return err == nil && h.authz.allowsCluster(bindings, clusterID, rbac.ResourceCatalog, rbac.VerbRead)
		},
		Preview:               func(ctx context.Context, op sqlc.ToolOperation) map[string]any { return h.operationPreview(ctx, op) },
		StaleThresholdSeconds: 60,
	})
	toolCount, _ := h.queries.CountClusterTools(ctx)
	installedCount, _ := h.queries.CountInstalledCharts(ctx)
	return map[string]any{
		"reconciler": opSummary.reconcilerMap(),
		"tools": map[string]any{
			"catalogCount": toolCount,
			"installedCount": func() any {
				if restricted {
					return nil
				}
				return installedCount
			}(),
		},
		"operations":         opSummary.Counts,
		"recentFailureCount": opSummary.RecentFailures,
		"recentOperations":   opSummary.Recent,
		"latestFailure":      opSummary.LatestFailure,
	}, nil
}
