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

func evaluateIstioCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID string) dimensionResult {
	path := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/service-mesh/detect/"
	detected, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, path, nil, "", http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("run live Istio detection: %w", err))
	}
	mesh, err := objectAtPath(detected.Body, "data")
	if err != nil {
		return failedCanary(clusterID, err)
	}
	if stringField(mesh, "cluster_id") != clusterID || strings.ToLower(stringField(mesh, "detected_mesh")) != "istio" || stringField(mesh, "detected_version") == "" || stringField(mesh, "control_plane_namespace") == "" || stringField(mesh, "last_error") != "" {
		return failedCanary(clusterID, fmt.Errorf("live detection did not report a healthy versioned Istio control plane"))
	}
	validation, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost,
		"/api/v1/clusters/"+url.PathEscape(clusterID)+"/service-mesh/validate/",
		map[string]any{"object": map[string]any{"apiVersion": "security.istio.io/v1beta1", "kind": "PeerAuthentication", "metadata": map[string]any{"name": "qualification", "namespace": "default"}, "spec": map[string]any{"mtls": map[string]any{"mode": "STRICT"}}}}, "", http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("validate Istio policy through product API: %w", err))
	}
	validated, err := objectAtPath(validation.Body, "data")
	if err != nil || validated["valid"] != true || stringField(validated, "kind") != "PeerAuthentication" {
		return failedCanary(clusterID, fmt.Errorf("Istio policy validation did not accept the bounded canary object"))
	}
	raw, _ := json.Marshal(map[string]any{"detection": detected.Body, "validation": validation.Body})
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: fmt.Sprintf("live mesh detection reported Istio %s in %s and policy validation succeeded", stringField(mesh, "detected_version"), stringField(mesh, "control_plane_namespace")), ObservedAt: now, HTTPStatus: http.StatusOK, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
}
