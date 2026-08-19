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
	sharedGrafanaAuthModeProxy     = "proxy"
	grafanaProxySecretName         = "astronomer-grafana-proxy-key"
	grafanaProxyListenPort         = 8080
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
	if strings.ContainsAny(req.IngressHost, "\n\r\t /") {
		return SharedGrafanaRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("ingressHost must be a hostname")
	}
	if req.IngressHost == "" {
		req.IngressHost = defaultGrafanaHost(h.serverURL)
	}
	if req.IngressHost == "" {
		return SharedGrafanaRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("ingressHost is required (set ServerURL so grafana.<host> can be derived; never values.ingress.host)")
	}
	if h == nil || strings.TrimSpace(h.proxyImage) == "" {
		return SharedGrafanaRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("grafana-proxy image is not configured (ASTRONOMER_SERVER_IMAGE)")
	}

	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return SharedGrafanaRequest{}, nil, sqlc.MonitoringBackend{}, fmt.Errorf("default monitoring backend is not configured")
	}
	return req, h.sharedGrafanaHelmValues(req, backend), backend, nil
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
	if req.IngressHost == "" && h != nil {
		req.IngressHost = defaultGrafanaHost(h.serverURL)
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
		"authMode":              sharedGrafanaAuthModeProxy,
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

func (h *MonitoringHandler) sharedGrafanaHelmValues(req SharedGrafanaRequest, backend sqlc.MonitoringBackend) map[string]any {
	persistence := map[string]any{"enabled": false}
	if req.StorageSize != "" {
		persistence = map[string]any{"enabled": true, "size": req.StorageSize}
		if req.StorageClass != "" {
			persistence["storageClassName"] = req.StorageClass
		}
	}
	image := ""
	serverURL := ""
	var expose GrafanaExpose
	if h != nil {
		image = h.proxyImage
		serverURL = h.serverURL
		expose = h.grafanaExpose
	}
	extra := grafanaFamilyExtraObjects(req, backend, image, serverURL, expose)
	grafanaHost := stripHostScheme(req.IngressHost)
	rootURL := ""
	if grafanaHost != "" {
		rootURL = "https://" + grafanaHost + "/"
	}
	csrfOrigins := grafanaHost
	if astro := hostnameOf(serverURL); astro != "" && astro != grafanaHost {
		csrfOrigins = strings.TrimSpace(csrfOrigins + " " + astro)
	}
	return map[string]any{
		"replicas": req.Replicas,
		"service": map[string]any{
			"enabled": true,
			"type":    "ClusterIP",
			"port":    80,
		},
		"fullnameOverride": req.ReleaseName,
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
			"server": map[string]any{
				"root_url":            rootURL,
				"serve_from_sub_path": false,
			},
			"dataproxy": map[string]any{
				"send_user_header": true,
				"timeout":          "60",
			},
			"auth": map[string]any{
				"disable_login_form":   true,
				"disable_signout_menu": true,
			},
			"auth.anonymous": map[string]any{"enabled": false},
			"auth.basic":     map[string]any{"enabled": false},
			"auth.proxy": map[string]any{
				"enabled":            true,
				"header_name":        "X-WEBAUTH-USER",
				"header_property":    "email",
				"auto_sign_up":       true,
				"enable_login_token": false,
				"headers":            "Email:X-WEBAUTH-USER Name:X-WEBAUTH-USER Role:X-WEBAUTH-ROLE",
			},
			"users": map[string]any{
				"allow_sign_up":        false,
				"auto_assign_org":      true,
				"auto_assign_org_role": "Viewer",
			},
			"live": map[string]any{"enabled": false},
			"security": map[string]any{
				"csrf_trusted_origins": csrfOrigins,
			},
		},
		"extraObjects": extra,
	}
}

func grafanaFamilyExtraObjects(req SharedGrafanaRequest, backend sqlc.MonitoringBackend, proxyImage, serverURL string, expose GrafanaExpose) []any {
	objects := grafanaOwnedConfigMaps(req, backend)
	objects = append(objects, grafanaProxyExtraObjects(req, proxyImage, serverURL, expose)...)
	return objects
}

func grafanaProxyServiceName(release string) string {
	return defaultString(release, sharedGrafanaDefaultRelease) + "-grafana-proxy"
}

func grafanaServiceName(release string) string {
	return defaultString(release, sharedGrafanaDefaultRelease)
}

