package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

type estateWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}
type estateHTTPResult struct {
	Scenario string  `json:"scenario"`
	Requests int     `json:"requests"`
	Errors   int     `json:"errors"`
	P99MS    float64 `json:"p99_ms"`
}
type estateReport struct {
	WarmupDrain                 estateWindow         `json:"warmup_request_drain"`
	MeasurementDrain            estateWindow         `json:"measurement_request_drain"`
	FixtureSHA256               string               `json:"fixture_sha256"`
	DriverRevision              string               `json:"driver_revision"`
	DriverModified              string               `json:"driver_modified"`
	Schema                      string               `json:"schema_version"`
	Scope                       string               `json:"evidence_scope"`
	Verdict                     string               `json:"verdict"`
	Qualified                   bool                 `json:"qualified"`
	ManifestSHA256              string               `json:"manifest_sha256"`
	Environment                 estateEnvironment    `json:"environment"`
	Tier                        int                  `json:"assignment_tier_per_member"`
	Members                     int                  `json:"members"`
	TargetRPS                   int                  `json:"target_rps"`
	RequestedWarmupSeconds      float64              `json:"requested_warmup_seconds"`
	RequestedMeasurementSeconds float64              `json:"requested_measurement_seconds"`
	Preflight                   estateWindow         `json:"preflight"`
	Warmup                      estateWindow         `json:"warmup"`
	Measurement                 estateWindow         `json:"measurement"`
	StartVerification           []estateVerification `json:"start_verification"`
	EndVerification             []estateVerification `json:"end_verification"`
	HTTP                        []estateHTTPResult   `json:"http"`
	Metrics                     []estateMetricReport `json:"member_metrics"`
	Problems                    []string             `json:"problems"`
	Pending                     map[string]string    `json:"pending"`
}

