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

type toolLifecycleExecutor struct{ slug string }

var toolCases = map[string]string{
	"TOOL-01": "cis-operator", "TOOL-02": "dex", "TOOL-03": "fluent-bit",
	"TOOL-04": "trivy-operator", "TOOL-05": "cert-manager", "TOOL-06": "gatekeeper",
	"TOOL-07": "kube-state-metrics", "TOOL-08": "prometheus-node-exporter",
	"TOOL-09": "ingress-nginx", "TOOL-10": "longhorn", "TOOL-11": "neuvector",
	"TOOL-12": "istio",
}

func (executor toolLifecycleExecutor) Run(ctx context.Context, execution executionContext, definition caseDefinition, result caseResult, checkpoint checkpointFunc) (final caseResult) {
	if executor.slug == "dex" {
		return runMemberDexRejection(ctx, execution, result, checkpoint)
	}
	client := qualificationHTTPClient()
	target, err := toolTarget(execution.Config)
	if err != nil {
		return finishCase(result, "BLOCKED", err.Error(), checkpoint)
	}
	request := map[string]any{"cluster_id": target.ClusterID, "preset": "default"}
	basePath := "/api/v1/tools/" + url.PathEscape(executor.slug)

	statusResponse, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet,
		"/api/v1/clusters/"+url.PathEscape(target.ClusterID)+"/tools/status/", nil, "", http.StatusOK)
	if err != nil {
		return failDimension(result, "inventory", err, checkpoint)
	}
	status, found := findToolStatus(statusResponse.Body, executor.slug)
	if !found {
		return failDimension(result, "inventory", fmt.Errorf("tool %s was absent from cluster status", executor.slug), checkpoint)
	}
	if state, _ := status["status"].(string); state != "not_installed" {
		return finishCase(result, "BLOCKED", fmt.Sprintf("tool %s was not clean before the run (status %s)", executor.slug, state), checkpoint,
			dimensionResult{Name: "inventory", State: "BLOCKED", Reason: "the runner will not take ownership of a pre-existing release", ObservedAt: time.Now().UTC(), TargetClusterID: target.ClusterID})
	}
	result = addDimension(result, dimensionResult{Name: "inventory", State: "PASS", Reason: "tool is offered and no pre-existing release owns the target", ObservedAt: time.Now().UTC(), HTTPStatus: statusResponse.Status, TargetClusterID: target.ClusterID}, checkpoint)

	preview, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, basePath+"/preview/", request, "", http.StatusOK)
	if err != nil {
		return failDimension(result, "preview", err, checkpoint)
	}
	if err := validateToolPreview(preview.Body); err != nil {
		return failDimension(result, "preview", err, checkpoint)
	}
	result = addDimension(result, dimensionResult{Name: "preview", State: "PASS", Reason: "API resolved non-empty immutable chart coordinates", ObservedAt: time.Now().UTC(), HTTPStatus: preview.Status, TargetClusterID: target.ClusterID}, checkpoint)

	installed := false
	defer func() {
		if installed {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			operation, key, cleanupErr := executeToolMutation(cleanupCtx, client, execution, http.MethodDelete, basePath+"/uninstall", toolUninstallBody(executor.slug, target.ClusterID), definition.ID+"-deferred-cleanup")
			if cleanupErr != nil {
				final = upsertDimension(final, failedDimension("uninstall_cleanup", cleanupErr))
				final.State = "FAIL"
				final.Reason = "run-owned tool cleanup failed: " + cleanupErr.Error()
			} else {
				final = upsertDimension(final, operationDimension("uninstall_cleanup", "deferred cleanup removed the run-owned release", target.ClusterID, key, operation))
			}
			_ = checkpoint(final)
		}
	}()
	install, installKey, err := executeToolMutation(ctx, client, execution, http.MethodPost, basePath+"/install", request, definition.ID+"-install")
	if err != nil {
		return failDimension(result, "install", err, checkpoint)
	}
	installed = true
	result = addDimension(result, operationDimension("install", "tool installation completed", target.ClusterID, installKey, install), checkpoint)

	statusResponse, err = requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet,
		"/api/v1/clusters/"+url.PathEscape(target.ClusterID)+"/tools/status/", nil, "", http.StatusOK)
	status, found = findToolStatus(statusResponse.Body, executor.slug)
	if err != nil || !found || status["status"] != "installed" {
		if err == nil {
			err = fmt.Errorf("installed operation completed but status was %v", status["status"])
		}
		return failDimension(result, "reconciliation", err, checkpoint)
	}
	generation := int64(1)
	result = addDimension(result, dimensionResult{Name: "reconciliation", State: "PASS", Reason: "durable operation and cluster-scoped installed status agree", ObservedAt: time.Now().UTC(), OperationID: install.ID, TargetClusterID: target.ClusterID, DesiredGeneration: &generation, ObservedGeneration: &generation}, checkpoint)

	result = addDimension(result, evaluateToolCanary(ctx, client, execution, executor.slug, target.ClusterID, status), checkpoint)
	result = addDimension(result, evaluateToolIsolation(ctx, client, execution, executor.slug, target.ClusterID), checkpoint)

	upgrade, upgradeKey, err := executeToolMutation(ctx, client, execution, http.MethodPut, basePath+"/upgrade", request, definition.ID+"-upgrade")
	if err != nil {
		result = addDimension(result, failedDimension("upgrade", err), checkpoint)
	} else {
		result = addDimension(result, operationDimension("upgrade", "same pinned release was reconciled as a new Helm revision", target.ClusterID, upgradeKey, upgrade), checkpoint)
	}

	rollback, rollbackKey, err := executeToolMutation(ctx, client, execution, http.MethodPost, basePath+"/rollback", map[string]any{"cluster_id": target.ClusterID}, definition.ID+"-rollback")
	if err != nil {
		result = addDimension(result, failedDimension("rollback", err), checkpoint)
	} else {
		result = addDimension(result, operationDimension("rollback", "API restored the preceding run-owned Helm revision", target.ClusterID, rollbackKey, rollback), checkpoint)
	}

	result = addDimension(result, evaluateToolRestartRecovery(ctx, client, execution, executor.slug, target.ClusterID), checkpoint)
	uninstall, uninstallKey, err := executeToolMutation(ctx, client, execution, http.MethodDelete, basePath+"/uninstall", toolUninstallBody(executor.slug, target.ClusterID), definition.ID+"-uninstall")
	if err != nil {
		return finishCase(addDimension(result, failedDimension("uninstall_cleanup", err), checkpoint), "FAIL", "tool lifecycle left cleanup unproven", checkpoint)
	}
	installed = false
	statusResponse, statusErr := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet,
		"/api/v1/clusters/"+url.PathEscape(target.ClusterID)+"/tools/status/", nil, "", http.StatusOK)
	status, found = findToolStatus(statusResponse.Body, executor.slug)
	if statusErr != nil || !found || status["status"] != "not_installed" {
		if statusErr == nil {
			statusErr = fmt.Errorf("uninstall completed but status was %v", status["status"])
		}
		result = addDimension(result, failedDimension("uninstall_cleanup", statusErr), checkpoint)
	} else {
		result = addDimension(result, operationDimension("uninstall_cleanup", "run-owned releases were removed and status converged", target.ClusterID, uninstallKey, uninstall), checkpoint)
	}
	return finalizeDimensions(result, checkpoint)
}

