package deploy

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestApplicationMetricsRenderContract(t *testing.T) {
	for _, fullname := range []string{"astronomer", "custom-control"} {
		t.Run(fullname, func(t *testing.T) {
			docs := parseRenderedDocs(t, helmTemplateWithValueFilesAndFlags(t, nil, []string{"--namespace", "control"},
				"fullnameOverride="+fullname, "metrics.serviceMonitor.enabled=true",
				"metrics.serviceMonitor.namespace=monitoring", "metrics.serviceMonitor.labels.release=observability",
				"metrics.prometheusRule.labels.release=observability"))
			rule := findRenderedDoc(t, docs, "PrometheusRule", fullname)
			if stringAt(rule, "metadata", "labels", "release") != "observability" {
				t.Fatal("rule discovery label missing")
			}
			alerts := applicationMetricsAlerts(t, rule)
			for _, component := range []string{"server", "worker"} {
				monitor := findRenderedDoc(t, docs, "ServiceMonitor", fullname+"-"+component)
				serviceName := fullname + "-" + component + "-metrics"
				service := findRenderedDoc(t, docs, "Service", serviceName)
				if stringAt(monitor, "metadata", "namespace") != "monitoring" || stringAt(monitor, "metadata", "labels", "release") != "observability" {
					t.Fatal("cross-namespace monitor discovery metadata lost")
				}
				spec := nestedMap(monitor, "spec")
				if !reflect.DeepEqual(nestedMap(spec, "namespaceSelector")["matchNames"], []any{"control"}) {
					t.Fatalf("wrong monitored namespaces: %#v", spec)
				}
				selector := nestedMap(spec, "selector", "matchLabels")
				if len(selector) == 0 {
					t.Fatal("monitor selector is empty")
				}
				for key, value := range selector {
					if nestedMap(service, "metadata", "labels")[key] != value {
						t.Fatalf("monitor does not select metrics Service: %s", key)
					}
				}
				endpoint := spec["endpoints"].([]any)[0].(map[string]any)
				if endpoint["port"] != "metrics" || endpoint["path"] != "/metrics" {
					t.Fatalf("wrong scrape endpoint: %#v", endpoint)
				}
				alert := alerts[component]
				want := fmt.Sprintf(`(max(up{namespace="control",service=%q}) or vector(0)) == 0`, serviceName)
				if strings.TrimSpace(stringValue(alert["expr"])) != want || alert["for"] != "5m" {
					t.Fatalf("wrong expression/duration: %#v", alert)
				}
				labels := nestedMap(alert, "labels")
				if labels["namespace"] != "control" || labels["service"] != serviceName || labels["severity"] != "warning" {
					t.Fatalf("wrong alert routing labels: %#v", labels)
				}
				if !strings.HasSuffix(stringAt(alert, "annotations", "runbook_url"), "/application-metrics-unavailable.md") {
					t.Fatal("missing runbook")
				}
			}
		})
	}
}

func TestApplicationMetricsOptOut(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sets     []string
		monitors bool
	}{
		{name: "base"},
		{name: "rules-disabled", sets: []string{"metrics.serviceMonitor.enabled=true", "metrics.prometheusRule.enabled=false"}, monitors: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := parseRenderedDocs(t, helmTemplate(t, tc.sets...))
			for _, doc := range docs {
				if doc["kind"] == "PrometheusRule" {
					t.Fatal("opt-out rendered rules")
				}
			}
			for _, component := range []string{"server", "worker"} {
				if renderedDocExists(docs, "ServiceMonitor", "astronomer-"+component) != tc.monitors {
					t.Fatalf("unexpected %s monitor presence", component)
				}
			}
		})
	}
}

func applicationMetricsAlerts(t *testing.T, rule renderedDoc) map[string]map[string]any {
	t.Helper()
	alerts := map[string]map[string]any{}
	for _, group := range nestedMap(rule, "spec")["groups"].([]any) {
		for _, raw := range group.(map[string]any)["rules"].([]any) {
			item := raw.(map[string]any)
			if item["alert"] != "AstronomerApplicationMetricsUnavailable" {
				continue
			}
			component := stringAt(item, "labels", "component")
			if alerts[component] != nil {
				t.Fatalf("duplicate metrics alert for %s", component)
			}
			alerts[component] = item
		}
	}
	if len(alerts) != 2 || alerts["server"] == nil || alerts["worker"] == nil {
		t.Fatalf("missing per-component metrics alerts: %#v", alerts)
	}
	return alerts
}
