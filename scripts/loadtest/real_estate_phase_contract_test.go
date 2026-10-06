package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRealEstatePhaseManifestBounds(t *testing.T) {
	for _, change := range []func(*estateManifest){
		func(m *estateManifest) { m.Schema = "astronomer-real-estate-v1" },
		func(m *estateManifest) { m.Phases = nil },
		func(m *estateManifest) { m.Phases = append(m.Phases, m.Phases[0]) },
		func(m *estateManifest) { m.Phases[0].RPS = .001 },
		func(m *estateManifest) { m.Phases[0].Mode = "unknown" },
		func(m *estateManifest) { m.Search = estateTestSearch() },
		func(m *estateManifest) {
			m.Search = estateTestSearch()
			m.Phases[0].Mode = "search"
			m.Phases[0].RPS = .2
		},
		func(m *estateManifest) {
			m.Search = estateTestSearch()
			m.Phases[0].Mode = "search"
			m.Phases[0].RPS = .1
			m.Search.Types = []string{"secrets"}
		},
		func(m *estateManifest) {
			m.Search = estateTestSearch()
			m.Phases[0].Mode = "search"
			m.Phases[0].RPS = .1
			m.Search.Fanout = append(m.Search.Fanout, m.Search.Fanout[0])
		},
	} {
		m := estateTestManifest()
		change(&m)
		if m.validate() == nil {
			t.Fatal("invalid phase contract accepted")
		}
	}
	m := estateTestManifest()
	m.Search = estateTestSearch()
	m.Phases[0].Mode = "search"
	m.Phases[0].RPS = .1
	if err := m.validate(); err != nil {
		t.Fatal(err)
	}
	q := estateRequestCatalog(m, m.Phases[0])[0]
	if strings.Contains(q.Path, "cluster_id") || strings.Contains(q.Path, "project_id") || !strings.Contains(q.Path, "namespace=benchmark") {
		t.Fatal("invented search filter")
	}
	r := newEstateReport(m, "digest", &config{})
	m.Search.TokenFile = "private-path"
	r = newEstateReport(m, "digest", &config{})
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "private-path") {
		t.Fatal("search token path exposed")
	}
}
func TestRealEstateMeasuredIdleOrchestration(t *testing.T) {
	m := estateTestManifest()
	client := estateHTTPClient()
	client.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/healthz" {
			for _, member := range m.Members {
				if strings.Contains(member.Metrics.URL, r.URL.Host) {
					return estateResponse(map[string]any{"status": "ok", "cluster_id": member.ClusterID}), nil
				}
			}
		}
		if r.URL.Path == "/metrics" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(estateMetricFixture(time.Now())))), Header: http.Header{}}, nil
		}
		return nil, fmt.Errorf("idle made application request %s", r.URL.Path)
	})
	// Short internal runner fixture; public manifest validation rejects this.
	p := runMeasuredEstatePhase(context.Background(), &config{server: "https://api.test"}, m, estatePhaseSpec{Name: "idle", Mode: "idle", MeasurementSeconds: 1}, "token", "", []string{"", ""}, client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if len(p.HTTP) != 0 || p.Measurement.End.Sub(p.Measurement.Start) != time.Second || len(p.Metrics) != 2 {
		t.Fatalf("idle phase incomplete: %+v", p)
	}
	for _, metric := range p.Metrics {
		if metric.Samples != 1 || metric.Errors != 0 {
			t.Fatal("idle metrics absent")
		}
	}
}
func TestRealEstateDeliveryValidationRejectsStaleOrForeign(t *testing.T) {
	m := estateTestManifest().Members[0]
	a := m.Assignments[0]
	for _, kind := range []string{"ready", "stale", "foreign", "noncurrent", "generation"} {
		t.Run(kind, func(t *testing.T) {
			d := map[string]any{"id": a.ID, "cluster_id": m.ClusterID, "target_id": a.TargetID, "desired_generation": a.Generation, "observed_generation": a.Generation, "desired_spec_digest": a.SpecDigest, "observed_spec_digest": a.SpecDigest, "phase": "ready", "action": "apply", "last_observed_at": time.Now()}
			if kind == "stale" {
				d["last_observed_at"] = time.Now().Add(-time.Hour)
			}
			if kind == "foreign" {
				d["cluster_id"] = "other"
			}
			if kind == "generation" {
				d["observed_generation"] = 99
			}
			if kind == "noncurrent" {
				d["inventory"] = map[string]any{"observation": map[string]any{"state": "stale", "observed_at": time.Now()}}
			}
			raw, _ := json.Marshal(map[string]any{"data": map[string]any{"deployment": d}})
			err := validateEstateDeliveryBody(raw, m, "detail", &a)
			if (err == nil) != (kind == "ready") {
				t.Fatalf("%s validation: %v", kind, err)
			}
		})
	}
}
func TestRealEstateComparisonRejectsTimingOrPhaseChanges(t *testing.T) {
	for _, change := range []func(*estateReport){
		func(r *estateReport) { r.Schema = "astronomer-real-estate-report-v1" },
		func(r *estateReport) { r.Timing = "headers-only" },
		func(r *estateReport) { r.Definitions[0].MeasurementSeconds++ },
		func(r *estateReport) { r.Search = estateTestSearch() },
	} {
		a, b := estateCompleteCollectorReport(), estateCompleteCollectorReport()
		change(b)
		if comparableEstateReports(*a, *b) == nil {
			t.Fatal("incomparable report accepted")
		}
	}
}