func grafanaProxyExtraObjects(req SharedGrafanaRequest, proxyImage, serverURL string, expose GrafanaExpose) []any {
	ns := defaultString(req.Namespace, "monitoring")
	release := defaultString(req.ReleaseName, sharedGrafanaDefaultRelease)
	host := stripHostScheme(req.IngressHost)
	upstream := fmt.Sprintf("http://%s.%s.svc.cluster.local:80", grafanaServiceName(release), ns)
	astroURL := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	svcName := grafanaProxyServiceName(release)
	labels := map[string]any{
		"app.kubernetes.io/name":      "grafana-proxy",
		"app.kubernetes.io/instance":  release,
		"app.kubernetes.io/component": "grafana-proxy",
	}
	objects := []any{
		grafanaProxyKeySecret(ns),
		grafanaProxyDeployment(ns, release, svcName, labels, proxyImage, upstream, astroURL, host),
		grafanaProxyService(ns, svcName, labels),
		grafanaLockdownNetworkPolicy(ns, release, labels),
		grafanaProxyListenNetworkPolicy(ns, svcName, labels),
	}
	if host == "" {
		return objects
	}
	useGateway := expose.GatewayClass != "" && expose.PlatformNamespace != "" && expose.GatewayName != ""
	if useGateway {
		objects = append(objects,
			grafanaProxyPlatformHTTPRoute(expose.PlatformNamespace, expose.GatewayName, ns, svcName, host),
			grafanaProxyReferenceGrant(ns, svcName, expose.PlatformNamespace),
		)
		return objects
	}
	objects = append(objects, grafanaProxyIngress(ns, svcName, host, expose.IngressClass))
	return objects
}

func grafanaProxyKeySecret(namespace string) map[string]any {
	// Helm lookup keeps the HMAC key stable across upgrades and out of preview values.
	lookup := `{{ $s := lookup "v1" "Secret" .Release.Namespace "` + grafanaProxySecretName + `" }}{{ if and $s $s.data }}{{ index $s.data "key" | b64dec }}{{ else }}{{ randAlphaNum 32 }}{{ end }}`
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      grafanaProxySecretName,
			"namespace": namespace,
			"annotations": map[string]any{
				"helm.sh/resource-policy": "keep",
			},
		},
		"type": "Opaque",
		"stringData": map[string]any{
			"key": lookup,
		},
	}
}

func grafanaProxyDeployment(namespace, release, svcName string, labels map[string]any, image, upstream, astroURL, grafanaHost string) map[string]any {
	env := []any{
		map[string]any{"name": "LISTEN_ADDR", "value": fmt.Sprintf(":%d", grafanaProxyListenPort)},
		map[string]any{"name": "GRAFANA_UPSTREAM", "value": upstream},
		map[string]any{"name": "ASTRONOMER_URL", "value": astroURL},
		map[string]any{"name": "GRAFANA_HOST", "value": grafanaHost},
		map[string]any{
			"name": "GRAFANA_PROXY_KEY",
			"valueFrom": map[string]any{
				"secretKeyRef": map[string]any{
					"name": grafanaProxySecretName,
					"key":  "key",
				},
			},
		},
	}
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      svcName,
			"namespace": namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			"replicas": 1,
			"selector": map[string]any{"matchLabels": labels},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"automountServiceAccountToken": false,
					"securityContext": map[string]any{
						"runAsNonRoot": true,
						"runAsUser":    65534,
						"runAsGroup":   65534,
						"seccompProfile": map[string]any{
							"type": "RuntimeDefault",
						},
					},
					"containers": []any{
						map[string]any{
							"name":  "grafana-proxy",
							"image": image,
							"args":  []any{"grafana-proxy"},
							"ports": []any{
								map[string]any{"name": "http", "containerPort": grafanaProxyListenPort},
							},
							"env": env,
							"resources": map[string]any{
								"requests": map[string]any{"cpu": "50m", "memory": "64Mi"},
								"limits":   map[string]any{"cpu": "200m", "memory": "128Mi"},
							},
							"securityContext": map[string]any{
								"allowPrivilegeEscalation": false,
								"readOnlyRootFilesystem":   true,
								"runAsNonRoot":             true,
								"capabilities":             map[string]any{"drop": []any{"ALL"}},
							},
						},
					},
				},
			},
		},
	}
}

func grafanaProxyService(namespace, name string, labels map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			"type":     "ClusterIP",
			"selector": labels,
			"ports": []any{
				map[string]any{"name": "http", "port": grafanaProxyListenPort, "targetPort": grafanaProxyListenPort},
			},
		},
	}
}

func grafanaLockdownNetworkPolicy(namespace, release string, proxyLabels map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      release + "-grafana-lockdown",
			"namespace": namespace,
		},
		"spec": map[string]any{
			"podSelector": map[string]any{
				"matchLabels": map[string]any{
					"app.kubernetes.io/name":     "grafana",
					"app.kubernetes.io/instance": release,
				},
			},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{
				map[string]any{
					"from": []any{
						map[string]any{"podSelector": map[string]any{"matchLabels": proxyLabels}},
					},
				},
			},
		},
	}
}