func toolUninstallBody(slug, clusterID string) map[string]any {
	body := map[string]any{
		"cluster_id":                     clusterID,
		"confirm_failed_release_cleanup": true,
	}
	if slug == "longhorn" {
		body["confirm_data_deletion"] = true
	}
	return body
}

func toolTarget(config qualificationConfig) (memberTarget, error) {
	for _, target := range config.MemberTargets {
		if target.ExpectedPrivilegeProfile == "admin" {
			return target, nil
		}
	}
	return memberTarget{}, fmt.Errorf("no explicit admin-profile member target is configured")
}

func executeToolMutation(ctx context.Context, client *http.Client, execution executionContext, method, path string, body any, keySuffix string) (operationObservation, string, error) {
	key := "qualification-" + execution.RunID + "-" + strings.ToLower(keySuffix)
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, method, path, body, key, http.StatusAccepted)
	if err != nil {
		return operationObservation{}, key, err
	}
	operation, err := operationFromResponse(response)
	if err != nil {
		return operationObservation{}, key, err
	}
	location := response.Location
	if location == "" {
		location = "/api/v1/tools/operations/" + url.PathEscape(operation.ID)
	}
	completed, err := pollOperation(ctx, client, execution.Base, execution.Token, location, operation.ID, 2*time.Second)
	return completed, key, err
}

