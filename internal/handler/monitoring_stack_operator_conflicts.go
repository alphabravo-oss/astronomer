package handler

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	appsv1 "k8s.io/api/apps/v1"
)

// MonitoringOperatorConflict describes a Prometheus operator that can
// reconcile the managed stack's namespace but is not owned by that stack.
// Multiple operators selecting the same Prometheus CR continuously rewrite its
// generated workloads, so this is a hard lifecycle blocker rather than a
// warning that can be safely ignored.
type MonitoringOperatorConflict struct {
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	ReleaseName string   `json:"releaseName,omitempty"`
	Watched     []string `json:"watchedNamespaces,omitempty"`
	Excluded    []string `json:"excludedNamespaces,omitempty"`
	WatchesAll  bool     `json:"watchesAllNamespaces"`
}

func (h *MonitoringHandler) clusterMonitoringOperatorConflicts(ctx context.Context, clusterID, namespace, releaseName string) ([]MonitoringOperatorConflict, error) {
	if h == nil || h.requester == nil {
		return nil, fmt.Errorf("Kubernetes requester is not configured")
	}
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, "/apis/apps/v1/deployments", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		if resp == nil {
			return nil, fmt.Errorf("list deployments returned no response")
		}
		return nil, responseError(resp)
	}
	var deployments appsv1.DeploymentList
	if err := parseJSONResponse(resp, &deployments); err != nil {
		return nil, fmt.Errorf("decode deployments: %w", err)
	}

	conflicts := make([]MonitoringOperatorConflict, 0)
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		ok := hasPrometheusOperatorContainer(deployment)
		if !ok || managedMonitoringOperator(deployment, namespace, releaseName) {
			continue
		}
		watched, excluded, all := prometheusOperatorNamespaceScope(prometheusOperatorFlags(deployment))
		if !prometheusOperatorWatchesNamespace(watched, excluded, namespace) {
			continue
		}
		labels := deployment.GetLabels()
		ownerRelease := labels["app.kubernetes.io/instance"]
		if ownerRelease == "" {
			ownerRelease = labels["release"]
		}
		conflicts = append(conflicts, MonitoringOperatorConflict{
			Namespace:   deployment.Namespace,
			Name:        deployment.Name,
			ReleaseName: ownerRelease,
			Watched:     watched,
			Excluded:    excluded,
			WatchesAll:  all,
		})
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Namespace == conflicts[j].Namespace {
			return conflicts[i].Name < conflicts[j].Name
		}
		return conflicts[i].Namespace < conflicts[j].Namespace
	})
	return conflicts, nil
}

func hasPrometheusOperatorContainer(deployment *appsv1.Deployment) bool {
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if strings.Contains(strings.ToLower(container.Image), "prometheus-operator") {
			return true
		}
	}
	return false
}

func prometheusOperatorFlags(deployment *appsv1.Deployment) []string {
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if strings.Contains(strings.ToLower(container.Image), "prometheus-operator") {
			return append(append([]string{}, container.Command...), container.Args...)
		}
	}
	return nil
}

func managedMonitoringOperator(deployment *appsv1.Deployment, namespace, releaseName string) bool {
	if deployment.Namespace != namespace {
		return false
	}
	labels := deployment.GetLabels()
	return labels["app.kubernetes.io/instance"] == releaseName || labels["release"] == releaseName
}

func prometheusOperatorNamespaceScope(flags []string) (watched, excluded []string, watchesAll bool) {
	for i := 0; i < len(flags); i++ {
		name, value, matched := prometheusOperatorFlagValue(flags, i)
		if !matched {
			continue
		}
		if !strings.Contains(flags[i], "=") {
			i++
		}
		values := splitMonitoringNamespaces(value)
		switch name {
		case "--namespaces":
			watched = values
		case "--deny-namespaces":
			excluded = values
		}
	}
	sort.Strings(watched)
	sort.Strings(excluded)
	return watched, excluded, len(watched) == 0 && len(excluded) == 0
}

func prometheusOperatorFlagValue(flags []string, index int) (name, value string, matched bool) {
	for _, candidate := range []string{"--namespaces", "--deny-namespaces"} {
		if flags[index] == candidate && index+1 < len(flags) {
			return candidate, flags[index+1], true
		}
		if strings.HasPrefix(flags[index], candidate+"=") {
			return candidate, strings.TrimPrefix(flags[index], candidate+"="), true
		}
	}
	return "", "", false
}

func splitMonitoringNamespaces(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" && !monitoringContainsString(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func prometheusOperatorWatchesNamespace(watched, excluded []string, namespace string) bool {
	if monitoringContainsString(excluded, namespace) {
		return false
	}
	return len(watched) == 0 || monitoringContainsString(watched, namespace)
}

func monitoringContainsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (h *MonitoringHandler) monitoringOperatorConflictResponse(w http.ResponseWriter, r *http.Request, clusterID string, req MonitoringStackRequest) bool {
	conflicts, err := h.clusterMonitoringOperatorConflicts(r.Context(), clusterID, req.Namespace, req.ReleaseName)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError,
			"Could not verify Prometheus operator ownership: "+err.Error())
		return true
	}
	if len(conflicts) == 0 {
		return false
	}
	RespondJSON(w, http.StatusConflict, map[string]any{
		"error":             "monitoring_operator_conflict",
		"message":           "Another Prometheus operator watches the managed monitoring namespace. Remove it through its owning application, or scope it away from this namespace, before continuing.",
		"operatorConflicts": conflicts,
	})
	return true
}
