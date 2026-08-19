package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/deploy/dashboards"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
)

const (
	sharedGrafanaChartRepo         = "https://grafana.github.io/helm-charts"
	sharedGrafanaChartName         = "grafana"
	sharedGrafanaDefaultRelease    = "astronomer-grafana"
	sharedGrafanaDefaultChart      = "8.12.1"
	sharedGrafanaAuthModeClusterIP = "clusterip"
)

func (h *MonitoringHandler) sharedGrafanaPayload(ctx context.Context, r *http.Request) (SharedGrafanaRequest, map[string]any, sqlc.MonitoringBackend, error) {
	if h.queries == nil {
		return SharedGrafanaRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("monitoring store not configured")
	}
	if h.helm == nil {
		return SharedGrafanaRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("helm requester not configured")
	}

	var req SharedGrafanaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return SharedGrafanaRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("invalid JSON body")
	}
	if req.ManagementClusterID == "" {
		req.ManagementClusterID = r.URL.Query().Get("clusterId")
	}
	if req.ManagementClusterID == "" {
		return SharedGrafanaRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("managementClusterId is required")
	}
	if req.Namespace == "" {
		req.Namespace = "monitoring"
	}
	if req.ReleaseName == "" {
		req.ReleaseName = sharedGrafanaDefaultRelease
	}
	if req.ChartVersion == "" {
		req.ChartVersion = sharedGrafanaDefaultChart
	}
	if req.Replicas <= 0 {
		req.Replicas = 1
	}
	if strings.ContainsAny(req.LogDatasourceURL, "\n\r\t") {
		return SharedGrafanaRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("logDatasourceUrl must be a single-line URL")
	}

	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return SharedGrafanaRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("default monitoring backend is not configured")
	}
	return req, sharedGrafanaHelmValues(req, backend), backend, nil
}

func (h *MonitoringHandler) sharedGrafanaPrecheck(ctx context.Context, req SharedGrafanaRequest, op string) (int, string, string, bool) {
	if op != "install" && op != "replace" {
		return 0, "", "", true
	}
	snap := h.collectSizerSnapshotFor(ctx, req.ManagementClusterID, req.StorageClass, "")
	eval := evaluateSizer(snap.input)
	if eval.Verdicts.Grafana.Result != "fail" {
		return 0, "", "", true
	}
	msg := strings.Join(eval.Verdicts.Grafana.Reasons, ", ")
	if msg == "" {
		msg = "grafana sizer verdict failed"
	}
	return http.StatusPreconditionFailed, apierror.SizerFailed, msg, false
}

func (h *MonitoringHandler) updateSharedGrafanaMetadata(ctx context.Context, backend sqlc.MonitoringBackend, req SharedGrafanaRequest, status string) error {
	if h.queries == nil {
		return nil
	}
	resolvedRollback := h.resolveAutoRollbackPolicy(backend, req.AutoRollbackOnFailure)
	appliedSpecHash := specHash(map[string]any{
		"managementClusterId":   req.ManagementClusterID,
		"namespace":             defaultString(req.Namespace, "monitoring"),
		"releaseName":           defaultString(req.ReleaseName, sharedGrafanaDefaultRelease),
		"chartVersion":          req.ChartVersion,
		"replicas":              req.Replicas,
		"storageClass":          req.StorageClass,
		"storageSize":           req.StorageSize,
		"ingressHost":           req.IngressHost,
		"logDatasourceUrl":      req.LogDatasourceURL,
		"autoRollbackOnFailure": resolvedRollback,
	})
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return fmt.Errorf("resolve monitoring backend auth_config: %w", err)
	}
	_, thanosOK := thanosDatasourceURL(backend)
	authCfg["sharedGrafana"] = map[string]any{
		"managementClusterId":   req.ManagementClusterID,
		"namespace":             defaultString(req.Namespace, "monitoring"),
		"releaseName":           defaultString(req.ReleaseName, sharedGrafanaDefaultRelease),
		"status":                status,
		"chartVersion":          req.ChartVersion,
		"replicas":              req.Replicas,
		"storageClass":          req.StorageClass,
		"storageSize":           req.StorageSize,
		"ingressHost":           req.IngressHost,
		"logDatasourceUrl":      req.LogDatasourceURL,
		"grafanaHost":           req.IngressHost,
		"authMode":              sharedGrafanaAuthModeClusterIP,
		"autoRollbackOnFailure": resolvedRollback,
		"thanosDatasource":      thanosOK,
		"lastAppliedSpecHash":   appliedSpecHash,
		"updatedAt":             time.Now().UTC().Format(time.RFC3339),
	}
	params := sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           backend.QueryUrl,
		AlertmanagerUrl:    backend.AlertmanagerUrl,
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
		CreatedByID:        backend.CreatedByID,
	}
	if err := imonitoring.SealInto(&params, authCfg, h.monitoringSealer()); err != nil {
		return err
	}
	_, err = h.queries.UpsertDefaultMonitoringBackend(ctx, params)
	return err
}