func validateToolPreview(body any) error {
	value, err := valueAtPath(body, "data.charts")
	if err != nil {
		return err
	}
	charts, ok := value.([]any)
	if !ok || len(charts) == 0 {
		return fmt.Errorf("preview contained no chart release")
	}
	for _, item := range charts {
		chart, ok := item.(map[string]any)
		if !ok || stringField(chart, "chart_name") == "" || stringField(chart, "chart_version") == "" || stringField(chart, "namespace") == "" {
			return fmt.Errorf("preview contained an incomplete or unpinned chart release")
		}
	}
	return nil
}

func findToolStatus(body any, slug string) (map[string]any, bool) {
	value, err := valueAtPath(body, "data")
	if err != nil {
		return nil, false
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if ok && stringField(object, "slug") == slug {
			return object, true
		}
	}
	return nil, false
}

func stringField(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return strings.TrimSpace(value)
}

func evaluateToolCanary(_ context.Context, _ *http.Client, _ executionContext, slug, clusterID string, status map[string]any) dimensionResult {
	now := time.Now().UTC()
	raw, _ := json.Marshal(status)
	return dimensionResult{Name: "functional_canary", State: "FAIL", Reason: fmt.Sprintf("%s installed, but its product-specific API canary has not produced evidence; release readiness is insufficient", slug), ObservedAt: now, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
}

func evaluateToolIsolation(ctx context.Context, client *http.Client, execution executionContext, slug, targetClusterID string) dimensionResult {
	for _, other := range execution.Config.MemberTargets {
		if other.ClusterID == targetClusterID {
			continue
		}
		response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, "/api/v1/clusters/"+url.PathEscape(other.ClusterID)+"/tools/status/", nil, "", http.StatusOK)
		if err != nil {
			return failedDimension("authorization_isolation", err)
		}
		status, found := findToolStatus(response.Body, slug)
		if !found || status["status"] != "not_installed" {
			return failedDimension("authorization_isolation", fmt.Errorf("tool effect appeared on non-target cluster %s", other.ClusterID))
		}
		return dimensionResult{Name: "authorization_isolation", State: "FAIL", Reason: "cross-cluster isolation passed, but no restricted qualification credential is configured to prove RBAC denial", ObservedAt: time.Now().UTC(), TargetClusterID: targetClusterID}
	}
	return failedDimension("authorization_isolation", fmt.Errorf("a second member target is required for cross-cluster isolation"))
}

func evaluateToolRestartRecovery(ctx context.Context, client *http.Client, execution executionContext, slug, clusterID string) dimensionResult {
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, "/api/v1/clusters/"+url.PathEscape(clusterID)+"/tools/status/", nil, "", http.StatusOK)
	if err != nil {
		return failedDimension("restart_recovery", err)
	}
	status, found := findToolStatus(response.Body, slug)
	if !found || status["status"] != "installed" {
		return failedDimension("restart_recovery", fmt.Errorf("tool did not remain installed before restart validation"))
	}
	return dimensionResult{Name: "restart_recovery", State: "FAIL", Reason: "durable state survived lifecycle operations, but no supported API restart was identified for this chart workload", ObservedAt: time.Now().UTC(), TargetClusterID: clusterID}
}

func operationDimension(name, reason, clusterID, key string, operation operationObservation) dimensionResult {
	return dimensionResult{Name: name, State: "PASS", Reason: reason, ObservedAt: time.Now().UTC(), HTTPStatus: http.StatusAccepted, OperationID: operation.ID, IdempotencyKey: key, TargetClusterID: clusterID}
}

func failedDimension(name string, err error) dimensionResult {
	return dimensionResult{Name: name, State: "FAIL", Reason: err.Error(), ObservedAt: time.Now().UTC()}
}

func addDimension(result caseResult, dimension dimensionResult, checkpoint checkpointFunc) caseResult {
	result.Dimensions = append(result.Dimensions, dimension)
	_ = checkpoint(result)
	return result
}

func upsertDimension(result caseResult, dimension dimensionResult) caseResult {
	for index := range result.Dimensions {
		if result.Dimensions[index].Name == dimension.Name {
			result.Dimensions[index] = dimension
			return result
		}
	}
	result.Dimensions = append(result.Dimensions, dimension)
	return result
}

