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

func evaluateIngressCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID string) dimensionResult {
	base := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/api/v1/namespaces/astronomer-ingress-nginx/services/"
	routed, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet,
		base+"http:ingress-nginx-controller:80/proxy/", http.StatusNotFound)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("send request through ingress controller service: %w", err))
	}
	if !strings.Contains(strings.ToLower(string(routed.Body)), "404") {
		return failedCanary(clusterID, fmt.Errorf("ingress controller did not return its expected unmatched-route response"))
	}
	metrics, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet,
		base+"http:ingress-nginx-controller-metrics:10254/proxy/metrics", http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("scrape ingress controller metrics: %w", err))
	}
	if !prometheusMetricHasPositiveSample(metrics.Body, "nginx_ingress_controller_build_info") {
		return failedCanary(clusterID, fmt.Errorf("ingress controller metrics lacked a positive build-info sample"))
	}
	raw := append(append([]byte{}, routed.Body...), metrics.Body...)
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: "ingress controller served the expected unmatched-route response and exposed live controller metrics", ObservedAt: now, HTTPStatus: routed.Status, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
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
		if line == "" || strings.HasPrefix(line, "#") || !(strings.HasPrefix(line, metric+"{") || strings.HasPrefix(line, metric+" ")) {
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