func sharedGrafanaReplaceRequired(metadata map[string]any, req SharedGrafanaRequest) (bool, []string) {
	if len(metadata) == 0 || stringFromMap(metadata, "status") == "not_configured" || stringFromMap(metadata, "status") == "uninstalled" {
		return false, nil
	}
	reasons := []string{}
	if current := stringFromMap(metadata, "namespace"); current != "" && current != req.Namespace {
		reasons = append(reasons, "namespace change")
	}
	if current := stringFromMap(metadata, "releaseName"); current != "" && current != req.ReleaseName {
		reasons = append(reasons, "release name change")
	}
	if current := stringFromMap(metadata, "storageClass"); current != req.StorageClass {
		reasons = append(reasons, "storage class change")
	}
	if current := stringFromMap(metadata, "storageSize"); current != req.StorageSize {
		reasons = append(reasons, "storage size change")
	}
	return len(reasons) > 0, reasons
}

func sharedGrafanaProjectedStatus(metadata map[string]any, backend sqlc.MonitoringBackend) string {
	status := defaultString(stringFromMap(metadata, "status"), "not_configured")
	switch status {
	case "not_configured", "uninstalled", "installing", "updating":
		return status
	}
	if _, ok := thanosDatasourceURL(backend); !ok {
		return "degraded"
	}
	return status
}

func sharedGrafanaHelmValues(req SharedGrafanaRequest, backend sqlc.MonitoringBackend) map[string]any {
	persistence := map[string]any{"enabled": false}
	if req.StorageSize != "" {
		persistence = map[string]any{"enabled": true, "size": req.StorageSize}
		if req.StorageClass != "" {
			persistence["storageClassName"] = req.StorageClass
		}
	}
	extra := grafanaOwnedConfigMaps(req, backend)
	return map[string]any{
		"replicas": req.Replicas,
		"service": map[string]any{
			"enabled": true,
			"type":    "ClusterIP",
			"port":    80,
		},
		"ingress": map[string]any{
			"enabled": false,
		},
		"persistence": persistence,
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "250m", "memory": "256Mi"},
			"limits":   map[string]any{"cpu": "500m", "memory": "512Mi"},
		},
		"sidecar": map[string]any{
			"dashboards": map[string]any{
				"enabled":    true,
				"label":      "grafana_dashboard",
				"labelValue": "1",
			},
			"datasources": map[string]any{
				"enabled":    true,
				"label":      "grafana_datasource",
				"labelValue": "1",
			},
		},
		"grafana.ini": map[string]any{
			"dataproxy": map[string]any{
				"send_user_header": true,
				"timeout":          "60",
			},
			"auth.anonymous": map[string]any{"enabled": false},
			"users":          map[string]any{"allow_sign_up": false},
			"live":           map[string]any{"enabled": false},
		},
		"extraObjects": extra,
	}
}

