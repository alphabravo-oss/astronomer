package handler

import (
	"sigs.k8s.io/yaml"
	"strings"
)

// Cluster Grafana trusts only the ticket-verifying sidecar on loopback. Direct
// requests to its ClusterIP cannot assert an Astronomer identity.
func (h *MonitoringHandler) clusterGrafanaValues(req MonitoringStackRequest, clusterID string, enabled bool) map[string]any {
	values := map[string]any{"enabled": enabled}
	if !enabled {
		return values
	}
	publicPath := clusterGrafanaProxyPath(clusterID)
	service := grafanaProxyServiceName(req.ReleaseName)
	secret := grafanaProxyKeySecret(req.Namespace)
	secretName := service + "-key"
	secret["metadata"].(map[string]any)["name"] = secretName
	secret["stringData"].(map[string]any)["key"] = strings.ReplaceAll(secret["stringData"].(map[string]any)["key"].(string), grafanaProxySecretName, secretName)
	labels := map[string]any{"app.kubernetes.io/name": "grafana", "app.kubernetes.io/instance": req.ReleaseName}
	values["extraObjects"] = []any{secret, grafanaProxyService(req.Namespace, service, labels)}
	containers := []any{map[string]any{
		"name": "astronomer-grafana-proxy", "image": h.proxyImage, "args": []any{"grafana-proxy"},
		"ports": []any{map[string]any{"name": "auth-proxy", "containerPort": grafanaProxyListenPort}},
		"env": []any{
			map[string]any{"name": "LISTEN_ADDR", "value": ":8080"},
			map[string]any{"name": "GRAFANA_UPSTREAM", "value": "http://127.0.0.1:3000"},
			map[string]any{"name": "ASTRONOMER_URL", "value": strings.TrimRight(h.serverURL, "/")},
			map[string]any{"name": "GRAFANA_PUBLIC_PATH", "value": publicPath},
			map[string]any{"name": "GRAFANA_PROXY_KEY", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": secretName, "key": "key"}}},
		},
		"resources":       map[string]any{"requests": map[string]any{"cpu": "25m", "memory": "32Mi"}, "limits": map[string]any{"cpu": "200m", "memory": "128Mi"}},
		"securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "runAsNonRoot": true, "capabilities": map[string]any{"drop": []any{"ALL"}}},
	}}
	encoded, _ := yaml.Marshal(containers)
	values["extraContainers"] = string(encoded)
	values["sidecar"] = map[string]any{
		"datasources": map[string]any{"skipReload": true, "initDatasources": true},
		"dashboards":  map[string]any{"skipReload": true},
	}

	values["grafana.ini"] = map[string]any{
		"server":         map[string]any{"root_url": strings.TrimRight(h.serverURL, "/") + publicPath, "serve_from_sub_path": false},
		"auth":           map[string]any{"disable_login_form": true, "disable_signout_menu": true},
		"auth.anonymous": map[string]any{"enabled": false},
		"auth.basic":     map[string]any{"enabled": false},
		"auth.proxy":     map[string]any{"enabled": true, "header_name": "X-WEBAUTH-USER", "header_property": "email", "auto_sign_up": true, "enable_login_token": false, "sync_ttl": 0, "whitelist": "127.0.0.1", "headers": "Email:X-WEBAUTH-USER Name:X-WEBAUTH-USER Role:X-WEBAUTH-ROLE"},
		"security":       map[string]any{"allow_embedding": true, "csrf_trusted_origins": hostnameOf(h.serverURL)},
		"users":          map[string]any{"allow_sign_up": false, "auto_assign_org": true, "auto_assign_org_role": "Viewer"},
		"live":           map[string]any{"max_connections": 0},
	}
	return values
}
