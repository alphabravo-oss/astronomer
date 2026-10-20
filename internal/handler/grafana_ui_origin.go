package handler

import (
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/toolui"
)

func (h *MonitoringHandler) SetToolUIDomain(domain string) { h.toolUIDomain = domain }

func (h *MonitoringHandler) grafanaUIURL(clusterID string) string {
	if h == nil {
		return sharedGrafanaProxyPath
	}
	target := toolui.Target{Kind: "grafana"}
	if clusterID != "" {
		target = toolui.Target{Kind: "cluster-grafana", Cluster: clusterID}
	}
	if h.toolUIDomain == "" {
		return strings.TrimRight(h.serverURL, "/") + target.Prefix()
	}
	return "https://" + toolui.Host(h.toolUIDomain, target) + target.Prefix()
}
