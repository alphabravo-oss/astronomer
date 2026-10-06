package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationDashboardRenderScope(t *testing.T) {
	for _, tc := range []struct{ namespace, fullname string }{{"astronomer", "astronomer"}, {"custom-control", "custom-plane"}} {
		t.Run(tc.fullname, func(t *testing.T) {
			docs := parseRenderedDocs(t, helmTemplateWithValueFilesAndFlags(t, nil, []string{"--namespace", tc.namespace}, "fullnameOverride="+tc.fullname, "metrics.dashboards.enabled=true", "metrics.serviceMonitor.enabled=true", "metrics.serviceMonitor.namespace=monitoring"))
			cm := findRenderedDoc(t, docs, "ConfigMap", "astronomer-dashboard-management-plane")
			var dashboard struct {
				UID        string `json:"uid"`
				Templating struct {
					List []struct {
						Name, Query string
						Current     struct{ Text, Value string }
					}
				} `json:"templating"`
				Panels []struct {
					Title   string
					Targets []struct {
						Expr, LegendFormat string
						Instant            bool
					}
				} `json:"panels"`
			}
			if err := json.Unmarshal([]byte(stringAt(cm, "data", "management-plane.json")), &dashboard); err != nil {
				t.Fatal(err)
			}
			if dashboard.UID != "astronomer-management-plane" {
				t.Fatal("dashboard identity changed")
			}
			vars := map[string]string{}
			for _, v := range dashboard.Templating.List {
				if v.Query != v.Current.Value || v.Query != v.Current.Text {
					t.Fatalf("inconsistent variable: %#v", v)
				}
				vars[v.Name] = v.Query
			}
			if vars["metrics_namespace"] != tc.namespace || vars["metrics_fullname"] != tc.fullname {
				t.Fatalf("wrong dashboard scope: %#v", vars)
			}
			queries := map[string][]string{}
			for _, panel := range dashboard.Panels {
				for _, target := range panel.Targets {
					if panel.Title == "Agents disconnected" && !target.Instant {
						t.Fatal("disconnected stat can retain historical health")
					}
					queries[panel.Title] = append(queries[panel.Title], target.Expr)
				}
			}
			for _, component := range []string{"server", "worker"} {
				title := strings.ToUpper(component[:1]) + component[1:] + " scrape availability"
				q := queries[title]
				want := `max(up{namespace="${metrics_namespace}",service="${metrics_fullname}-` + component + `-metrics"})`
				if len(q) != 1 || q[0] != want {
					t.Fatalf("availability query %s: %#v", title, q)
				}
				service := findRenderedDoc(t, docs, "Service", tc.fullname+"-"+component+"-metrics")
				if namespace := stringAt(service, "metadata", "namespace"); namespace != "" && namespace != tc.namespace {
					t.Fatal("scope does not match metrics Service namespace")
				}
			}
			for title, metric := range map[string]string{"Process CPU usage (cores)": "process_cpu_seconds_total", "Go heap in use": "go_memstats_heap_inuse_bytes", "Go goroutines": "go_goroutines"} {
				if len(queries[title]) != 2 {
					t.Fatalf("missing per-component %s queries", title)
				}
				for i, component := range []string{"server", "worker"} {
					selector := `{namespace="${metrics_namespace}",service="${metrics_fullname}-` + component + `-metrics"}`
					want := metric + selector
					if metric == "process_cpu_seconds_total" {
						want = "rate(" + want + "[5m])"
					}
					want += " and on(namespace, service, instance) (up" + selector + " == 1)"
					if queries[title][i] != want {
						t.Fatalf("incorrect resource query: %s", queries[title][i])
					}
				}
			}
			disconnected := queries["Agents disconnected"]
			if len(disconnected) != 1 || !strings.Contains(disconnected[0], "== bool 0") || !strings.Contains(disconnected[0], "and on(namespace, service, instance) (up{") || strings.Contains(disconnected[0], "vector(0)") {
				t.Fatalf("disconnection count hides missing inputs: %#v", disconnected)
			}
			if queries["HTTP request rate by status class"] == nil || queries["Backup / restore-drill failures (24h)"] == nil {
				t.Fatal("existing panels missing")
			}
		})
	}
}

func TestApplicationDashboardSourceMissingData(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("dashboards", "management-plane.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dashboard struct {
		Panels []struct {
			Title       string
			FieldConfig struct{ Defaults map[string]any }
			Targets     []struct{ Expr string }
		}
	}
	if err := json.Unmarshal(source, &dashboard); err != nil {
		t.Fatal(err)
	}
	for _, panel := range dashboard.Panels {
		if panel.Title == "Agents disconnected" || strings.Contains(panel.Title, "scrape availability") || panel.Title == "Process CPU usage (cores)" || panel.Title == "Go heap in use" || panel.Title == "Go goroutines" {
			if panel.FieldConfig.Defaults["noValue"] != "No data" {
				t.Fatalf("missing-data display absent: %s", panel.Title)
			}
			for _, target := range panel.Targets {
				if strings.Contains(target.Expr, "vector(0)") {
					t.Fatalf("fabricated zero: %s", target.Expr)
				}
			}
		}
	}
}

func TestApplicationDashboardOptOut(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		sets := []string{}
		if enabled {
			sets = append(sets, "metrics.dashboards.enabled=true")
		}
		docs := parseRenderedDocs(t, helmTemplate(t, sets...))
		if renderedDocExists(docs, "ConfigMap", "astronomer-dashboard-management-plane") != enabled {
			t.Fatal("dashboard enablement not respected")
		}
		for _, component := range []string{"server", "worker"} {
			if renderedDocExists(docs, "ServiceMonitor", "astronomer-"+component) {
				t.Fatal("dashboard setting enabled application monitors")
			}
		}
	}
}