func newEstateReport(m estateManifest, digest string, c *config) *estateReport {
	revision, modified := estateDriverIdentity()
	return &estateReport{FixtureSHA256: estateFixtureDigest(m), DriverRevision: revision, DriverModified: modified, Schema: "astronomer-real-estate-report-v1", Scope: "engineering_preprovisioned_observation", Verdict: "incomplete", ManifestSHA256: digest, Environment: m.Environment, Tier: m.Tier, Members: len(m.Members), TargetRPS: c.rps, RequestedWarmupSeconds: c.warmup.Seconds(), RequestedMeasurementSeconds: c.duration.Seconds(), Problems: []string{}, Pending: map[string]string{
		"agent_cold_start":   "NOT_RUN: members pre-provisioned; preflight is not startup",
		"fixture_lifecycle":  "NOT_RUN: no creation or cleanup; fixtures unchanged",
		"rendered_namespace": "See per-member start/end verification; unavailable for legacy, missing or truncated source inventory",
		"idle_estate":        "NOT_RUN", "resource_churn": "NOT_RUN", "scoped_search": "NOT_RUN", "delivery_status_workload": "NOT_RUN", "multiple_panels": "NOT_RUN", "two_browser_tabs": "NOT_RUN", "reconnect_burst": "NOT_RUN",
		"end_to_end_freshness_p95":  "NOT_RUN: source age is not change-to-UI latency",
		"list_reduction_80_percent": "NOT_RUN: tracked-source counters exclude complete baseline API traffic",
		"assignment_recurring_gets": "NOT_RUN: tracked LIST/WATCH counters cannot establish GET absence",
		"queue_age_and_event_relay": "NOT_RUN: member endpoints do not provide management queue/relay evidence",
		"audit_conservation":        "NOT_RUN: read-only workload performs no mandatory audit mutations",
		"repeatability":             "NOT_RUN: requires independent matched repetitions",
	}}
}
func estateHTTPResults(rec *recorder, c *config, w estateWindow) []estateHTTPResult {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	out := []estateHTTPResult{}
	for _, sc := range defaultScenarios() {
		samples := rec.httpSamples[sc.name]
		p99 := time.Duration(0)
		if len(samples) > 0 {
			p99 = percentile(samples, 0.99)
		}
		failures := rec.httpErrors[sc.name]
		for status, n := range rec.httpStatus[sc.name] {
			if status < 200 || status >= 300 {
				failures += n
			}
		}
		out = append(out, estateHTTPResult{sc.name, rec.httpCount[sc.name], failures, float64(p99) / float64(time.Millisecond)})
	}
	return out
}
func (r *estateReport) evaluate() {
	// This collector has no path to qualified/pass, irrespective of observed data.
	r.Qualified = false
	r.Verdict = "incomplete"
	if r.RequestedWarmupSeconds < 300 || r.RequestedMeasurementSeconds < 1800 || r.Warmup.End.Sub(r.Warmup.Start).Seconds() < r.RequestedWarmupSeconds || r.Measurement.End.Sub(r.Measurement.Start).Seconds() < r.RequestedMeasurementSeconds*0.98 {
		r.Problems = append(r.Problems, "warmup or measured window incomplete")
	}
	if len(r.StartVerification) != r.Members || len(r.EndVerification) != r.Members || r.Members < 2 {
		r.Problems = append(r.Problems, "member verification incomplete")
	}
	for _, batch := range [][]estateVerification{r.StartVerification, r.EndVerification} {
		for _, v := range batch {
			if !v.IdentityVerified {
				r.Problems = append(r.Problems, "member identity not verified")
			}
		}
	}
	total := 0
	for _, h := range r.HTTP {
		total += h.Requests
		if h.Errors > 0 || h.Requests == 0 {
			r.Problems = append(r.Problems, "HTTP errors or missing scenario: "+h.Scenario)
		}
		if (h.Scenario == "cluster_list" && h.P99MS > 500) || (h.Scenario == "cluster_pods" && h.P99MS > 2000) {
			r.Problems = append(r.Problems, "existing HTTP latency budget exceeded: "+h.Scenario)
		}
	}
	if len(r.HTTP) != len(defaultScenarios()) || float64(total) < float64(r.TargetRPS)*r.RequestedMeasurementSeconds*0.95 {
		r.Problems = append(r.Problems, "achieved workload below existing 95% floor")
	}
	if len(r.Metrics) != r.Members {
		r.Problems = append(r.Problems, "member metric evidence missing")
	}
	for _, m := range r.Metrics {
		r.Problems = append(r.Problems, m.problems()...)
		expected := int(r.RequestedMeasurementSeconds / metricsScrape.Seconds())
		if float64(m.Samples) < float64(expected)*0.98 {
			r.Problems = append(r.Problems, m.Member+": insufficient metric window coverage")
		}
	}
	if len(r.Problems) > 0 {
		r.Verdict = "failed"
	}
}
func finishEstateReport(path string, r *estateReport) error {
	r.evaluate()
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	var md strings.Builder
	fmt.Fprintf(&md, "# Real-estate engineering observation\n\nVerdict: **%s**. Qualified: **false**.\n\n%d pre-provisioned members; %d declared assignments per member.\n\nThis partial collector cannot establish live E04 acceptance. Source ages are not end-to-end freshness. Request deltas cover only declared tracked-source metrics.\n\n", r.Verdict, r.Members, r.Tier)
	for _, p := range r.Problems {
		fmt.Fprintf(&md, "- %s\n", p)
	}
	md.WriteString("\nPending evidence:\n\n")
	keys := []string{}
	for k := range r.Pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&md, "- %s: %s\n", k, r.Pending[k])
	}
	md.WriteString("\nPer-member metrics, fixture checks, HTTP outcomes and provenance are in the adjacent JSON artifact.\n")
	markdown := []byte(md.String())
	if err = os.WriteFile(path, markdown, 0600); err != nil {
		return err
	}
	if err = os.WriteFile(path+".json", raw, 0600); err != nil {
		return err
	}
	sums := fmt.Sprintf("%x  %s\n%x  %s.json\n", sha256.Sum256(markdown), filepath.Base(path), sha256.Sum256(raw), filepath.Base(path))
	if err = os.WriteFile(path+".sha256", []byte(sums), 0600); err != nil {
		return err
	}
	if r.Verdict == "failed" {
		return errors.New("real-estate engineering checks failed; see report")
	}
	return nil
}

// Comparability validates identical measurement conditions, not complete API
// attribution. Even comparable reports leave LIST reduction and GET absence NOT_RUN.
func comparableEstateReports(a, b estateReport) error {
	left, right := a.Environment, b.Environment
	left.Commit = ""
	right.Commit = ""
	left.ImagesSHA256 = ""
	right.ImagesSHA256 = ""
	x, _ := json.Marshal(left)
	y, _ := json.Marshal(right)
	if a.FixtureSHA256 == "" || a.FixtureSHA256 != b.FixtureSHA256 || string(x) != string(y) || a.Tier != b.Tier || a.Members != b.Members || a.TargetRPS != b.TargetRPS || a.RequestedMeasurementSeconds != b.RequestedMeasurementSeconds || a.RequestedWarmupSeconds != b.RequestedWarmupSeconds {
		return errors.New("estate environment, tier or workload mismatch")
	}
	if a.Verdict == "failed" || b.Verdict == "failed" {
		return errors.New("failed engineering reports cannot support comparison")
	}
	return nil
}

func estateFixtureDigest(m estateManifest) string {
	members := append([]estateMember(nil), m.Members...)
	for i := range members {
		members[i].Metrics = estateMetricsTarget{}
	}
	raw, _ := json.Marshal(members)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func estateDriverIdentity() (string, string) {
	revision, modified := "unavailable", "unavailable"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				revision = s.Value
			}
			if s.Key == "vcs.modified" {
				modified = s.Value
			}
		}
	}
	return revision, modified
}