func TestRealEstateSearchPhaseUsesProofAndCancelsForeignResult(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			m := estateTestManifest()
			m.Search = estateTestSearch()
			searchRequests, proofs := 0, 0
			client := estateHTTPClient()
			client.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/healthz":
					for _, member := range m.Members {
						if strings.Contains(member.Metrics.URL, r.URL.Host) {
							return estateResponse(map[string]any{"status": "ok", "cluster_id": member.ClusterID}), nil
						}
					}
				case "/metrics":
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(estateMetricFixture(time.Now())))), Header: http.Header{}}, nil
				case "/api/v1/rbac/my-permissions":
					if r.Header.Get("Authorization") != "Bearer search-token" {
						t.Error("wrong search credential")
					}
					proofs++
					return estateResponse(map[string]any{"data": map[string]any{"subject": map[string]any{"self": true}, "superuser": true}}), nil
				case "/api/v1/clusters/":
					return estateResponse(map[string]any{"data": []any{map[string]any{"id": m.Members[0].ClusterID, "status": "active"}, map[string]any{"id": m.Members[1].ClusterID, "status": "active"}}, "pagination": map[string]any{"total": 2, "has_more": false}}), nil
				case "/api/v1/resources/search":
					searchRequests++
					id := m.Search.Fanout[0]
					if foreign {
						id = "other"
					}
					return estateResponse(map[string]any{"data": map[string]any{"type": "pods", "clusters_queried": 2, "clusters_failed": 0, "truncated": false, "errors": []any{}, "results": []any{map[string]any{"cluster_id": id, "namespace": m.Search.Namespace}}}}), nil
				}
				return nil, fmt.Errorf("unexpected fixture request %s", r.URL.Path)
			})
			p := runMeasuredEstatePhase(context.Background(), &config{server: "https://api.test"}, m, estatePhaseSpec{Name: "search", Mode: "search", RPS: .1, MeasurementSeconds: 1}, "api-token", "search-token", []string{"", ""}, client, slog.Default())
			if searchRequests != 1 || proofs != 2 || p.Totals.Completed != 1 {
				t.Fatalf("missing scheduled/proof coverage: %d %d %+v", searchRequests, proofs, p.Totals)
			}
			if foreign {
				if p.Totals.Failed != 1 || len(p.Problems) == 0 {
					t.Fatal("foreign search did not abort")
				}
			} else if p.Totals.Success != 1 || len(p.Problems) > 0 {
				t.Fatalf("complete search failed: %+v", p)
			}
		})
	}
}
