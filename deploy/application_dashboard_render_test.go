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
			assertObservationDashboardQueries(t, queries, vars)
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
		if panel.Title == "Agents disconnected" || strings.Contains(panel.Title, "scrape availability") || panel.Title == "Process CPU usage (cores)" || panel.Title == "Go heap in use" || panel.Title == "Go goroutines" || strings.HasPrefix(panel.Title, "Embedded ") || panel.Title == "Server DB average acquisition duration" {
			if panel.FieldConfig.Defaults["noValue"] != "No data" {
				t.Fatalf("missing-data display absent: %s", panel.Title)
			}
			if strings.HasPrefix(panel.Title, "Embedded ") || panel.Title == "Server DB average acquisition duration" {
				custom, ok := panel.FieldConfig.Defaults["custom"].(map[string]any)
				if !ok || custom["spanNulls"] != false {
					t.Fatalf("observation gaps are connected: %s", panel.Title)
				}
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

// These are structural query contracts, not a substitute for PromQL evaluation.
func assertObservationDashboardQueries(t *testing.T, queries map[string][]string, vars map[string]string) {
	t.Helper()
	contracts := map[string][]string{
		"Embedded observation source availability": {"astronomer_agent_delivery_observation_source_available", "astronomer_agent_delivery_observation_observed_at_timestamp_seconds", "> bool 0", ">= bool 0", "<= bool 240"},
		"Embedded observation source age":          {"time() - astronomer_agent_delivery_observation_observed_at_timestamp_seconds", "> 0", ">= 0"},
		"Embedded tracked LIST/WATCH request rate": {"astronomer_agent_observation_requests_total", "sum by (namespace, service, instance, kind, verb, outcome)", "rate("},
		"Embedded inventory refresh p95 duration":  {"astronomer_agent_delivery_observation_refresh_duration_seconds_bucket", "histogram_quantile(0.95,", "sum by (namespace, service, instance, source, outcome, le)"},
		"Embedded inventory refresh frequency":     {"astronomer_agent_delivery_observation_refresh_duration_seconds_count", "sum by (namespace, service, instance, source, outcome)", "rate("},
		"Server DB average acquisition duration":   {"rate(astronomer_db_pool_acquire_duration_seconds_total", "/ (rate(astronomer_db_pool_acquire_count_total", "[5m]) > 0)"},
	}
	selector := `{namespace="${metrics_namespace}",service="${metrics_fullname}-server-metrics"}`
	for title, fragments := range contracts {
		values := queries[title]
		if len(values) != 1 {
			t.Fatalf("expected one per-target query for %s: %v", title, values)
		}
		q := values[0]
		for _, fragment := range append(fragments, selector, "and on(namespace, service, instance) (up"+selector+" == 1)") {
			if !strings.Contains(q, fragment) {
				t.Fatalf("%s lacks contract %q: %s", title, fragment, q)
			}
		}
		for _, forbidden := range []string{"vector(0)", "or 0", "clamp_min", "avg(", "== bool", "observation_watch_events_total", "cluster_id"} {
			if strings.Contains(q, forbidden) {
				t.Fatalf("%s loses observation semantics: %s", title, q)
			}
		}
		if title == "Embedded observation source availability" || title == "Embedded observation source age" {
			sample := "astronomer_agent_delivery_observation_sampled_at_timestamp_seconds" + selector
			match := "on(namespace, service, instance, astronomer_instance_id, source)"
			recency := " and " + match + " ((time() - " + sample + " >= 0) and (time() - " + sample + " <= 60))"
			if !strings.Contains(q, recency) || strings.Contains(q, "observation_source_age_seconds") {
				t.Fatalf("%s can conceal a stopped producer: %s", title, q)
			}
			if title == "Embedded observation source availability" {
				// Multiply by boolean evidence validity rather than filter it out:
				// sampled noncurrent/unknown/expired sources must retain zero.
				observed := "astronomer_agent_delivery_observation_observed_at_timestamp_seconds" + selector
				validity := " * " + match + " ((" + observed + " > bool 0) * (time() - " + observed + " >= bool 0) * (time() - " + observed + " <= bool 240))"
				if !strings.Contains(q, validity) {
					t.Fatalf("availability hides invalid evidence instead of showing zero: %s", q)
				}
			} else if strings.Contains(q, "bool") {
				t.Fatalf("age must omit unknown/future evidence, not turn it into a boolean: %s", q)
			}
		}
		// Resolve Grafana constants from the actual rendered ConfigMap and ensure
		// every metric selector stays on this release's server service.
		resolved := strings.NewReplacer("${metrics_namespace}", vars["metrics_namespace"], "${metrics_fullname}", vars["metrics_fullname"]).Replace(q)
		want := `{namespace="` + vars["metrics_namespace"] + `",service="` + vars["metrics_fullname"] + `-server-metrics"}`
		if strings.Count(resolved, want) < 2 || strings.Contains(resolved, "${") || strings.Contains(resolved, "worker-metrics") {
			t.Fatalf("%s escaped rendered scope: %s", title, resolved)
		}
	}
}
