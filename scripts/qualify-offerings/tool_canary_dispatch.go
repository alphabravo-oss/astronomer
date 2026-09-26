package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func evaluateToolCanary(ctx context.Context, client *http.Client, execution executionContext, slug, clusterID string, status map[string]any, phase string) dimensionResult {
	if slug == "cis-operator" {
		return evaluateCISCanary(ctx, client, execution, clusterID, 2*time.Second)
	}
	if slug == "trivy-operator" {
		return evaluateTrivyCanary(ctx, client, execution, clusterID, phase, 2*time.Second)
	}
	if slug == "istio" {
		return evaluateIstioCanary(ctx, client, execution, clusterID)
	}
	if slug == "cert-manager" {
		return evaluateCertManagerCanary(ctx, client, execution, clusterID, phase, 2*time.Second)
	}
	if slug == "gatekeeper" {
		return evaluateGatekeeperCanary(ctx, client, execution, clusterID, phase, 2*time.Second)
	}
	if slug == "longhorn" {
		return evaluateLonghornCanary(ctx, client, execution, clusterID, phase, 2*time.Second)
	}
	if slug == "neuvector" {
		return evaluateNeuVectorCanary(ctx, client, execution, clusterID)
	}
	if slug == "ingress-nginx" {
		return evaluateIngressCanary(ctx, client, execution, clusterID)
	}
	if spec, ok := metricCanaryFor(slug); ok {
		return evaluateMetricCanary(ctx, client, execution, slug, clusterID, spec)
	}
	now := time.Now().UTC()
	raw, _ := json.Marshal(status)
	return dimensionResult{Name: "functional_canary", State: "FAIL", Reason: fmt.Sprintf("%s installed, but its product-specific API canary has not produced evidence; release readiness is insufficient", slug), ObservedAt: now, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
}

func evaluateNeuVectorCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID string) dimensionResult {
	path := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/api/v1/namespaces/cattle-neuvector-system/pods"
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, nil, "", http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("list NeuVector components: %w", err))
	}
	body, ok := response.Body.(map[string]any)
	if !ok {
		return failedCanary(clusterID, fmt.Errorf("NeuVector pod inventory was not a Kubernetes list"))
	}
	items, _ := body["items"].([]any)
	readyByApp := map[string][]string{}
	for _, item := range items {
		pod, _ := item.(map[string]any)
		metadata, _ := pod["metadata"].(map[string]any)
		labels, _ := metadata["labels"].(map[string]any)
		app := stringField(labels, "app")
		if app == "" || !k8sPodReady(pod) {
			continue
		}
		readyByApp[app] = append(readyByApp[app], stringField(metadata, "name"))
	}
	for _, app := range []string{"neuvector-controller-pod", "neuvector-scanner-pod", "neuvector-enforcer-pod"} {
		if len(readyByApp[app]) == 0 {
			return failedCanary(clusterID, fmt.Errorf("NeuVector component %s had no Ready pod", app))
		}
	}
	controllerPod := readyByApp["neuvector-controller-pod"][0]
	readyPath := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/api/v1/namespaces/cattle-neuvector-system/pods/http:" + url.PathEscape(controllerPod) + ":18500/proxy/ready"
	ready, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, readyPath, http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("call NeuVector controller readiness API: %w", err))
	}
	raw, _ := json.Marshal(map[string]any{"pods": response.Body, "controller_ready": string(ready.Body)})
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: fmt.Sprintf("NeuVector controller API answered and controller, scanner, and %d enforcer pods were Ready", len(readyByApp["neuvector-enforcer-pod"])), ObservedAt: now, HTTPStatus: ready.Status, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
}

func k8sPodReady(pod map[string]any) bool {
	status, _ := pod["status"].(map[string]any)
	if stringField(status, "phase") != "Running" {
		return false
	}
	conditions, _ := status["conditions"].([]any)
	for _, item := range conditions {
		condition, _ := item.(map[string]any)
		if stringField(condition, "type") == "Ready" && strings.EqualFold(stringField(condition, "status"), "True") {
			return true
		}
	}
	return false
}
