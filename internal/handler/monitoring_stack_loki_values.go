package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func (h *MonitoringHandler) sharedLokiHelmValues(req SharedLokiRequest, existing map[string]any, storage map[string]any, prefix string) map[string]any {
	mode := req.Mode
	if mode == "" {
		mode = stringFromMap(existing, "mode")
	}
	if mode == "" {
		mode = sizerModeSingleBinary
	}
	if existingPrefix := stringFromMap(existing, "computedLokiPrefix"); existingPrefix != "" {
		if stringFromMap(existing, "storageConfigId") == req.StorageConfigID || req.StorageConfigID == "" {
			prefix = existingPrefix
		}
	}
	if prefix == "" {
		prefix = "loki"
	}
	setLokiObjectStorePrefix(storage, prefix)
	schemaFrom := defaultString(stringFromMap(existing, "schemaFrom"), sharedLokiSchemaFrom)
	deploymentMode := "SingleBinary"
	singleReplicas := 1
	readReplicas := 0
	writeReplicas := 0
	backendReplicas := 0
	replication := 1
	rateMB, burstMB, streams, queryLen, queryPar, bodySize := 1, 2, 5000, "7d", 8, "4m"
	if mode == sizerModeSimpleScalable {
		deploymentMode = "SimpleScalable"
		singleReplicas = 0
		readReplicas = 2
		writeReplicas = 2
		backendReplicas = 2
		replication = 2
		rateMB, burstMB, streams, queryLen, queryPar, bodySize = 2, 4, 20000, "30d", 32, "8m"
	}
	walSize := defaultString(req.WalStorageSize, "10Gi")
	persistence := map[string]any{"enabled": true, "size": walSize}
	if req.StorageClass != "" {
		persistence["storageClass"] = req.StorageClass
	}
	image := ""
	if h != nil {
		image = h.proxyImage
	}
	return map[string]any{
		"fullnameOverride": req.ReleaseName,
		"deploymentMode":   deploymentMode,
		"loki": map[string]any{
			"auth_enabled": true,
			"commonConfig": map[string]any{
				"replication_factor": replication,
				"path_prefix":        "/var/loki",
			},
			"schemaConfig": map[string]any{
				"configs": []any{
					map[string]any{
						"from":         schemaFrom,
						"store":        "tsdb",
						"object_store": "s3",
						"schema":       "v13",
						"index": map[string]any{
							"prefix": "loki_index_",
							"period": "24h",
						},
					},
				},
			},
			"storage": storage,
			"limits_config": map[string]any{
				"ingestion_rate_mb":           rateMB,
				"ingestion_burst_size_mb":     burstMB,
				"max_streams_per_user":        streams,
				"max_global_streams_per_user": streams,
				"max_line_size":               262144,
				"retention_period":            req.Retention,
				"max_query_length":            queryLen,
				"max_query_parallelism":       queryPar,
				"allow_structured_metadata":   true,
				"volume_enabled":              true,
			},
			"compactor": map[string]any{
				"retention_enabled":    true,
				"delete_request_store": "s3",
			},
		},
		"singleBinary": map[string]any{
			"replicas":    singleReplicas,
			"persistence": persistence,
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "1000m", "memory": "2Gi"},
				"limits":   map[string]any{"cpu": "2000m", "memory": "4Gi"},
			},
		},
		"backend":      lokiComponentValues(backendReplicas, mode == sizerModeSimpleScalable, lokiSimpleScalablePodResources(), nil),
		"read":         lokiComponentValues(readReplicas, mode == sizerModeSimpleScalable, lokiSimpleScalablePodResources(), nil),
		"write":        lokiComponentValues(writeReplicas, mode == sizerModeSimpleScalable, lokiSimpleScalablePodResources(), persistence),
		"gateway":      lokiGatewayValues(mode, bodySize),
		"minio":        map[string]any{"enabled": false},
		"lokiCanary":   map[string]any{"enabled": false},
		"test":         map[string]any{"enabled": false},
		"chunksCache":  map[string]any{"enabled": false},
		"resultsCache": map[string]any{"enabled": false},
		"monitoring": map[string]any{
			"selfMonitoring": map[string]any{
				"enabled": false,
				"grafanaAgent": map[string]any{
					"installOperator": false,
				},
			},
			"lokiCanary": map[string]any{"enabled": false},
		},
		"extraObjects": lokiFamilyExtraObjects(req, image, h.grafanaExpose),
	}
}

func (h *MonitoringHandler) lokiIngestClass() string {
	if h == nil {
		return ""
	}
	return h.grafanaExpose.IngressClass
}

type lokiTokenHashLister interface {
	ListLokiIngestTokenHashes(ctx context.Context) ([]sqlc.ListLokiIngestTokenHashesRow, error)
}

func (h *MonitoringHandler) lokiHasIngestTokens(ctx context.Context) bool {
	if h == nil || h.queries == nil {
		return false
	}
	lister, ok := h.queries.(lokiTokenHashLister)
	if !ok {
		return false
	}
	rows, err := lister.ListLokiIngestTokenHashes(ctx)
	return err == nil && len(rows) > 0
}

