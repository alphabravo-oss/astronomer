package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func evaluateAppCanary(ctx context.Context, client *http.Client, execution executionContext, slug, clusterID string, spec appInstallSpec, phase string) dimensionResult {
	// Products offered through both Apps and Tools must prove the same product
	// effect through each independent ownership path.
	if _, shared := map[string]bool{
		"kube-state-metrics": true, "prometheus-node-exporter": true, "trivy-operator": true,
		"cert-manager": true, "ingress-nginx": true, "longhorn": true,
		"gatekeeper": true, "fluent-bit": true,
	}[slug]; shared {
		return evaluateToolCanary(ctx, client, execution, slug, clusterID, map[string]any{"namespace": spec.Namespace, "release_name": spec.ReleaseName}, phase)
	}
	switch slug {
	case "constellation":
		return evaluateJSONHealthCanary(ctx, client, execution, slug, clusterID, spec.Namespace, "constellation-api", 8080, "/healthz", []string{"status"})
	case "metrics-server":
		return evaluateMetricsServerCanary(ctx, client, execution, clusterID)
	case "grafana":
		return evaluateJSONHealthCanary(ctx, client, execution, slug, clusterID, spec.Namespace, "grafana", 80, "/api/health", []string{"database", "version"})
	case "kube-prometheus-stack":
		return evaluatePrometheusCanary(ctx, client, execution, clusterID, spec.Namespace)
	case "loki":
		return evaluateTextHealthCanary(ctx, client, execution, slug, clusterID, spec.Namespace, []serviceProbe{{"loki-gateway", 80, "/ready"}, {"loki", 3100, "/ready"}}, "ready")
	case "external-secrets":
		return evaluateExternalSecretsCanary(ctx, client, execution, clusterID, phase)
	case "kyverno":
		return evaluateKyvernoCanary(ctx, client, execution, clusterID, phase)
	case "external-dns":
		return evaluateExternalDNSCanary(ctx, client, execution, clusterID, spec.Namespace, phase)
	case "opentelemetry-collector":
		return evaluateOTelCanary(ctx, client, execution, clusterID, spec.Namespace, spec.ReleaseName)
	case "tempo":
		return evaluateTempoCanary(ctx, client, execution, clusterID, spec.Namespace)
	case "velero":
		return evaluateVeleroNodeAgentCanary(ctx, client, execution, clusterID, spec.Namespace)
	case "keda":
		return evaluateKEDACanary(ctx, client, execution, clusterID, phase)
	case "cloudnative-pg":
		return evaluateCloudNativePGCanary(ctx, client, execution, clusterID, phase)
	}
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "FAIL", Reason: fmt.Sprintf("%s installed, but no product-effect API canary is compiled", slug), ObservedAt: now, ArtifactSHA: digest([]byte(slug)), SampleAt: &now, TargetClusterID: clusterID}
}

func evaluateTempoCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, namespace string) dimensionResult {
	traceID := digest([]byte(execution.RunID + time.Now().UTC().String()))[:32]
	nowNanos := time.Now().UTC().UnixNano()
	body := map[string]any{"resourceSpans": []any{map[string]any{"resource": map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "astronomer-qualification"}}}}, "scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "astronomer-qualification"}, "spans": []any{map[string]any{"traceId": traceID, "spanId": traceID[:16], "name": "tempo-qualification", "kind": 1, "startTimeUnixNano": fmt.Sprint(nowNanos), "endTimeUnixNano": fmt.Sprint(nowNanos + int64(time.Millisecond))}}}}}}}
	writePath := fmt.Sprintf("/api/v1/clusters/%s/k8s/api/v1/namespaces/%s/services/http:tempo:4318/proxy/v1/traces", url.PathEscape(clusterID), url.PathEscape(namespace))
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, writePath, body, "", http.StatusOK); err != nil {
		return failedCanary(clusterID, fmt.Errorf("send Tempo OTLP trace: %w", err))
	}
	readPath := fmt.Sprintf("/api/v1/clusters/%s/k8s/api/v1/namespaces/%s/services/http:tempo:3100/proxy/api/traces/%s", url.PathEscape(clusterID), url.PathEscape(namespace), traceID)
	for {
		response, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, readPath, http.StatusOK, http.StatusNotFound)
		if err == nil && response.Status == http.StatusOK && strings.Contains(string(response.Body), "tempo-qualification") {
			now := time.Now().UTC()
			return dimensionResult{Name: "functional_canary", State: "PASS", Reason: "Tempo ingested an OTLP trace and returned the same trace by ID", ObservedAt: now, HTTPStatus: response.Status, ArtifactSHA: digest(response.Body), SampleAt: &now, TargetClusterID: clusterID}
		}
		select {
		case <-ctx.Done():
			return failedCanary(clusterID, fmt.Errorf("Tempo did not return ingested trace %s: %w", traceID, ctx.Err()))
		case <-time.After(2 * time.Second):
		}
	}
}

func evaluateKEDACanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, phase string) (result dimensionResult) {
	namespace := memberNamespace(execution.Config, clusterID)
	name := "astr-qual-keda-" + digest([]byte(execution.RunID + phase))[:10]
	base := "/api/v1/clusters/" + url.PathEscape(clusterID)
	deployPath := base + "/k8s/apis/apps/v1/namespaces/" + url.PathEscape(namespace) + "/deployments/" + name
	scaledPath := base + "/k8s/apis/keda.sh/v1alpha1/namespaces/" + url.PathEscape(namespace) + "/scaledobjects/" + name
	deployCreated, scaledCreated := false, false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, item := range []struct {
			created bool
			path    string
		}{{scaledCreated, scaledPath}, {deployCreated, deployPath}} {
			if item.created {
				_, _ = requestAPI(cleanupCtx, client, execution.Base, execution.Token, http.MethodDelete, item.path, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound)
			}
		}
	}()
	labels := map[string]any{"app": name}
	deployment := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"replicas": 0, "selector": map[string]any{"matchLabels": labels}, "template": map[string]any{"metadata": map[string]any{"labels": labels}, "spec": map[string]any{"containers": []any{map[string]any{"name": "canary", "image": "busybox:1.36", "command": []any{"sh", "-c", "sleep 3600"}}}}}}}
	deployCollection := base + "/k8s/apis/apps/v1/namespaces/" + url.PathEscape(namespace) + "/deployments"
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, deployCollection, deployment, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create KEDA target: %w", err))
	}
	deployCreated = true
	current := time.Now().UTC()
	start := current.Add(-time.Minute)
	end := current.Add(2 * time.Minute)
	cron := func(value time.Time) string {
		return fmt.Sprintf("%d %d %d %d *", value.Minute(), value.Hour(), value.Day(), int(value.Month()))
	}
	scaled := map[string]any{"apiVersion": "keda.sh/v1alpha1", "kind": "ScaledObject", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"scaleTargetRef": map[string]any{"name": name}, "minReplicaCount": 0, "maxReplicaCount": 1, "pollingInterval": 2, "cooldownPeriod": 2, "triggers": []any{map[string]any{"type": "cron", "metadata": map[string]any{"timezone": "UTC", "start": cron(start), "end": cron(end), "desiredReplicas": "1"}}}}}
	scaledCollection := base + "/k8s/apis/keda.sh/v1alpha1/namespaces/" + url.PathEscape(namespace) + "/scaledobjects"
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, scaledCollection, scaled, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create KEDA ScaledObject: %w", err))
	}
	scaledCreated = true
	for {
		response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, deployPath, nil, "", http.StatusOK)
		if err != nil {
			return failedCanary(clusterID, err)
		}
		object, _ := response.Body.(map[string]any)
		specObj, _ := object["spec"].(map[string]any)
		statusObj, _ := object["status"].(map[string]any)
		if numberField(specObj, "replicas") >= 1 && numberField(statusObj, "availableReplicas") >= 1 {
			raw, _ := json.Marshal(response.Body)
			now := time.Now().UTC()
			return dimensionResult{Name: "functional_canary", State: "PASS", Reason: "KEDA cron scaler raised the target Deployment from zero to one Ready replica", ObservedAt: now, HTTPStatus: response.Status, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
		}
		select {
		case <-ctx.Done():
			return failedCanary(clusterID, fmt.Errorf("KEDA did not scale the target: %w", ctx.Err()))
		case <-time.After(2 * time.Second):
		}
	}
}

func evaluateCloudNativePGCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, phase string) (result dimensionResult) {
	namespace := memberNamespace(execution.Config, clusterID)
	name := "astr-qual-cnpg-" + digest([]byte(execution.RunID + phase))[:10]
	base := "/api/v1/clusters/" + url.PathEscape(clusterID)
	clusterPath := base + "/k8s/apis/postgresql.cnpg.io/v1/namespaces/" + url.PathEscape(namespace) + "/clusters/" + name
	jobPath := base + "/k8s/apis/batch/v1/namespaces/" + url.PathEscape(namespace) + "/jobs/" + name
	clusterCreated, jobCreated := false, false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		for _, item := range []struct {
			created bool
			path    string
		}{{jobCreated, jobPath}, {clusterCreated, clusterPath}} {
			if item.created {
				_, _ = requestAPI(cleanupCtx, client, execution.Base, execution.Token, http.MethodDelete, item.path, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound)
			}
		}
	}()
	cluster := map[string]any{"apiVersion": "postgresql.cnpg.io/v1", "kind": "Cluster", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"instances": 1, "storage": map[string]any{"size": "1Gi", "storageClass": "local-path"}}}
	collection := base + "/k8s/apis/postgresql.cnpg.io/v1/namespaces/" + url.PathEscape(namespace) + "/clusters"
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, collection, cluster, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create CloudNativePG cluster: %w", err))
	}
	clusterCreated = true
	for {
		response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, clusterPath, nil, "", http.StatusOK)
		if err != nil {
			return failedCanary(clusterID, err)
		}
		object, _ := response.Body.(map[string]any)
		statusObj, _ := object["status"].(map[string]any)
		conditions, _ := statusObj["conditions"].([]any)
		ready := false
		for _, item := range conditions {
			condition, _ := item.(map[string]any)
			if stringField(condition, "type") == "Ready" && strings.EqualFold(stringField(condition, "status"), "True") {
				ready = true
			}
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			return failedCanary(clusterID, fmt.Errorf("CloudNativePG cluster did not become Ready: %w", ctx.Err()))
		case <-time.After(3 * time.Second):
		}
	}
	job := map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"backoffLimit": 1, "template": map[string]any{"spec": map[string]any{"restartPolicy": "Never", "containers": []any{map[string]any{"name": "psql", "image": "postgres:16-alpine", "env": []any{map[string]any{"name": "PGUSER", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": name + "-app", "key": "username"}}}, map[string]any{"name": "PGPASSWORD", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": name + "-app", "key": "password"}}}, map[string]any{"name": "PGDATABASE", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": name + "-app", "key": "dbname"}}}, map[string]any{"name": "PGHOST", "value": name + "-rw"}}, "command": []any{"sh", "-ec", "psql -v ON_ERROR_STOP=1 -c \"CREATE TABLE qualification(value text); INSERT INTO qualification VALUES ('persisted');\" && psql -Atc \"SELECT value FROM qualification\""}}}}}}}
	jobCollection := base + "/k8s/apis/batch/v1/namespaces/" + url.PathEscape(namespace) + "/jobs"
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, jobCollection, job, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create CloudNativePG SQL job: %w", err))
	}
	jobCreated = true
	for {
		response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, jobPath, nil, "", http.StatusOK)
		if err != nil {
			return failedCanary(clusterID, err)
		}
		object, _ := response.Body.(map[string]any)
		statusObj, _ := object["status"].(map[string]any)
		if numberField(statusObj, "succeeded") >= 1 {
			raw, _ := json.Marshal(response.Body)
			now := time.Now().UTC()
			return dimensionResult{Name: "functional_canary", State: "PASS", Reason: "CloudNativePG reconciled a database cluster and a client Job completed a SQL write/read transaction", ObservedAt: now, HTTPStatus: response.Status, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
		}
		if numberField(statusObj, "failed") > 0 {
			return failedCanary(clusterID, errors.New("CloudNativePG SQL transaction Job failed"))
		}
		select {
		case <-ctx.Done():
			return failedCanary(clusterID, fmt.Errorf("CloudNativePG SQL transaction did not complete: %w", ctx.Err()))
		case <-time.After(3 * time.Second):
		}
	}
}

func evaluateMetricsServerCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID string) dimensionResult {
	path := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/apis/metrics.k8s.io/v1beta1/nodes"
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, nil, "", http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("query metrics.k8s.io nodes: %w", err))
	}
	items, err := arrayAtPath(response.Body, "items")
	if err != nil || len(items) == 0 {
		return failedCanary(clusterID, errors.New("metrics.k8s.io returned no node samples"))
	}
	for _, item := range items {
		object, _ := item.(map[string]any)
		usage, _ := object["usage"].(map[string]any)
		if stringField(usage, "cpu") == "" || stringField(usage, "memory") == "" || stringField(object, "timestamp") == "" {
			return failedCanary(clusterID, errors.New("metrics.k8s.io node sample lacked CPU, memory, or timestamp"))
		}
	}
	raw, _ := json.Marshal(response.Body)
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: fmt.Sprintf("metrics.k8s.io returned fresh CPU and memory for %d nodes", len(items)), ObservedAt: now, HTTPStatus: response.Status, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
}

func evaluateJSONHealthCanary(ctx context.Context, client *http.Client, execution executionContext, slug, clusterID, namespace, service string, port int, path string, required []string) dimensionResult {
	proxy := fmt.Sprintf("/api/v1/clusters/%s/k8s/api/v1/namespaces/%s/services/http:%s:%d/proxy%s", url.PathEscape(clusterID), url.PathEscape(namespace), url.PathEscape(service), port, path)
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, proxy, nil, "", http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("call %s health API: %w", slug, err))
	}
	data, ok := response.Body.(map[string]any)
	if !ok {
		return failedCanary(clusterID, fmt.Errorf("%s health response was not JSON object", slug))
	}
	for _, key := range required {
		if stringField(data, key) == "" {
			return failedCanary(clusterID, fmt.Errorf("%s health response lacked %s", slug, key))
		}
	}
	raw, _ := json.Marshal(response.Body)
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: slug + " health API returned live application metadata", ObservedAt: now, HTTPStatus: response.Status, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
}

func evaluatePrometheusCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, namespace string) dimensionResult {
	path := fmt.Sprintf("/api/v1/clusters/%s/k8s/api/v1/namespaces/%s/services/http:kube-prometheus-stack-prometheus:9090/proxy/api/v1/query?query=up", url.PathEscape(clusterID), url.PathEscape(namespace))
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, nil, "", http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("query Prometheus API: %w", err))
	}
	if status, _ := valueAtPath(response.Body, "status"); status != "success" {
		return failedCanary(clusterID, errors.New("Prometheus query did not report success"))
	}
	results, err := arrayAtPath(response.Body, "data.result")
	if err != nil || len(results) == 0 {
		return failedCanary(clusterID, errors.New("Prometheus up query returned no live targets"))
	}
	raw, _ := json.Marshal(response.Body)
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: fmt.Sprintf("Prometheus query API returned %d live target series", len(results)), ObservedAt: now, HTTPStatus: response.Status, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
}

type serviceProbe struct {
	Service string
	Port    int
	Path    string
}

