package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
)

func (h *MonitoringHandler) resolveAutoRollbackPolicy(backend sqlc.MonitoringBackend, override *bool) bool {
	if override != nil {
		return *override
	}
	policies := mapFromMapValue(decodeJSONMap(backend.AuthConfig)["operationPolicies"])
	if value, ok := policies["defaultAutoRollbackOnFailure"].(bool); ok {
		return value
	}
	return false
}

func (h *MonitoringHandler) resolveMaxRetryAttempts(backend sqlc.MonitoringBackend) int32 {
	policies := mapFromMapValue(decodeJSONMap(backend.AuthConfig)["operationPolicies"])
	switch value := policies["maxRetryAttempts"].(type) {
	case float64:
		if value >= 1 {
			return int32(value)
		}
	case int32:
		if value >= 1 {
			return value
		}
	case int:
		if value >= 1 {
			return int32(value)
		}
	}
	return 1
}

func (h *MonitoringHandler) operationMaxAttempts(payload json.RawMessage) int32 {
	var env monitoringOperationEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return 1
	}
	if env.ResolvedMaxRetryAttempts < 1 {
		return 1
	}
	return env.ResolvedMaxRetryAttempts
}

func (h *MonitoringHandler) currentReleaseRevision(ctx context.Context, clusterID, releaseName, namespace string) int {
	if h == nil || h.helm == nil || clusterID == "" || releaseName == "" || namespace == "" {
		return 0
	}
	result, err := h.helm.Status(ctx, clusterID, releaseName, namespace)
	if err != nil {
		return 0
	}
	return result.Revision
}

func (h *MonitoringHandler) rollbackIfConfigured(ctx context.Context, operationID uuid.UUID, originalErr error, enabled bool, clusterID, releaseName, namespace string, previousRevision int) error {
	if !enabled {
		return originalErr
	}
	if previousRevision <= 0 {
		return fmt.Errorf("%w; rollback requested but no previous revision was available", originalErr)
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "warn", "rollback", "upgrade failed readiness, attempting rollback", map[string]any{
		"clusterId":        clusterID,
		"releaseName":      releaseName,
		"namespace":        namespace,
		"previousRevision": previousRevision,
		"error":            originalErr.Error(),
	})
	_, rollbackErr := h.helm.Do(ctx, clusterID, protocol.MsgHelmRollback, protocol.HelmRequestPayload{
		ReleaseName: releaseName,
		Namespace:   namespace,
		Revision:    previousRevision,
		Timeout:     900,
	})
	if rollbackErr != nil {
		h.recordMonitoringOperationEvent(ctx, operationID, "error", "rollback", "rollback failed", map[string]any{
			"clusterId":        clusterID,
			"releaseName":      releaseName,
			"namespace":        namespace,
			"previousRevision": previousRevision,
			"error":            rollbackErr.Error(),
		})
		return fmt.Errorf("%w; rollback to revision %d failed: %v", originalErr, previousRevision, rollbackErr)
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "rollback", "rollback completed", map[string]any{
		"clusterId":        clusterID,
		"releaseName":      releaseName,
		"namespace":        namespace,
		"previousRevision": previousRevision,
	})
	if err := h.waitForReleaseReadiness(ctx, operationID, clusterID, namespace, releaseName, 1, 2*time.Minute); err != nil {
		return fmt.Errorf("%w; rollback succeeded but readiness after rollback failed: %v", originalErr, err)
	}
	return fmt.Errorf("%w; rollback to revision %d completed", originalErr, previousRevision)
}

func (h *MonitoringHandler) recordMonitoringOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateMonitoringOperationEvent(ctx, sqlc.CreateMonitoringOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}

func (h *MonitoringHandler) waitForReleaseReadiness(ctx context.Context, operationID uuid.UUID, clusterID, namespace, releaseName string, minReadyPods int, timeout time.Duration) error {
	if h.requester == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	labelSelector := url.QueryEscape("app.kubernetes.io/instance=" + releaseName)
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "readiness", "waiting for release readiness", map[string]any{
		"clusterId":    clusterID,
		"namespace":    namespace,
		"releaseName":  releaseName,
		"minReadyPods": minReadyPods,
		"timeout":      timeout.String(),
	})
	for {
		ready, total, err := h.countReadyReleasePods(ctx, clusterID, namespace, labelSelector)
		if err == nil && total >= minReadyPods && ready >= minReadyPods {
			h.recordMonitoringOperationEvent(ctx, operationID, "info", "readiness", "release became ready", map[string]any{
				"clusterId":   clusterID,
				"namespace":   namespace,
				"releaseName": releaseName,
				"readyPods":   ready,
				"totalPods":   total,
			})
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("release readiness check timed out: %w", err)
			}
			return fmt.Errorf("release readiness check timed out: %d/%d ready pods for %s", ready, total, releaseName)
		}
		if err != nil {
			h.recordMonitoringOperationEvent(ctx, operationID, "warn", "readiness", "release readiness poll failed", map[string]any{
				"error": err.Error(),
			})
		}
		time.Sleep(5 * time.Second)
	}
}