func setLokiObjectStorePrefix(storage map[string]any, prefix string) {
	if storage == nil {
		return
	}
	obj, _ := storage["object_store"].(map[string]any)
	if obj == nil {
		obj = map[string]any{"type": "s3"}
		storage["object_store"] = obj
	}
	obj["prefix"] = prefix
}

func lokiSimpleScalablePodResources() map[string]any {
	// write/read/backend ×2 = 3000m / 6Gi; gateway adds 500m / 2Gi → 3500m / 8Gi chart.
	return map[string]any{
		"requests": map[string]any{"cpu": "500m", "memory": "1Gi"},
		"limits":   map[string]any{"cpu": "1000m", "memory": "2Gi"},
	}
}

func lokiSimpleScalableGatewayResources() map[string]any {
	return map[string]any{
		"requests": map[string]any{"cpu": "500m", "memory": "2Gi"},
		"limits":   map[string]any{"cpu": "1000m", "memory": "4Gi"},
	}
}

func lokiComponentValues(replicas int, withResources bool, resources map[string]any, persistence map[string]any) map[string]any {
	out := map[string]any{"replicas": replicas}
	if persistence != nil {
		out["persistence"] = persistence
	}
	if withResources {
		out["resources"] = resources
	}
	return out
}

func lokiGatewayValues(mode, bodySize string) map[string]any {
	out := map[string]any{
		"enabled": true,
		"service": map[string]any{"type": "ClusterIP"},
		"ingress": map[string]any{"enabled": false},
		"nginxConfig": map[string]any{
			"clientMaxBodySize": bodySize,
		},
	}
	if mode == sizerModeSimpleScalable {
		out["resources"] = lokiSimpleScalableGatewayResources()
	}
	return out
}

func lokiFamilyExtraObjects(req SharedLokiRequest, image string, expose GrafanaExpose) []any {
	ns := defaultString(req.Namespace, "monitoring")
	release := defaultString(req.ReleaseName, sharedLokiDefaultRelease)
	_, authURL := lokiDerivedURLs(release, ns)
	gateway := fmt.Sprintf("http://%s-gateway.%s.svc.cluster.local", release, ns)
	labels := map[string]any{
		"app.kubernetes.io/name":      "loki-auth",
		"app.kubernetes.io/instance":  release,
		"app.kubernetes.io/component": "loki-auth",
	}
	svcName := release + "-auth"
	// Public ingest (Ingress/HTTPRoute/Certificate) is reconcile-owned so
	// Helm upgrades cannot fight a rotate-created object, and uninstall
	// can delete it even though Helm never adopted it.
	return []any{
		lokiAuthDeployment(ns, svcName, labels, image, gateway),
		lokiAuthService(ns, svcName, labels),
		lokiAuthListenNetworkPolicy(ns, svcName, labels, expose),
		lokiGatewayFromAuthNetworkPolicy(ns, release, labels),
		grafanaDatasourceConfigMap(
			release+"-grafana-datasource",
			ns,
			release,
			lokiGrafanaDatasourceYAML(authURL),
		),
	}
}

func lokiAuthDeployment(namespace, name string, labels map[string]any, image, upstream string) map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      name,
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
							"name":  "loki-auth",
							"image": image,
							"args":  []any{"loki-auth"},
							"ports": []any{
								map[string]any{"name": "http", "containerPort": lokiAuthListenPort},
							},
							"env": []any{
								map[string]any{"name": "LISTEN_ADDR", "value": fmt.Sprintf(":%d", lokiAuthListenPort)},
								map[string]any{"name": "LOKI_UPSTREAM", "value": upstream},
								map[string]any{"name": "HASHES_PATH", "value": "/var/run/loki-auth/hashes/hashes"},
								map[string]any{"name": "ACL_PATH", "value": "/var/run/loki-auth/acl/acl"},
								map[string]any{"name": "QUERY_KEY_PATH", "value": "/var/run/loki-auth/query-key/key"},
							},
							"volumeMounts": []any{
								map[string]any{"name": "token-hashes", "mountPath": "/var/run/loki-auth/hashes", "readOnly": true},
								map[string]any{"name": "query-acl", "mountPath": "/var/run/loki-auth/acl", "readOnly": true},
								map[string]any{"name": "query-key", "mountPath": "/var/run/loki-auth/query-key", "readOnly": true},
							},
							"readinessProbe": map[string]any{
								"httpGet": map[string]any{"path": "/ready", "port": lokiAuthListenPort},
							},
							"resources": map[string]any{
								"requests": map[string]any{"cpu": "100m", "memory": "64Mi"},
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
					"volumes": []any{
						map[string]any{
							"name": "token-hashes",
							"secret": map[string]any{
								"secretName": lokiTokenHashSecretName,
								"optional":   true,
							},
						},
						map[string]any{
							"name": "query-acl",
							"configMap": map[string]any{
								"name":     lokiQueryACLConfigMapName,
								"optional": true,
							},
						},
						map[string]any{
							"name": "query-key",
							"secret": map[string]any{
								"secretName": lokiQuerySecretName,
								"optional":   true,
							},
						},
					},
				},
			},
		},
	}
}

