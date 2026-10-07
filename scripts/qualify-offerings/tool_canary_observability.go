package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func evaluateIngressCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, phase string) (result dimensionResult) {
	namespace := memberNamespace(execution.Config, clusterID)
	if namespace == "" {
		return failedCanary(clusterID, fmt.Errorf("no qualification namespace is configured for the ingress target"))
	}
	suffix := digest([]byte(execution.RunID + "-ingress-" + phase))[:10]
	name := "astr-qual-ingress-" + suffix
	marker := "astronomer-ingress-" + suffix
	route := "/" + name
	clusterBase := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s"
	coreBase := clusterBase + "/api/v1/namespaces/" + url.PathEscape(namespace)
	ingressBase := clusterBase + "/apis/networking.k8s.io/v1/namespaces/" + url.PathEscape(namespace)
	podPath, servicePath := coreBase+"/pods/"+name, coreBase+"/services/"+name
	ingressPath, networkPolicyPath := ingressBase+"/ingresses/"+name, ingressBase+"/networkpolicies/"+name
	podCreated, serviceCreated, networkPolicyCreated, ingressCreated := false, false, false, false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cleanupErrors := []string{}
		for _, item := range []struct {
			created bool
			path    string
		}{{ingressCreated, ingressPath}, {networkPolicyCreated, networkPolicyPath}, {serviceCreated, servicePath}, {podCreated, podPath}} {
			if !item.created {
				continue
			}
			if _, err := requestAPI(cleanupCtx, client, execution.Base, execution.Token, http.MethodDelete, item.path, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound); err != nil {
				cleanupErrors = append(cleanupErrors, err.Error())
				continue
			}
			if err := waitK8sDeleted(cleanupCtx, client, execution, item.path, time.Second); err != nil {
				cleanupErrors = append(cleanupErrors, err.Error())
			}
		}
		if len(cleanupErrors) > 0 {
			result = failedCanary(clusterID, fmt.Errorf("ingress canary cleanup failed: %s", strings.Join(cleanupErrors, "; ")))
		}
	}()

	labels := map[string]any{"app.kubernetes.io/name": name, "app.kubernetes.io/managed-by": "astronomer-qualification"}
	pod := map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": namespace, "labels": labels},
		"spec": map[string]any{
			"containers": []any{map[string]any{
				"name": "backend", "image": "busybox:1.36",
				"command":        []any{"sh", "-ec", "mkdir -p /www; printf '%s\\n' \"$MARKER\" > /www/index.html; exec httpd -f -p 8080 -h /www"},
				"env":            []any{map[string]any{"name": "MARKER", "value": marker}},
				"ports":          []any{map[string]any{"name": "http", "containerPort": 8080}},
				"readinessProbe": map[string]any{"httpGet": map[string]any{"path": "/", "port": 8080}, "periodSeconds": 1},
			}},
		},
	}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, coreBase+"/pods", pod, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create ingress canary backend: %w", err))
	}
	podCreated = true
	if err := pollK8sObject(ctx, client, execution, podPath, 2*time.Second, func(object map[string]any) (bool, error) {
		status, _ := object["status"].(map[string]any)
		if stringField(status, "phase") == "Failed" {
			return false, fmt.Errorf("backend pod entered Failed phase")
		}
		return k8sPodReady(object), nil
	}); err != nil {
		return failedCanary(clusterID, fmt.Errorf("wait for ingress canary backend: %w", err))
	}

	service := map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"name": name, "namespace": namespace, "labels": labels},
		"spec":     map[string]any{"selector": labels, "ports": []any{map[string]any{"name": "http", "port": 8080, "targetPort": "http"}}},
	}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, coreBase+"/services", service, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create ingress canary Service: %w", err))
	}
	serviceCreated = true
	networkPolicy := map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": name, "namespace": namespace, "labels": labels},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": name}},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{map[string]any{
				"from":  []any{map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": "astronomer-ingress-nginx"}}}},
				"ports": []any{map[string]any{"protocol": "TCP", "port": 8080}},
			}},
		},
	}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, ingressBase+"/networkpolicies", networkPolicy, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("allow ingress controller to reach canary backend: %w", err))
	}
	networkPolicyCreated = true
	ingress := map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "Ingress",
		"metadata": map[string]any{
			"name": name, "namespace": namespace, "labels": labels,
			"annotations": map[string]any{"nginx.ingress.kubernetes.io/rewrite-target": "/"},
		},
		"spec": map[string]any{
			"ingressClassName": "nginx",
			"rules": []any{map[string]any{"http": map[string]any{"paths": []any{map[string]any{
				"path": route, "pathType": "Prefix",
				"backend": map[string]any{"service": map[string]any{"name": name, "port": map[string]any{"number": 8080}}},
			}}}}},
		},
	}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, ingressBase+"/ingresses", ingress, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create ingress canary route: %w", err))
	}
	ingressCreated = true

	proxyPath := clusterBase + "/api/v1/namespaces/astronomer-ingress-nginx/services/http:ingress-nginx-controller:80/proxy" + route
	for {
		routed, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, proxyPath, http.StatusOK, http.StatusNotFound, http.StatusBadGateway, http.StatusServiceUnavailable)
		if err == nil && routed.Status == http.StatusOK && strings.Contains(string(routed.Body), marker) {
			now := time.Now().UTC()
			return dimensionResult{Name: "functional_canary", State: "PASS", Reason: fmt.Sprintf("ingress-nginx routed %s through the controller to Service %s/%s and returned the unique backend marker", route, namespace, name), ObservedAt: now, HTTPStatus: routed.Status, ArtifactSHA: digest(routed.Body), SampleAt: &now, TargetClusterID: clusterID}
		}
		select {
		case <-ctx.Done():
			return failedCanary(clusterID, fmt.Errorf("ingress route did not return its backend marker: %w", ctx.Err()))
		case <-time.After(2 * time.Second):
		}
	}
}