func (h *MonitoringHandler) countReadyReleasePods(ctx context.Context, clusterID, namespace, labelSelector string) (int, int, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s", namespace, labelSelector)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return 0, 0, err
	}
	if err := ensureSuccess(resp); err != nil {
		return 0, 0, err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return 0, 0, err
	}
	items := objectItems(payload)
	ready := 0
	for _, item := range items {
		if podReady(item) {
			ready++
		}
	}
	return ready, len(items), nil
}

func podReady(item map[string]any) bool {
	status, _ := item["status"].(map[string]any)
	phase, _ := status["phase"].(string)
	if phase != "Running" {
		return false
	}
	conditions, _ := status["conditions"].([]any)
	for _, cond := range conditions {
		entry, _ := cond.(map[string]any)
		if entry == nil {
			continue
		}
		if entry["type"] == "Ready" && entry["status"] == "True" {
			return true
		}
	}
	return false
}

func (h *MonitoringHandler) verifySharedThanosReadiness(ctx context.Context, operationID uuid.UUID, req SharedThanosStackRequest) error {
	serviceName := defaultString(req.ReleaseName, "thanos") + "-query-frontend"
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking shared Thanos query frontend health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	if err := h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "9090", "/-/healthy", "", 90*time.Second); err != nil {
		return err
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "smoke", "running shared Thanos PromQL smoke query", map[string]any{
		"service": serviceName,
	})
	if err := h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "9090", "/api/v1/query", "query=vector(1)", 90*time.Second); err != nil {
		return err
	}
	if err := h.syncGrafanaThanosDatasource(ctx); err != nil && h.log != nil {
		h.log.Warn("sync grafana thanos datasource after Thanos readiness", "error", err)
	}
	return nil
}

func (h *MonitoringHandler) verifySharedAlertmanagerReadiness(ctx context.Context, operationID uuid.UUID, req SharedAlertmanagerRequest) error {
	serviceName := defaultString(req.ReleaseName, "astronomer-alertmanager")
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking shared Alertmanager health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	return h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "9093", "/-/healthy", "", 90*time.Second)
}

func (h *MonitoringHandler) verifySharedLokiReadiness(ctx context.Context, operationID uuid.UUID, req SharedLokiRequest) error {
	serviceName := defaultString(req.ReleaseName, sharedLokiDefaultRelease) + "-gateway"
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking shared Loki gateway health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	if err := h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "80", "/ready", "", 90*time.Second); err != nil {
		return err
	}
	return h.stampSharedLokiHealth(ctx, req)
}

func (h *MonitoringHandler) verifySharedGrafanaReadiness(ctx context.Context, operationID uuid.UUID, req SharedGrafanaRequest) error {
	serviceName := defaultString(req.ReleaseName, sharedGrafanaDefaultRelease)
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking shared Grafana health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	if err := h.waitForServiceProxySuccess(ctx, req.ManagementClusterID, req.Namespace, serviceName, "80", "/api/health", "", 90*time.Second); err != nil {
		return err
	}
	return h.stampSharedGrafanaHealth(ctx, req)
}

func (h *MonitoringHandler) verifyClusterMonitoringReadiness(ctx context.Context, operationID uuid.UUID, clusterID string, req MonitoringStackRequest) error {
	serviceName, err := h.findPrometheusServiceName(ctx, clusterID, req.Namespace, req.ReleaseName)
	if err != nil {
		return err
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "service", "checking cluster Prometheus service health", map[string]any{
		"service":   serviceName,
		"namespace": req.Namespace,
	})
	if err := h.waitForServiceProxySuccess(ctx, clusterID, req.Namespace, serviceName, "9090", "/-/healthy", "", 90*time.Second); err != nil {
		return err
	}
	h.recordMonitoringOperationEvent(ctx, operationID, "info", "smoke", "running cluster Prometheus PromQL smoke query", map[string]any{
		"service": serviceName,
	})
	return h.waitForServiceProxySuccess(ctx, clusterID, req.Namespace, serviceName, "9090", "/api/v1/query", "query=vector(1)", 90*time.Second)
}