func lokiAuthService(namespace, name string, labels map[string]any) map[string]any {
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
				map[string]any{"name": "http", "port": lokiAuthListenPort, "targetPort": lokiAuthListenPort},
			},
		},
	}
}

func lokiGrafanaDatasourceYAML(url string) string {
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
      httpHeaderName1: Authorization
      # Grafana dataproxy.send_user_header ships X-Grafana-User only after the
      # datasource proves it owns the dedicated query key.
    secureJsonData:
      httpHeaderValue1: 'Bearer $LOKI_QUERY_KEY'
`, yamlQuotedString(url))) + "\n"
}

func lokiAuthListenNetworkPolicy(namespace, name string, labels map[string]any, expose GrafanaExpose) map[string]any {
	from := []any{
		map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{
			"app.kubernetes.io/name":     "grafana",
			"app.kubernetes.io/instance": sharedGrafanaDefaultRelease,
		}}},
	}
	if expose.PlatformNamespace != "" && expose.GatewayName != "" {
		from = append(from, map[string]any{
			"namespaceSelector": map[string]any{"matchLabels": map[string]any{
				"kubernetes.io/metadata.name": expose.PlatformNamespace,
			}},
			"podSelector": map[string]any{"matchLabels": map[string]any{
				"gateway.networking.k8s.io/gateway-name": expose.GatewayName,
			}},
		})
	}
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
					"from": from,
					"ports": []any{
						map[string]any{"protocol": "TCP", "port": lokiAuthListenPort},
					},
				},
			},
		},
	}
}

func lokiGatewayFromAuthNetworkPolicy(namespace, release string, authLabels map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      release + "-gateway-from-auth",
			"namespace": namespace,
		},
		"spec": map[string]any{
			"podSelector": map[string]any{
				"matchLabels": map[string]any{
					"app.kubernetes.io/name":      "loki",
					"app.kubernetes.io/instance":  release,
					"app.kubernetes.io/component": "gateway",
				},
			},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{
				map[string]any{
					"from": []any{
						map[string]any{"podSelector": map[string]any{"matchLabels": authLabels}},
					},
				},
			},
		},
	}
}

func lokiIngestShouldBePublic(status string, hasTokens bool, hostname string) bool {
	if !hasTokens || strings.TrimSpace(hostname) == "" {
		return false
	}
	switch status {
	case "", "not_configured", "uninstalled":
		return false
	default:
		return true
	}
}

func lokiTLSIssuer(expose GrafanaExpose) (name, kind string) {
	name = strings.TrimSpace(expose.TLSIssuerName)
	if name == "" {
		name = "astronomer-tls"
	}
	kind = strings.TrimSpace(expose.TLSIssuerKind)
	if kind == "" {
		kind = "Issuer"
	}
	return name, kind
}

func lokiIngestCertificate(namespace, host string, expose GrafanaExpose) map[string]any {
	issuer, kind := lokiTLSIssuer(expose)
	return map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]any{
			"name":      lokiIngestTLSSecretName,
			"namespace": namespace,
		},
		"spec": map[string]any{
			"secretName": lokiIngestTLSSecretName,
			"dnsNames":   []any{host},
			"issuerRef": map[string]any{
				"name":  issuer,
				"kind":  kind,
				"group": "cert-manager.io",
			},
		},
	}
}

func lokiIngestIngress(namespace, svcName, host, ingressClass string, expose GrafanaExpose) map[string]any {
	issuer, kind := lokiTLSIssuer(expose)
	annotations := map[string]any{}
	if strings.EqualFold(kind, "ClusterIssuer") {
		annotations["cert-manager.io/cluster-issuer"] = issuer
	} else {
		annotations["cert-manager.io/issuer"] = issuer
	}
	spec := map[string]any{
		"tls": []any{
			map[string]any{
				"hosts":      []any{host},
				"secretName": lokiIngestTLSSecretName,
			},
		},
		"rules": []any{
			map[string]any{
				"host": host,
				"http": map[string]any{
					"paths": []any{
						map[string]any{
							"path":     "/loki/api/v1/push",
							"pathType": "Prefix",
							"backend": map[string]any{
								"service": map[string]any{
									"name": svcName,
									"port": map[string]any{"number": lokiAuthListenPort},
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
			"name":        svcName,
			"namespace":   namespace,
			"annotations": annotations,
		},
		"spec": spec,
	}
}

func lokiIngestHTTPRoute(platformNS, gatewayName, lokiNS, svcName, host string) map[string]any {
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]any{
			"name":      svcName,
			"namespace": platformNS,
		},
		"spec": map[string]any{
			"parentRefs": []any{
				map[string]any{"name": gatewayName, "sectionName": lokiIngestGatewayListener},
			},
			"hostnames": []any{host},
			"rules": []any{
				map[string]any{
					"matches": []any{
						map[string]any{
							"path": map[string]any{"type": "PathPrefix", "value": "/loki/api/v1/push"},
						},
					},
					"backendRefs": []any{
						map[string]any{
							"name":      svcName,
							"namespace": lokiNS,
							"port":      lokiAuthListenPort,
						},
					},
				},
			},
		},
	}
}