func grafanaOwnedConfigMaps(req SharedGrafanaRequest, backend sqlc.MonitoringBackend) []any {
	objects := grafanaDashboardConfigMaps()
	if url, ok := thanosDatasourceURL(backend); ok {
		objects = append(objects, grafanaDatasourceConfigMap(
			defaultString(req.ReleaseName, sharedGrafanaDefaultRelease)+"-thanos-datasource",
			grafanaThanosDatasourceYAML(url),
		))
	}
	if byo := strings.TrimSpace(req.LogDatasourceURL); byo != "" {
		objects = append(objects, grafanaDatasourceConfigMap(
			defaultString(req.ReleaseName, sharedGrafanaDefaultRelease)+"-loki-byo-datasource",
			grafanaBYOLokiDatasourceYAML(byo),
		))
	}
	return objects
}

func grafanaDashboardConfigMaps() []any {
	entries, err := fs.ReadDir(dashboards.FS, ".")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	objects := make([]any, 0, len(names))
	for _, name := range names {
		raw, err := dashboards.FS.ReadFile(name)
		if err != nil {
			continue
		}
		slug := strings.TrimSuffix(name, ".json")
		objects = append(objects, map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name": grafanaDashboardConfigMapName(slug),
				"labels": map[string]any{
					"grafana_dashboard": "1",
				},
			},
			"data": map[string]any{
				name: helmTplEscape(string(raw)),
			},
		})
	}
	return objects
}

func grafanaDashboardConfigMapName(slug string) string {
	name := "astronomer-grafana-dashboard-" + slug
	if len(name) > 63 {
		name = name[:63]
		name = strings.TrimRight(name, "-")
	}
	return name
}

func grafanaDatasourceConfigMap(name, yamlBody string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name": name,
			"labels": map[string]any{
				"grafana_datasource": "1",
			},
		},
		"data": map[string]any{
			"datasource.yaml": yamlBody,
		},
	}
}

func grafanaThanosDatasourceYAML(url string) string {
	return strings.TrimSpace(fmt.Sprintf(`
apiVersion: 1
datasources:
  - name: Thanos
    uid: thanos
    type: prometheus
    access: proxy
    url: %s
    isDefault: true
    jsonData:
      timeInterval: 30s
      timeout: 60
      prometheusType: Thanos
`, yamlQuotedString(url))) + "\n"
}

func grafanaBYOLokiDatasourceYAML(url string) string {
	return strings.TrimSpace(fmt.Sprintf(`
apiVersion: 1
datasources:
  - name: Loki
    uid: loki
    type: loki
    access: proxy
    url: %s
    editable: false
    jsonData:
      timeout: 60
`, yamlQuotedString(url))) + "\n"
}

func yamlQuotedString(s string) string {
	raw, err := yaml.Marshal(s)
	if err != nil {
		return `""`
	}
	return strings.TrimSpace(string(raw))
}

func thanosDatasourceURL(backend sqlc.MonitoringBackend) (string, bool) {
	meta := sharedThanosMetadata(backend)
	status := stringFromMap(meta, "status")
	if status == "" || status == "not_configured" || status == "uninstalled" {
		return "", false
	}
	release := defaultString(stringFromMap(meta, "releaseName"), "thanos")
	ns := defaultString(stringFromMap(meta, "namespace"), "monitoring")
	return fmt.Sprintf("http://%s-query-frontend.%s.svc.cluster.local:9090", release, ns), true
}

// helmTplEscape keeps Grafana legend formats like {{status_class}} from being
// interpolated by the chart's extraObjects tpl.
func helmTplEscape(s string) string {
	return strings.ReplaceAll(s, "{{", `{{"{{"}}`)
}

func grafanaStackPresent(status string) bool {
	switch status {
	case "", "not_configured", "uninstalled":
		return false
	default:
		return true
	}
}