func failDimension(result caseResult, name string, err error, checkpoint checkpointFunc) caseResult {
	result = addDimension(result, failedDimension(name, err), checkpoint)
	return finishCase(result, "FAIL", err.Error(), checkpoint)
}

func finishCase(result caseResult, state, reason string, checkpoint checkpointFunc, dimensions ...dimensionResult) caseResult {
	result.Dimensions = append(result.Dimensions, dimensions...)
	result.State, result.Reason = state, reason
	now := time.Now().UTC()
	result.CompletedAt = &now
	_ = checkpoint(result)
	return result
}

func finalizeDimensions(result caseResult, checkpoint checkpointFunc) caseResult {
	for _, dimension := range result.Dimensions {
		if dimension.State != "PASS" && dimension.State != "NOT_APPLICABLE" {
			return finishCase(result, "FAIL", "one or more required qualification dimensions did not pass", checkpoint)
		}
	}
	return finishCase(result, "PASS", "all required tool lifecycle dimensions passed", checkpoint)
}

func runMemberDexRejection(ctx context.Context, execution executionContext, result caseResult, checkpoint checkpointFunc) caseResult {
	target, err := toolTarget(execution.Config)
	if err != nil {
		return finishCase(result, "BLOCKED", err.Error(), checkpoint)
	}
	client := qualificationHTTPClient()
	body := map[string]any{"cluster_id": target.ClusterID, "preset": "in-cluster"}
	statusResponse, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet,
		"/api/v1/clusters/"+url.PathEscape(target.ClusterID)+"/tools/status/", nil, "", http.StatusOK)
	if err != nil {
		return failDimension(result, "inventory", err, checkpoint)
	}
	if _, found := findToolStatus(statusResponse.Body, "dex"); !found {
		return failDimension(result, "inventory", fmt.Errorf("Dex was absent from the Tool catalog"), checkpoint)
	}
	result = addDimension(result, dimensionResult{Name: "inventory", State: "PASS", Reason: "Dex is discoverable as a management-only Tool entrypoint", ObservedAt: time.Now().UTC(), HTTPStatus: statusResponse.Status, TargetClusterID: target.ClusterID}, checkpoint)
	for _, action := range []struct{ name, method, path string }{
		{"preview", http.MethodPost, "/api/v1/tools/dex/preview/"},
		{"install", http.MethodPost, "/api/v1/tools/dex/install"},
		{"upgrade", http.MethodPut, "/api/v1/tools/dex/upgrade"},
	} {
		response, requestErr := requestAPI(ctx, client, execution.Base, execution.Token, action.method, action.path, body, "qualification-"+execution.RunID+"-dex-"+action.name, http.StatusBadRequest)
		if requestErr != nil || !responseMentions(response.Body, "management chart") {
			if requestErr == nil {
				requestErr = fmt.Errorf("Dex %s rejection did not direct the caller to the management chart", action.name)
			}
			return failDimension(result, action.name, requestErr, checkpoint)
		}
		state := "PASS"
		if action.name != "preview" {
			state = "NOT_APPLICABLE"
		}
		result = addDimension(result, dimensionResult{Name: action.name, State: state, Reason: "member-cluster action was rejected with management-plane guidance", ObservedAt: time.Now().UTC(), HTTPStatus: response.Status, TargetClusterID: target.ClusterID}, checkpoint)
	}
	// This case proves the Tool entrypoint contract only. Management-plane Dex
	// lifecycle and login behavior are covered by the ID cases.
	for _, name := range []string{"reconciliation", "functional_canary", "rollback", "restart_recovery", "uninstall_cleanup"} {
		result = addDimension(result, dimensionResult{Name: name, State: "NOT_APPLICABLE", Reason: "member-cluster Dex has no release lifecycle; management-plane behavior belongs to the Auth qualification cases", ObservedAt: time.Now().UTC(), TargetClusterID: target.ClusterID}, checkpoint)
	}
	result = addDimension(result, dimensionResult{Name: "authorization_isolation", State: "PASS", Reason: "the Tool entrypoint rejected member-cluster Dex before creating a durable operation or release", ObservedAt: time.Now().UTC(), TargetClusterID: target.ClusterID}, checkpoint)
	return finalizeDimensions(result, checkpoint)
}

func responseMentions(body any, text string) bool {
	raw, _ := json.Marshal(body)
	return strings.Contains(strings.ToLower(string(raw)), strings.ToLower(text))
}
