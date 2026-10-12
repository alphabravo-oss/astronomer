package deploy

import (
	"fmt"
	"strings"
	"testing"
)

func TestTraceExportRuleUsesRenderedMetricsServiceLabels(t *testing.T) {
	for _, fullname := range []string{"astronomer", "custom-control"} {
		t.Run(fullname, func(t *testing.T) {
			docs := parseRenderedDocs(t, helmTemplateWithValueFilesAndFlags(t, nil, []string{"--namespace", "control"}, "fullnameOverride="+fullname, "metrics.serviceMonitor.enabled=true"))
			rule := findRenderedDoc(t, docs, "PrometheusRule", fullname)
			var alert map[string]any
			for _, group := range nestedMap(rule, "spec")["groups"].([]any) {
				for _, raw := range group.(map[string]any)["rules"].([]any) {
					item := raw.(map[string]any)
					if item["alert"] == "AstronomerTraceExportFailing" {
						if alert != nil {
							t.Fatal("duplicate trace export alert")
						}
						alert = item
					}
				}
			}
			if alert == nil {
				t.Fatal("missing trace export failure alert")
			}
			want := fmt.Sprintf(`sum by (namespace,service,astronomer_instance_id) (rate(astronomer_tracing_exports_total{namespace="control",result="failure",service=~%q}[5m])) > 0`, fullname+"-(server|worker)-metrics")
			if strings.TrimSpace(stringValue(alert["expr"])) != want || alert["for"] != "5m" {
				t.Fatalf("wrong export failure expression: %#v", alert)
			}
			labels := nestedMap(alert, "labels")
			if len(labels) != 2 || labels["severity"] != "warning" || labels["component"] != "tracing" {
				t.Fatalf("unexpected diagnostic routing labels: %#v", labels)
			}
			if !strings.HasSuffix(stringAt(alert, "annotations", "runbook_url"), "/trace-export-failing.md") {
				t.Fatal("missing trace export runbook")
			}
			for _, component := range []string{"server", "worker"} {
				monitor := findRenderedDoc(t, docs, "ServiceMonitor", fullname+"-"+component)
				service := findRenderedDoc(t, docs, "Service", fullname+"-"+component+"-metrics")
				for key, value := range nestedMap(monitor, "spec", "selector", "matchLabels") {
					if nestedMap(service, "metadata", "labels")[key] != value {
						t.Fatalf("export alert service is not scraped: %s", key)
					}
				}
			}
		})
	}
}