func grafanaRequestFromMetadata(meta map[string]any) SharedGrafanaRequest {
	req := SharedGrafanaRequest{
		ManagementClusterID: stringFromMap(meta, "managementClusterId"),
		Namespace:           stringFromMap(meta, "namespace"),
		ReleaseName:         stringFromMap(meta, "releaseName"),
		ChartVersion:        stringFromMap(meta, "chartVersion"),
		StorageClass:        stringFromMap(meta, "storageClass"),
		StorageSize:         stringFromMap(meta, "storageSize"),
		IngressHost:         stringFromMap(meta, "ingressHost"),
		LogDatasourceURL:    stringFromMap(meta, "logDatasourceUrl"),
	}
	switch n := meta["replicas"].(type) {
	case float64:
		req.Replicas = int32(n)
	case int:
		req.Replicas = int32(n)
	case int32:
		req.Replicas = n
	case json.Number:
		if v, err := n.Int64(); err == nil {
			req.Replicas = int32(v)
		}
	}
	if _, ok := meta["autoRollbackOnFailure"]; ok {
		v := boolFromAny(meta["autoRollbackOnFailure"])
		req.AutoRollbackOnFailure = &v
	}
	return req
}

func (h *MonitoringHandler) stampSharedGrafanaHealth(ctx context.Context, req SharedGrafanaRequest) error {
	if h.queries == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return err
	}
	status := "healthy"
	if _, ok := thanosDatasourceURL(backend); !ok {
		status = "degraded"
	}
	return h.updateSharedGrafanaMetadata(ctx, backend, req, status)
}

func (h *MonitoringHandler) syncGrafanaThanosDatasource(ctx context.Context) error {
	if h.queries == nil || h.requester == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return nil
	}
	meta := sharedStackMetadata(backend, "sharedGrafana")
	if !grafanaStackPresent(stringFromMap(meta, "status")) {
		return nil
	}
	url, ok := thanosDatasourceURL(backend)
	if !ok {
		return nil
	}
	req := grafanaRequestFromMetadata(meta)
	ns := defaultString(req.Namespace, "monitoring")
	release := defaultString(req.ReleaseName, sharedGrafanaDefaultRelease)
	cm := grafanaDatasourceConfigMap(release+"-thanos-datasource", grafanaThanosDatasourceYAML(url))
	if err := h.ensureGrafanaConfigMap(ctx, req.ManagementClusterID, ns, cm); err != nil {
		return err
	}
	status := stringFromMap(meta, "status")
	if status == "degraded" || status == "healthy" || status == "reinstalled" || status == "configured" || status == "drifted" {
		if err := h.updateSharedGrafanaMetadata(ctx, backend, req, "healthy"); err != nil {
			return err
		}
	}
	return nil
}

func (h *MonitoringHandler) ensureGrafanaConfigMap(ctx context.Context, clusterID, namespace string, obj map[string]any) error {
	if h.requester == nil {
		return nil
	}
	if clusterID == "" || namespace == "" {
		return fmt.Errorf("grafana configmap target is incomplete")
	}
	meta, _ := obj["metadata"].(map[string]any)
	if meta == nil {
		return fmt.Errorf("grafana configmap missing metadata")
	}
	name, _ := meta["name"].(string)
	if name == "" {
		return fmt.Errorf("grafana configmap missing name")
	}
	meta["namespace"] = namespace
	body, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	patchPath := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", namespace, name)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodPatch, patchPath, body, requestHeaders("application/merge-patch+json"))
	if err == nil && resp != nil && resp.StatusCode != http.StatusNotFound {
		return ensureSuccess(resp)
	}
	createPath := fmt.Sprintf("/api/v1/namespaces/%s/configmaps", namespace)
	resp, err = h.requester.Do(ctx, clusterID, http.MethodPost, createPath, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return ensureSuccess(resp)
}