func (h *MonitoringHandler) waitForServiceProxySuccess(ctx context.Context, clusterID, namespace, serviceName, port, path, rawQuery string, timeout time.Duration) error {
	if h.requester == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		lastErr = h.serviceProxyCheck(ctx, clusterID, namespace, serviceName, port, path, rawQuery)
		if lastErr == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service readiness check timed out for %s/%s: %w", namespace, serviceName, lastErr)
		}
		time.Sleep(5 * time.Second)
	}
}

func (h *MonitoringHandler) serviceProxyCheck(ctx context.Context, clusterID, namespace, serviceName, port, path, rawQuery string) error {
	target := serviceName + ":" + port
	proxyPath := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s/proxy%s", namespace, target, path)
	if rawQuery != "" {
		proxyPath += "?" + rawQuery
	}
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, proxyPath, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	return ensureSuccess(resp)
}

func (h *MonitoringHandler) findPrometheusServiceName(ctx context.Context, clusterID, namespace, releaseName string) (string, error) {
	if h.requester == nil {
		return "", fmt.Errorf("kubernetes requester not configured")
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/services?labelSelector=%s", namespace, url.QueryEscape("app.kubernetes.io/instance="+releaseName))
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return "", err
	}
	if err := ensureSuccess(resp); err != nil {
		return "", err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return "", err
	}
	for _, item := range objectItems(payload) {
		meta, _ := item["metadata"].(map[string]any)
		spec, _ := item["spec"].(map[string]any)
		name, _ := meta["name"].(string)
		if name == "" {
			continue
		}
		if strings.Contains(strings.ToLower(name), "prometheus") && serviceExposesPort(spec, 9090) {
			return name, nil
		}
	}
	return "", fmt.Errorf("prometheus service not found for release %s", releaseName)
}

func (h *MonitoringHandler) findGrafanaServiceName(ctx context.Context, clusterID, namespace, releaseName string) (string, error) {
	if h.requester == nil {
		return "", fmt.Errorf("kubernetes requester not configured")
	}
	if !isSafeK8sName(namespace) || !isSafeK8sName(releaseName) {
		return "", fmt.Errorf("invalid Grafana release target")
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/services?labelSelector=%s", namespace, url.QueryEscape("app.kubernetes.io/instance="+releaseName))
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return "", err
	}
	if err := ensureSuccess(resp); err != nil {
		return "", err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return "", err
	}
	for _, item := range objectItems(payload) {
		meta, _ := item["metadata"].(map[string]any)
		spec, _ := item["spec"].(map[string]any)
		name, _ := meta["name"].(string)
		if isSafeK8sName(name) && strings.Contains(strings.ToLower(name), "grafana") && serviceExposesPort(spec, 80) {
			return name, nil
		}
	}
	return "", fmt.Errorf("grafana service not found for release %s", releaseName)
}

// clusterGrafanaAvailable verifies Grafana and the service the browser-facing
// proxy actually targets in one discovery request. A plain Grafana Service is
// not sufficient: stacks created before the authenticated sidecar was
// introduced can still expose port 80, while every /observability/grafana
// request fails against the missing proxy.
func (h *MonitoringHandler) clusterGrafanaAvailable(ctx context.Context, clusterID, namespace, releaseName string) error {
	if h.requester == nil {
		return fmt.Errorf("kubernetes requester not configured")
	}
	service := grafanaProxyServiceName(releaseName)
	if !isSafeK8sName(namespace) || !isSafeK8sName(service) {
		return fmt.Errorf("invalid Grafana proxy target")
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/services?labelSelector=%s", namespace, url.QueryEscape("app.kubernetes.io/instance="+releaseName))
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return err
	}
	grafanaFound := false
	proxyFound := false
	for _, item := range objectItems(payload) {
		meta, _ := item["metadata"].(map[string]any)
		spec, _ := item["spec"].(map[string]any)
		name, _ := meta["name"].(string)
		if name == service && serviceExposesPort(spec, grafanaProxyListenPort) {
			proxyFound = true
		}
		if isSafeK8sName(name) && strings.Contains(strings.ToLower(name), "grafana") && serviceExposesPort(spec, 80) {
			grafanaFound = true
		}
	}
	if grafanaFound && proxyFound {
		return nil
	}
	return fmt.Errorf("Grafana proxy service %s does not expose port %d", service, grafanaProxyListenPort)
}

func serviceExposesPort(spec map[string]any, port int) bool {
	ports, _ := spec["ports"].([]any)
	for _, item := range ports {
		entry, _ := item.(map[string]any)
		if entry == nil {
			continue
		}
		if intValue(entry, "port") == port || intValue(entry, "targetPort") == port {
			return true
		}
	}
	return false
}
