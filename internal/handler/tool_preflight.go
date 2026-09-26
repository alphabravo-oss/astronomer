package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

type toolPreflightCheck struct {
	Code    string `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (h *ToolHandler) toolPreflightChecks(ctx context.Context, slug, clusterID string, plan []toolRelease, allowedRelease string) []toolPreflightCheck {
	if slug == "fluent-bit" {
		return fluentBitOutputChecks(plan)
	}
	if slug != "prometheus-node-exporter" {
		return []toolPreflightCheck{}
	}
	hostNetwork, hostPort := nodeExporterNetworkSettings(plan)
	if !hostNetwork {
		return []toolPreflightCheck{{Code: "host-port", Status: "pass", Message: "Host networking is disabled, so Node Exporter will not reserve a node host port."}}
	}
	code := "host-port-" + strconv.Itoa(hostPort)
	if h.k8s == nil {
		return []toolPreflightCheck{{Code: code, Status: "warn", Message: fmt.Sprintf("Host port %d availability could not be checked because the Kubernetes requester is unavailable.", hostPort)}}
	}
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodGet, "/api/v1/pods", nil, requestHeaders(""))
	if err != nil || ensureSuccess(resp) != nil {
		return []toolPreflightCheck{{Code: code, Status: "warn", Message: fmt.Sprintf("Host port %d availability could not be checked. Review existing node exporters before installing.", hostPort)}}
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string            `json:"name"`
				Namespace string            `json:"namespace"`
				Labels    map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Ports []struct {
						HostPort int `json:"hostPort"`
					} `json:"ports"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := parseJSONResponse(resp, &list); err != nil {
		return []toolPreflightCheck{{Code: code, Status: "warn", Message: fmt.Sprintf("Host port %d availability returned an unreadable Kubernetes response.", hostPort)}}
	}
	var owners []string
	for _, pod := range list.Items {
		if allowedRelease != "" && pod.Metadata.Labels["app.kubernetes.io/instance"] == allowedRelease {
			continue
		}
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.HostPort == hostPort {
					owners = append(owners, pod.Metadata.Namespace+"/"+pod.Metadata.Name)
				}
			}
		}
	}
	if len(owners) > 0 {
		return []toolPreflightCheck{{
			Code: code, Status: "block",
			Message: fmt.Sprintf("Host port %d is already used by %s. Choose an available port, disable host networking, or use the existing exporter.", hostPort, strings.Join(owners, ", ")),
		}}
	}
	return []toolPreflightCheck{{Code: code, Status: "pass", Message: fmt.Sprintf("Host port %d is available on the currently scheduled pods.", hostPort)}}
}

func fluentBitOutputChecks(plan []toolRelease) []toolPreflightCheck {
	var values struct {
		ExistingConfigMap string `json:"existingConfigMap"`
		Config            struct {
			Outputs string `json:"outputs"`
		} `json:"config"`
	}
	if len(plan) > 0 && strings.TrimSpace(plan[0].ValuesYAML) != "" {
		_ = yaml.Unmarshal([]byte(plan[0].ValuesYAML), &values)
	}
	if strings.TrimSpace(values.ExistingConfigMap) != "" {
		return []toolPreflightCheck{{Code: "log-output", Status: "pass", Message: fmt.Sprintf("Fluent Bit will load its pipeline from ConfigMap %q.", values.ExistingConfigMap)}}
	}
	if strings.TrimSpace(values.Config.Outputs) != "" {
		return []toolPreflightCheck{{Code: "log-output", Status: "pass", Message: "Fluent Bit has an explicit output pipeline. Functional validation must still confirm that the destination receives a canary log."}}
	}
	return []toolPreflightCheck{{
		Code: "log-output", Status: "block",
		Message: "Configure a reachable Fluent Bit output or an existingConfigMap before installing. The upstream chart's example Elasticsearch destination is not a safe operational default.",
	}}
}

func nodeExporterNetworkSettings(plan []toolRelease) (bool, int) {
	hostNetwork, port := true, 9100
	if len(plan) == 0 || strings.TrimSpace(plan[0].ValuesYAML) == "" {
		return hostNetwork, port
	}
	var values struct {
		HostNetwork *bool `json:"hostNetwork"`
		Service     struct {
			Port int `json:"port"`
		} `json:"service"`
	}
	if err := yaml.Unmarshal([]byte(plan[0].ValuesYAML), &values); err != nil {
		return hostNetwork, port
	}
	if values.HostNetwork != nil {
		hostNetwork = *values.HostNetwork
	}
	if values.Service.Port > 0 {
		port = values.Service.Port
	}
	return hostNetwork, port
}

func blockingToolPreflight(checks []toolPreflightCheck) string {
	for _, check := range checks {
		if check.Status == "block" {
			return check.Message
		}
	}
	return ""
}