type metricCanarySpec struct {
	Namespace string
	Service   string
	Port      int
	Path      string
	Metrics   []string
}

func metricCanaryFor(slug string) (metricCanarySpec, bool) {
	specs := map[string]metricCanarySpec{
		"fluent-bit":               {Namespace: "astronomer-logging", Service: "fluent-bit", Port: 2020, Path: "/api/v1/metrics/prometheus", Metrics: []string{"fluentbit_input_records_total", "fluentbit_output_proc_records_total"}},
		"kube-state-metrics":       {Namespace: "astronomer-monitoring", Service: "kube-state-metrics", Port: 8080, Path: "/metrics", Metrics: []string{"kube_pod_info", "kube_node_info"}},
		"prometheus-node-exporter": {Namespace: "astronomer-monitoring", Service: "prometheus-node-exporter", Port: 9100, Path: "/metrics", Metrics: []string{"node_cpu_seconds_total", "node_memory_MemTotal_bytes"}},
	}
	spec, ok := specs[slug]
	return spec, ok
}

func evaluateMetricCanary(ctx context.Context, client *http.Client, execution executionContext, slug, clusterID string, spec metricCanarySpec) dimensionResult {
	path := fmt.Sprintf("/api/v1/clusters/%s/k8s/api/v1/namespaces/%s/services/http:%s:%d/proxy%s",
		url.PathEscape(clusterID), url.PathEscape(spec.Namespace), url.PathEscape(spec.Service), spec.Port, spec.Path)
	response, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("scrape %s metrics through Kubernetes service proxy: %w", slug, err))
	}
	if !strings.Contains(strings.ToLower(response.ContentType), "text/plain") {
		return failedCanary(clusterID, fmt.Errorf("%s metrics endpoint returned content type %q", slug, response.ContentType))
	}
	for _, metric := range spec.Metrics {
		if !prometheusMetricHasPositiveSample(response.Body, metric) {
			return failedCanary(clusterID, fmt.Errorf("%s metrics endpoint had no positive %s sample", slug, metric))
		}
	}
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: fmt.Sprintf("%s metrics endpoint returned live positive samples for %s", slug, strings.Join(spec.Metrics, ", ")), ObservedAt: now, HTTPStatus: response.Status, ArtifactSHA: digest(response.Body), SampleAt: &now, TargetClusterID: clusterID}
}

func prometheusMetricHasPositiveSample(raw []byte, metric string) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || (!strings.HasPrefix(line, metric+"{") && !strings.HasPrefix(line, metric+" ")) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil && value > 0 {
			return true
		}
	}
	return false
}
