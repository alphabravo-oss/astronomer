package handler

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// ControllerStatus summarizes security policy and scan state.
func (h *SecurityHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load security templates")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *SecurityHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	templates, err := h.queries.ListPodSecurityTemplates(ctx, sqlc.ListPodSecurityTemplatesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	policies, err := h.queries.ListClusterSecurityPolicies(ctx, sqlc.ListClusterSecurityPoliciesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	scans, err := h.queries.ListSecurityScanResults(ctx, sqlc.ListSecurityScanResultsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	policyStatuses := map[string]int{}
	scanStatuses := map[string]int{}
	failedPolicies := 0
	runningScans := 0
	failedScans := 0
	for _, policy := range policies {
		policyStatuses[policy.SyncStatus]++
		if policy.SyncStatus == "failed" || policy.ErrorMessage != "" {
			failedPolicies++
		}
	}
	for _, scan := range scans {
		scanStatuses[scan.Status]++
		switch scan.Status {
		case "pending", "running", "in_progress":
			runningScans++
		case "failed", "error":
			failedScans++
		}
	}
	health := "healthy"
	reasons := make([]string, 0, 2)
	if failedPolicies > 0 {
		health = "degraded"
		reasons = append(reasons, "failed_policy_syncs_present")
	}
	if failedScans > 0 {
		health = "degraded"
		reasons = append(reasons, "failed_scans_present")
	}
	return map[string]any{
		"reconciler": map[string]any{
			"enabled": false,
		},
		"health":        health,
		"healthReasons": reasons,
		"templates": map[string]any{
			"total": len(templates),
		},
		"policies": map[string]any{
			"total":       len(policies),
			"failedCount": failedPolicies,
			"statuses":    policyStatuses,
		},
		"scans": map[string]any{
			"total":        len(scans),
			"runningCount": runningScans,
			"failedCount":  failedScans,
			"statuses":     scanStatuses,
		},
	}, nil
}
