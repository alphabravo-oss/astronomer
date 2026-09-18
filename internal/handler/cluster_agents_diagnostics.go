package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
)

func (h *ClusterAgentHandler) collectLiveDiagnostics(ctx context.Context, clusterID string, now time.Time) agentLiveDiagnostics {
	out := agentLiveDiagnostics{
		CollectedAt: now.UTC().Format(time.RFC3339),
		Discovery:   map[string]any{},
	}
	if h == nil || h.requester == nil {
		out.Errors = append(out.Errors, "kubernetes requester is not configured")
		return out
	}
	liveCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	deploymentPath := "/apis/apps/v1/namespaces/astronomer-system/deployments/astronomer-agent"
	var deployment map[string]any
	if err := h.getLiveJSON(liveCtx, clusterID, deploymentPath, &deployment); err != nil {
		out.Errors = append(out.Errors, "deployment: "+err.Error())
	} else {
		out.Deployment = summarizeAgentDeployment(deployment)
	}

	labelSelector := url.QueryEscape("app.kubernetes.io/name=astronomer-agent,app.kubernetes.io/component=agent")
	var podList struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				NodeName   string `json:"nodeName"`
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Ready        bool  `json:"ready"`
					RestartCount int32 `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := h.getLiveJSON(liveCtx, clusterID, "/api/v1/namespaces/astronomer-system/pods?labelSelector="+labelSelector, &podList); err != nil {
		out.Errors = append(out.Errors, "pods: "+err.Error())
	} else {
		for _, pod := range podList.Items {
			entry := agentLivePod{
				Name:      pod.Metadata.Name,
				Namespace: pod.Metadata.Namespace,
				Phase:     pod.Status.Phase,
				NodeName:  pod.Spec.NodeName,
				Ready:     len(pod.Status.ContainerStatuses) > 0,
			}
			for _, container := range pod.Spec.Containers {
				if container.Image != "" {
					entry.ContainerImages = append(entry.ContainerImages, container.Image)
				}
			}
			for _, status := range pod.Status.ContainerStatuses {
				entry.Ready = entry.Ready && status.Ready
				entry.RestartCount += status.RestartCount
			}
			out.Pods = append(out.Pods, entry)
			if len(out.Logs) < 3 && pod.Metadata.Name != "" {
				logs, truncated, err := h.getLivePodLogs(liveCtx, clusterID, pod.Metadata.Name)
				if err != nil {
					out.Errors = append(out.Errors, "logs/"+pod.Metadata.Name+": "+err.Error())
				} else {
					out.Logs = append(out.Logs, agentLiveLog{PodName: pod.Metadata.Name, Lines: logs, Truncated: truncated})
				}
			}
		}
	}

	var eventList struct {
		Items []struct {
			Type           string `json:"type"`
			Reason         string `json:"reason"`
			Message        string `json:"message"`
			EventTime      string `json:"eventTime"`
			LastTimestamp  string `json:"lastTimestamp"`
			FirstTimestamp string `json:"firstTimestamp"`
		} `json:"items"`
	}
	if err := h.getLiveJSON(liveCtx, clusterID, "/api/v1/namespaces/astronomer-system/events?fieldSelector="+url.QueryEscape("involvedObject.name=astronomer-agent"), &eventList); err != nil {
		out.Errors = append(out.Errors, "events: "+err.Error())
	} else {
		for i, event := range eventList.Items {
			if i >= 20 {
				break
			}
			out.Events = append(out.Events, agentLiveEvent{
				Type:    event.Type,
				Reason:  event.Reason,
				Message: event.Message,
				Time:    firstNonEmptyAgentValue(event.EventTime, event.LastTimestamp, event.FirstTimestamp),
			})
		}
	}

	var version map[string]any
	versionBody, versionHeaders, err := h.getLiveRaw(liveCtx, clusterID, "/version")
	if err != nil {
		out.Errors = append(out.Errors, "version: "+err.Error())
	} else {
		if err := json.Unmarshal(versionBody, &version); err != nil {
			out.Errors = append(out.Errors, "version: "+err.Error())
		} else {
			out.Discovery["version"] = version
		}
		out.Checks = append(out.Checks, agentClockSkewDiagnosticCheck(versionHeaders, now))
	}
	var apis map[string]any
	if err := h.getLiveJSON(liveCtx, clusterID, "/apis", &apis); err != nil {
		out.Errors = append(out.Errors, "apis: "+err.Error())
	} else {
		out.Discovery["apis"] = summarizeAPIResourceList(apis)
	}
	out.Checks = append(out.Checks,
		h.collectRBACSelfReview(liveCtx, clusterID),
		h.collectKubernetesReadyzCheck(liveCtx, clusterID),
	)
	return out
}

func (h *ClusterAgentHandler) collectRBACSelfReview(ctx context.Context, clusterID string) agentSelfTestCheck {
	body := map[string]any{
		"apiVersion": "authorization.k8s.io/v1",
		"kind":       "SelfSubjectAccessReview",
		"spec": map[string]any{
			"resourceAttributes": map[string]any{
				"namespace": "astronomer-system",
				"verb":      "get",
				"resource":  "pods",
			},
		},
	}
	var review struct {
		Status struct {
			Allowed         bool   `json:"allowed"`
			Denied          bool   `json:"denied"`
			Reason          string `json:"reason"`
			EvaluationError string `json:"evaluationError"`
		} `json:"status"`
	}
	if err := h.postLiveJSON(ctx, clusterID, "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", body, &review); err != nil {
		return agentSelfTestCheck{Name: "rbac_self_check", Status: "warning", Message: "Unable to run Kubernetes SelfSubjectAccessReview: " + err.Error()}
	}
	if review.Status.Allowed {
		return agentSelfTestCheck{Name: "rbac_self_check", Status: "passed", Message: "Agent can read its own pods in astronomer-system."}
	}
	if review.Status.Denied {
		return agentSelfTestCheck{Name: "rbac_self_check", Status: "failed", Message: firstNonEmptyAgentValue(review.Status.Reason, "Kubernetes denied the agent pod read self-check.")}
	}
	return agentSelfTestCheck{Name: "rbac_self_check", Status: "warning", Message: firstNonEmptyAgentValue(review.Status.EvaluationError, "Kubernetes returned an inconclusive SelfSubjectAccessReview.")}
}

func (h *ClusterAgentHandler) collectKubernetesReadyzCheck(ctx context.Context, clusterID string) agentSelfTestCheck {
	body, _, err := h.getLiveRaw(ctx, clusterID, "/readyz")
	if err != nil {
		return agentSelfTestCheck{Name: "network_readyz", Status: "warning", Message: "Kubernetes readyz check failed through the agent tunnel: " + err.Error()}
	}
	text := strings.TrimSpace(string(body))
	if text == "" || strings.EqualFold(text, "ok") {
		return agentSelfTestCheck{Name: "network_readyz", Status: "passed", Message: "Kubernetes API readyz succeeded through the agent tunnel."}
	}
	return agentSelfTestCheck{Name: "network_readyz", Status: "warning", Message: "Kubernetes API readyz returned: " + truncateDiagnosticMessage(text)}
}

func agentClockSkewDiagnosticCheck(headers map[string]string, now time.Time) agentSelfTestCheck {
	dateValue := firstHeaderValue(headers, "Date")
	if dateValue == "" {
		return agentSelfTestCheck{Name: "clock_skew", Status: "warning", Message: "Kubernetes API response did not include a Date header."}
	}
	remoteTime, err := http.ParseTime(dateValue)
	if err != nil {
		return agentSelfTestCheck{Name: "clock_skew", Status: "warning", Message: "Kubernetes API Date header could not be parsed."}
	}
	skew := now.Sub(remoteTime)
	if skew < 0 {
		skew = -skew
	}
	skewText := skew.Round(time.Second).String()
	switch {
	case skew > 5*time.Minute:
		return agentSelfTestCheck{Name: "clock_skew", Status: "failed", Message: "Management plane and Kubernetes API clocks differ by " + skewText + "."}
	case skew > 2*time.Minute:
		return agentSelfTestCheck{Name: "clock_skew", Status: "warning", Message: "Management plane and Kubernetes API clocks differ by " + skewText + "."}
	default:
		return agentSelfTestCheck{Name: "clock_skew", Status: "passed", Message: "Management plane and Kubernetes API clock skew is " + skewText + "."}
	}
}

// agentSelfManagementContext stamps the positive machine-origin marker on every
// live probe this handler makes. Each of them targets astronomer's OWN
// footprint — the astronomer-system Deployment/Pods/Events, the agent's pod
// logs, /version, /apis discovery, and the SelfSubjectAccessReview self-probe —
// which is design doc §10.2 (self-management into AstronomerOwnedNamespaces)
// and §10.5 (agent lifecycle and SSAR self-probes).
//
// A human triggers these by opening the fleet page, so an authenticated user IS
// in ctx. The explicit marker deliberately wins over that: these are astronomer
// acting on itself and must never be impersonated as the person who clicked. It
// is stamped in the three shared helpers rather than at each call site so a new
// probe cannot forget it.
func agentSelfManagementContext(ctx context.Context) context.Context {
	return callerid.WithMachine(ctx, callerid.SourceSelfManage)
}

func (h *ClusterAgentHandler) getLiveJSON(ctx context.Context, clusterID, path string, out any) error {
	body, _, err := h.getLiveRaw(ctx, clusterID, path)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (h *ClusterAgentHandler) getLiveRaw(ctx context.Context, clusterID, path string) ([]byte, map[string]string, error) {
	resp, err := h.requester.Do(agentSelfManagementContext(ctx), clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return nil, nil, err
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, resp.Headers, err
	}
	body, err := decodeResponseBody(resp)
	return body, resp.Headers, err
}

func (h *ClusterAgentHandler) postLiveJSON(ctx context.Context, clusterID, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := h.requester.Do(agentSelfManagementContext(ctx), clusterID, http.MethodPost, path, raw, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	return parseJSONResponse(resp, out)
}

func (h *ClusterAgentHandler) getLivePodLogs(ctx context.Context, clusterID, podName string) ([]string, bool, error) {
	path := "/api/v1/namespaces/astronomer-system/pods/" + url.PathEscape(podName) + "/log?tailLines=200&timestamps=true"
	raw, _, err := h.getLiveRaw(ctx, clusterID, path)
	if err != nil {
		return nil, false, err
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, false, nil
	}
	truncated := false
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
		truncated = true
	}
	for i, line := range lines {
		lines[i] = redaction.SensitiveLine(line)
	}
	return lines, truncated, nil
}

func summarizeAgentDeployment(raw map[string]any) map[string]any {
	return map[string]any{
		"name":               stringAt(raw, "metadata", "name"),
		"namespace":          stringAt(raw, "metadata", "namespace"),
		"generation":         rawAt(raw, "metadata", "generation"),
		"replicas":           rawAt(raw, "spec", "replicas"),
		"updated_replicas":   rawAt(raw, "status", "updatedReplicas"),
		"ready_replicas":     rawAt(raw, "status", "readyReplicas"),
		"available_replicas": rawAt(raw, "status", "availableReplicas"),
		"conditions":         rawAt(raw, "status", "conditions"),
	}
}

func summarizeAPIResourceList(raw map[string]any) map[string]any {
	groups, _ := raw["groups"].([]any)
	names := make([]string, 0, len(groups))
	for _, item := range groups {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := group["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return map[string]any{
		"group_count": len(names),
		"groups":      names,
	}
}

func rawAt(raw map[string]any, keys ...string) any {
	var cur any = raw
	for _, key := range keys {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = next[key]
	}
	return cur
}

func stringAt(raw map[string]any, keys ...string) string {
	value, _ := rawAt(raw, keys...).(string)
	return value
}

func firstHeaderValue(headers map[string]string, name string) string {
	if len(headers) == 0 {
		return ""
	}
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func truncateDiagnosticMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 200 {
		return value
	}
	return value[:200] + "...[truncated]"
}