func grafanaProxyListenNetworkPolicy(namespace, name string, labels map[string]any) map[string]any {
	// Ingress/Gateway controllers typically live outside this namespace and
	// have no portable labels (same as chart allowPublicIngress).
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      name + "-ingress",
			"namespace": namespace,
		},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": labels},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{
				map[string]any{
					"ports": []any{
						map[string]any{"protocol": "TCP", "port": grafanaProxyListenPort},
					},
				},
			},
		},
	}
}

func grafanaProxyIngress(namespace, svcName, host, ingressClass string) map[string]any {
	spec := map[string]any{
		"tls": []any{
			map[string]any{
				"hosts":      []any{host},
				"secretName": "astronomer-grafana-tls",
			},
		},
		"rules": []any{
			map[string]any{
				"host": host,
				"http": map[string]any{
					"paths": []any{
						map[string]any{
							"path":     "/",
							"pathType": "Prefix",
							"backend": map[string]any{
								"service": map[string]any{
									"name": svcName,
									"port": map[string]any{"number": grafanaProxyListenPort},
								},
							},
						},
					},
				},
			},
		},
	}
	if ingressClass != "" {
		spec["ingressClassName"] = ingressClass
	}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      svcName,
			"namespace": namespace,
		},
		"spec": spec,
	}
}

func grafanaProxyPlatformHTTPRoute(platformNS, gatewayName, grafanaNS, svcName, host string) map[string]any {
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]any{
			"name":      svcName,
			"namespace": platformNS,
		},
		"spec": map[string]any{
			"parentRefs": []any{
				map[string]any{"name": gatewayName},
			},
			"hostnames": []any{host},
			"rules": []any{
				map[string]any{
					"backendRefs": []any{
						map[string]any{
							"name":      svcName,
							"namespace": grafanaNS,
							"port":      grafanaProxyListenPort,
						},
					},
				},
			},
		},
	}
}

func grafanaProxyReferenceGrant(grafanaNS, svcName, platformNS string) map[string]any {
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1beta1",
		"kind":       "ReferenceGrant",
		"metadata": map[string]any{
			"name":      svcName + "-from-gateway",
			"namespace": grafanaNS,
		},
		"spec": map[string]any{
			"from": []any{
				map[string]any{
					"group":     "gateway.networking.k8s.io",
					"kind":      "HTTPRoute",
					"namespace": platformNS,
				},
			},
			"to": []any{
				map[string]any{
					"group": "",
					"kind":  "Service",
					"name":  svcName,
				},
			},
		},
	}
}

func grafanaOwnedConfigMaps(req SharedGrafanaRequest, backend sqlc.MonitoringBackend) []any {
	ns := defaultString(req.Namespace, "monitoring")
	release := defaultString(req.ReleaseName, sharedGrafanaDefaultRelease)
	objects := grafanaDashboardConfigMaps()
	if url, ok := thanosDatasourceURL(backend); ok {
		objects = append(objects, grafanaDatasourceConfigMap(
			grafanaThanosDatasourceConfigMapName(release),
			ns,
			release,
			grafanaThanosDatasourceYAML(url),
		))
	}
	if byo := strings.TrimSpace(req.LogDatasourceURL); byo != "" {
		objects = append(objects, grafanaDatasourceConfigMap(
			release+"-loki-byo-datasource",
			ns,
			release,
			grafanaBYOLokiDatasourceYAML(byo),
		))
	}
	return objects
}

func grafanaThanosDatasourceConfigMapName(release string) string {
	return defaultString(release, sharedGrafanaDefaultRelease) + "-thanos-datasource"
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

func grafanaDatasourceConfigMap(name, namespace, release, yamlBody string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]any{
				"grafana_datasource":           "1",
				"app.kubernetes.io/managed-by": "Helm",
			},
			"annotations": map[string]any{
				"meta.helm.sh/release-name":      release,
				"meta.helm.sh/release-namespace": namespace,
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
	cm := grafanaDatasourceConfigMap(
		grafanaThanosDatasourceConfigMapName(release),
		ns,
		release,
		grafanaThanosDatasourceYAML(url),
	)
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

func (h *MonitoringHandler) deleteGrafanaThanosDatasourceConfigMap(ctx context.Context, req SharedGrafanaRequest) {
	if h.requester == nil {
		return
	}
	ns := defaultString(req.Namespace, "monitoring")
	release := defaultString(req.ReleaseName, sharedGrafanaDefaultRelease)
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", ns, grafanaThanosDatasourceConfigMapName(release))
	resp, err := h.requester.Do(ctx, req.ManagementClusterID, http.MethodDelete, path, nil, requestHeaders(""))
	if err != nil || resp == nil {
		return
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNotFound:
		return
	}
}