// appReleaseFootprint returns the live, namespaced product effect for a Helm
// release. Catalog rows and Flux objects are control-plane bookkeeping; an
// uninstall is complete only when the workloads and services they created are
// absent as well.
func appReleaseFootprint(ctx context.Context, client *http.Client, execution executionContext, clusterID string, spec appInstallSpec) ([]string, error) {
	resources := []struct {
		apiPath string
		kind    string
	}{
		{"apis/apps/v1", "Deployment"},
		{"apis/apps/v1", "StatefulSet"},
		{"apis/apps/v1", "DaemonSet"},
		{"apis/batch/v1", "Job"},
		{"apis/batch/v1", "CronJob"},
		{"api/v1", "Pod"},
		{"api/v1", "Service"},
	}
	plurals := map[string]string{
		"Deployment": "deployments", "StatefulSet": "statefulsets", "DaemonSet": "daemonsets",
		"Job": "jobs", "CronJob": "cronjobs", "Pod": "pods", "Service": "services",
	}
	var footprint []string
	for _, resource := range resources {
		path := fmt.Sprintf("/api/v1/clusters/%s/k8s/%s/namespaces/%s/%s?labelSelector=%s",
			url.PathEscape(clusterID), resource.apiPath, url.PathEscape(spec.Namespace), plurals[resource.kind],
			url.QueryEscape("app.kubernetes.io/instance="+spec.ReleaseName))
		response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, nil, "", http.StatusOK, http.StatusNotFound)
		if err != nil {
			return nil, err
		}
		if response.Status == http.StatusNotFound {
			continue
		}
		items, err := arrayAtPath(response.Body, "items")
		if err != nil {
			return nil, fmt.Errorf("decode %s footprint: %w", resource.kind, err)
		}
		for _, item := range items {
			object, _ := item.(map[string]any)
			metadata, _ := object["metadata"].(map[string]any)
			name := stringField(metadata, "name")
			if name != "" {
				footprint = append(footprint, resource.kind+"/"+name)
			}
		}
	}
	sort.Strings(footprint)
	return footprint, nil
}

func waitAppReleaseFootprintAbsent(ctx context.Context, client *http.Client, execution executionContext, clusterID string, spec appInstallSpec, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("application cleanup polling interval must be positive")
	}
	for {
		footprint, err := appReleaseFootprint(ctx, client, execution, clusterID, spec)
		if err != nil {
			return err
		}
		if len(footprint) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("release %s/%s still owns %s: %w", spec.Namespace, spec.ReleaseName, strings.Join(footprint, ", "), ctx.Err())
		case <-time.After(interval):
		}
	}
}

func evaluateTextHealthCanary(ctx context.Context, client *http.Client, execution executionContext, slug, clusterID, namespace string, probes []serviceProbe, marker string) dimensionResult {
	var failures []string
	for _, probe := range probes {
		path := fmt.Sprintf("/api/v1/clusters/%s/k8s/api/v1/namespaces/%s/services/http:%s:%d/proxy%s", url.PathEscape(clusterID), url.PathEscape(namespace), url.PathEscape(probe.Service), probe.Port, probe.Path)
		response, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, http.StatusOK)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if marker != "" && !strings.Contains(strings.ToLower(string(response.Body)), strings.ToLower(marker)) {
			failures = append(failures, "response lacked "+marker)
			continue
		}
		now := time.Now().UTC()
		return dimensionResult{Name: "functional_canary", State: "PASS", Reason: slug + " readiness API answered through the Kubernetes service proxy", ObservedAt: now, HTTPStatus: response.Status, ArtifactSHA: digest(response.Body), SampleAt: &now, TargetClusterID: clusterID}
	}
	return failedCanary(clusterID, fmt.Errorf("%s service API was unavailable: %s", slug, strings.Join(failures, "; ")))
}

func evaluateExternalSecretsCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, phase string) (result dimensionResult) {
	namespace := memberNamespace(execution.Config, clusterID)
	name := "astr-qual-eso-" + digest([]byte(execution.RunID + phase))[:10]
	base := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/apis/external-secrets.io/v1/namespaces/" + url.PathEscape(namespace)
	secretPath := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/api/v1/namespaces/" + url.PathEscape(namespace) + "/secrets/" + name
	storePath, externalPath := base+"/secretstores/"+name, base+"/externalsecrets/"+name
	storeCreated, externalCreated := false, false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, item := range []struct {
			created bool
			path    string
		}{{externalCreated, externalPath}, {storeCreated, storePath}, {externalCreated, secretPath}} {
			if item.created {
				_, _ = requestAPI(cleanupCtx, client, execution.Base, execution.Token, http.MethodDelete, item.path, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound)
			}
		}
	}()
	marker := "external-secrets-" + name
	store := map[string]any{"apiVersion": "external-secrets.io/v1", "kind": "SecretStore", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"provider": map[string]any{"fake": map[string]any{"data": []any{map[string]any{"key": "qualification", "value": marker, "version": "v1"}}}}}}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, base+"/secretstores", store, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create fake SecretStore: %w", err))
	}
	storeCreated = true
	external := map[string]any{"apiVersion": "external-secrets.io/v1", "kind": "ExternalSecret", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"refreshInterval": "5s", "secretStoreRef": map[string]any{"name": name, "kind": "SecretStore"}, "target": map[string]any{"name": name, "creationPolicy": "Owner"}, "data": []any{map[string]any{"secretKey": "value", "remoteRef": map[string]any{"key": "qualification"}}}}}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, base+"/externalsecrets", external, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create ExternalSecret: %w", err))
	}
	externalCreated = true
	for {
		response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, secretPath, nil, "", http.StatusOK, http.StatusNotFound)
		if err != nil {
			return failedCanary(clusterID, err)
		}
		if response.Status == http.StatusOK {
			secret, _ := response.Body.(map[string]any)
			data, _ := secret["data"].(map[string]any)
			decoded, decodeErr := base64.StdEncoding.DecodeString(stringField(data, "value"))
			if decodeErr == nil && string(decoded) == marker {
				raw, _ := json.Marshal(response.Body)
				now := time.Now().UTC()
				return dimensionResult{Name: "functional_canary", State: "PASS", Reason: "External Secrets reconciled a fake provider value into a Kubernetes Secret", ObservedAt: now, HTTPStatus: http.StatusOK, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
			}
		}
		select {
		case <-ctx.Done():
			return failedCanary(clusterID, fmt.Errorf("ExternalSecret did not materialize: %w", ctx.Err()))
		case <-time.After(2 * time.Second):
		}
	}
}

func evaluateKyvernoCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, phase string) (result dimensionResult) {
	namespace := memberNamespace(execution.Config, clusterID)
	name := "astr-qual-kyverno-" + digest([]byte(execution.RunID + phase))[:10]
	base := "/api/v1/clusters/" + url.PathEscape(clusterID)
	policyPath := base + "/k8s/apis/kyverno.io/v1/namespaces/" + url.PathEscape(namespace) + "/policies/" + name
	podPath := base + "/k8s/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + name
	created := false
	defer func() {
		if created {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			_, _ = requestAPI(cleanupCtx, client, execution.Base, execution.Token, http.MethodDelete, policyPath, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound)
		}
	}()
	policy := map[string]any{"apiVersion": "kyverno.io/v1", "kind": "Policy", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"validationFailureAction": "Enforce", "background": false, "rules": []any{map[string]any{"name": "require-qualification-label", "match": map[string]any{"any": []any{map[string]any{"resources": map[string]any{"kinds": []any{"Pod"}, "names": []any{name}}}}}, "validate": map[string]any{"failureAction": "Enforce", "message": "qualification label required", "pattern": map[string]any{"metadata": map[string]any{"labels": map[string]any{"astronomer.io/qualification": "true"}}}}}}}}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, base+"/k8s/apis/kyverno.io/v1/namespaces/"+url.PathEscape(namespace)+"/policies", policy, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create Kyverno policy: %w", err))
	}
	created = true
	time.Sleep(2 * time.Second)
	pod := map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"containers": []any{map[string]any{"name": "canary", "image": "busybox:1.36", "command": []any{"sh", "-c", "sleep 5"}}}, "restartPolicy": "Never"}}
	denied, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, base+"/k8s/api/v1/namespaces/"+url.PathEscape(namespace)+"/pods", pod, "", http.StatusForbidden)
	if err != nil || !responseMentions(denied.Body, "qualification label required") {
		if err == nil {
			err = errors.New("Kyverno did not deny the violating pod")
		}
		return failedCanary(clusterID, err)
	}
	metadata := pod["metadata"].(map[string]any)
	metadata["labels"] = map[string]any{"astronomer.io/qualification": "true"}
	if _, err = requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, base+"/k8s/api/v1/namespaces/"+url.PathEscape(namespace)+"/pods", pod, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("Kyverno rejected compliant pod: %w", err))
	}
	_, _ = requestAPI(context.Background(), client, execution.Base, execution.Token, http.MethodDelete, podPath, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound)
	raw, _ := json.Marshal(denied.Body)
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: "Kyverno denied a violating pod and admitted the labeled equivalent", ObservedAt: now, HTTPStatus: http.StatusForbidden, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
}

func evaluateExternalDNSCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, namespace, phase string) (result dimensionResult) {
	workNS := memberNamespace(execution.Config, clusterID)
	name := "astr-qual-dns-" + digest([]byte(execution.RunID + phase))[:10]
	servicePath := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/api/v1/namespaces/" + url.PathEscape(workNS) + "/services/" + name
	created := false
	defer func() {
		if created {
			_, _ = requestAPI(context.Background(), client, execution.Base, execution.Token, http.MethodDelete, servicePath, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound)
		}
	}()
	service := map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": name, "namespace": workNS, "annotations": map[string]any{"external-dns.alpha.kubernetes.io/hostname": name + ".example.test"}}, "spec": map[string]any{"selector": map[string]any{"app": "does-not-exist"}, "ports": []any{map[string]any{"port": 80, "targetPort": 8080}}}}
	base := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/api/v1/namespaces/" + url.PathEscape(workNS) + "/services"
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, base, service, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, err)
	}
	created = true
	metricPath := fmt.Sprintf("/api/v1/clusters/%s/k8s/api/v1/namespaces/%s/services/http:external-dns:7979/proxy/metrics", url.PathEscape(clusterID), url.PathEscape(namespace))
	for {
		response, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, metricPath, http.StatusOK)
		if err == nil && prometheusMetricHasPositiveSample(response.Body, "external_dns_source_endpoints_total") {
			now := time.Now().UTC()
			return dimensionResult{Name: "functional_canary", State: "PASS", Reason: "ExternalDNS discovered the annotated Service and published source endpoint metrics using the in-memory provider", ObservedAt: now, HTTPStatus: response.Status, ArtifactSHA: digest(response.Body), SampleAt: &now, TargetClusterID: clusterID}
		}
		select {
		case <-ctx.Done():
			return failedCanary(clusterID, fmt.Errorf("ExternalDNS did not discover the canary Service: %w", ctx.Err()))
		case <-time.After(2 * time.Second):
		}
	}
}

func evaluateOTelCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, namespace, release string) dimensionResult {
	service := release + "-opentelemetry-collector"
	traceID := digest([]byte(execution.RunID + time.Now().UTC().String()))[:32]
	body := map[string]any{"resourceSpans": []any{map[string]any{"resource": map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "astronomer-qualification"}}}}, "scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "astronomer-qualification"}, "spans": []any{map[string]any{"traceId": traceID, "spanId": traceID[:16], "name": "qualification", "kind": 1, "startTimeUnixNano": "1", "endTimeUnixNano": "2"}}}}}}}
	path := fmt.Sprintf("/api/v1/clusters/%s/k8s/api/v1/namespaces/%s/services/http:%s:4318/proxy/v1/traces", url.PathEscape(clusterID), url.PathEscape(namespace), url.PathEscape(service))
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, path, body, "", http.StatusOK); err != nil {
		return failedCanary(clusterID, fmt.Errorf("send OTLP trace: %w", err))
	}
	metricsPath := fmt.Sprintf("/api/v1/clusters/%s/k8s/api/v1/namespaces/%s/services/http:%s:8888/proxy/metrics", url.PathEscape(clusterID), url.PathEscape(namespace), url.PathEscape(service))
	response, err := requestRawAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, metricsPath, http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, fmt.Errorf("scrape collector telemetry: %w", err))
	}
	if !prometheusMetricHasPositiveSample(response.Body, "otelcol_receiver_accepted_spans") {
		return failedCanary(clusterID, errors.New("collector did not report an accepted span"))
	}
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: "OpenTelemetry Collector accepted an OTLP trace and reported it in internal telemetry", ObservedAt: now, HTTPStatus: http.StatusOK, ArtifactSHA: digest(response.Body), SampleAt: &now, TargetClusterID: clusterID}
}

func evaluateVeleroNodeAgentCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, namespace string) dimensionResult {
	path := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods"
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, nil, "", http.StatusOK)
	if err != nil {
		return failedCanary(clusterID, err)
	}
	items, _ := arrayAtPath(response.Body, "items")
	ready := 0
	for _, item := range items {
		pod, _ := item.(map[string]any)
		meta, _ := pod["metadata"].(map[string]any)
		if strings.Contains(stringField(meta, "name"), "node-agent") && k8sPodReady(pod) {
			ready++
		}
	}
	if ready == 0 {
		return failedCanary(clusterID, errors.New("Velero node-agent had no Ready pod"))
	}
	raw, _ := json.Marshal(response.Body)
	now := time.Now().UTC()
	return dimensionResult{Name: "functional_canary", State: "PASS", Reason: fmt.Sprintf("Velero installed its local node-agent on %d nodes with backup storage intentionally disabled", ready), ObservedAt: now, HTTPStatus: response.Status, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
}

func evaluateAppRestartRecovery(ctx context.Context, client *http.Client, execution executionContext, slug, clusterID string, spec appInstallSpec) dimensionResult {
	target, err := discoverAppWorkload(ctx, client, execution, clusterID, spec.Namespace, spec.ReleaseName)
	if err != nil {
		return failedRestart(clusterID, err)
	}
	path := fmt.Sprintf("/api/v1/clusters/%s/workloads/%s/%s/%s/restart/", url.PathEscape(clusterID), url.PathEscape(target.Kind), url.PathEscape(target.Namespace), url.PathEscape(target.Name))
	restart, key, err := executeToolMutation(ctx, client, execution, http.MethodPost, path, nil, slug+"-app-restart-recovery")
	if err != nil {
		return failedRestart(clusterID, err)
	}
	canary := evaluateAppCanary(ctx, client, execution, slug, clusterID, spec, "post-restart")
	if canary.State != "PASS" {
		return failedRestart(clusterID, fmt.Errorf("post-restart canary failed: %s", canary.Reason))
	}
	return dimensionResult{Name: "restart_recovery", State: "PASS", Reason: "API restart completed and post-restart canary passed: " + canary.Reason, ObservedAt: time.Now().UTC(), HTTPStatus: http.StatusAccepted, OperationID: restart.ID, ArtifactSHA: canary.ArtifactSHA, IdempotencyKey: key, SampleAt: canary.SampleAt, TargetClusterID: clusterID}
}

func failedRestart(clusterID string, err error) dimensionResult {
	result := failedDimension("restart_recovery", err)
	result.TargetClusterID = clusterID
	return result
}

func discoverAppWorkload(ctx context.Context, client *http.Client, execution executionContext, clusterID, namespace, release string) (toolRestartTarget, error) {
	for _, resource := range []struct{ path, kind string }{{"deployments", "Deployment"}, {"statefulsets", "StatefulSet"}, {"daemonsets", "DaemonSet"}} {
		path := fmt.Sprintf("/api/v1/clusters/%s/k8s/apis/apps/v1/namespaces/%s/%s", url.PathEscape(clusterID), url.PathEscape(namespace), resource.path)
		response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, nil, "", http.StatusOK)
		if err != nil {
			continue
		}
		items, _ := arrayAtPath(response.Body, "items")
		for _, item := range items {
			object, _ := item.(map[string]any)
			meta, _ := object["metadata"].(map[string]any)
			labels, _ := meta["labels"].(map[string]any)
			if stringField(labels, "app.kubernetes.io/instance") == release {
				return toolRestartTarget{Kind: resource.kind, Namespace: namespace, Name: stringField(meta, "name")}, nil
			}
		}
	}
	return toolRestartTarget{}, fmt.Errorf("no restartable workload owned by release %s/%s was found", namespace, release)
}
