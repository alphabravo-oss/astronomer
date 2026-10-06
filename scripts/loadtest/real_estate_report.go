package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

type estateWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}
type estateReport struct {
	Schema            string                `json:"schema_version"`
	Timing            string                `json:"timing_semantics"`
	Scope             string                `json:"evidence_scope"`
	Verdict           string                `json:"verdict"`
	Qualified         bool                  `json:"qualified"`
	FixtureSHA256     string                `json:"fixture_sha256"`
	ManifestSHA256    string                `json:"manifest_sha256"`
	DriverRevision    string                `json:"driver_revision"`
	DriverModified    string                `json:"driver_modified"`
	Environment       estateEnvironment     `json:"environment"`
	Tier              int                   `json:"assignment_tier_per_member"`
	Members           int                   `json:"members"`
	Definitions       []estatePhaseSpec     `json:"phase_definitions"`
	Search            *estateSearchSpec     `json:"search_scope,omitempty"`
	Preflight         estateWindow          `json:"preflight"`
	StartVerification []estateVerification  `json:"start_verification"`
	EndVerification   []estateVerification  `json:"end_verification"`
	Phases            []estateMeasuredPhase `json:"phases"`
	Problems          []string              `json:"problems"`
	Pending           map[string]string     `json:"pending"`
}

func newEstateReport(m estateManifest, digest string, c *config) *estateReport {
	revision, modified := estateDriverIdentity()
	r := &estateReport{Schema: "astronomer-real-estate-report-v2", Timing: estateTiming, Scope: "engineering_preprovisioned_observation", Verdict: "incomplete", FixtureSHA256: estateFixtureDigest(m), ManifestSHA256: digest, DriverRevision: revision, DriverModified: modified, Environment: m.Environment, Tier: m.Tier, Members: len(m.Members), Definitions: m.Phases, Problems: []string{}, Pending: map[string]string{
		"agent_cold_start":   "NOT_RUN: pre-provisioned members",
		"fixture_lifecycle":  "NOT_RUN: no creation or cleanup; fixtures unchanged",
		"rendered_namespace": "See start/end source-aware verification",
		"resource_churn":     "NOT_RUN", "multiple_panels": "NOT_RUN", "two_browser_tabs": "NOT_RUN", "reconnect_burst": "NOT_RUN",
		"end_to_end_freshness_p95":  "NOT_RUN: source age is not change-to-UI latency",
		"list_reduction_80_percent": "NOT_RUN: tracked counters exclude complete baseline API traffic",
		"assignment_recurring_gets": "NOT_RUN: tracked LIST/WATCH counters cannot establish GET absence",
		"queue_age_and_event_relay": "NOT_RUN: member endpoints omit management queue/relay evidence",
		"audit_conservation":        "NOT_RUN: read-only workload performs no mandatory audit mutations",
		"repeatability":             "NOT_RUN: independent matched repetitions required",
		"idle_estate":               "NOT_RUN", "inventory_browsing": "NOT_RUN", "scoped_search": "NOT_RUN", "delivery_status_workload": "NOT_RUN",
	}}
	if m.Search != nil {
		copy := *m.Search
		copy.TokenFile = ""
		r.Search = &copy
	}
	return r
}
func (r *estateReport) evaluate() {
	r.Qualified = false
	r.Verdict = "incomplete"
	if r.Schema != "astronomer-real-estate-report-v2" || r.Timing != estateTiming || len(r.Definitions) == 0 || len(r.Phases) != len(r.Definitions) {
		r.Problems = append(r.Problems, "phase contract incomplete")
	}
	if r.Members < 2 || len(r.StartVerification) != r.Members || len(r.EndVerification) != r.Members {
		r.Problems = append(r.Problems, "member verification incomplete")
	}
	for _, batch := range [][]estateVerification{r.StartVerification, r.EndVerification} {
		for _, v := range batch {
			if !v.IdentityVerified {
				r.Problems = append(r.Problems, "member identity not verified")
			}
		}
	}
	for i, p := range r.Phases {
		if i >= len(r.Definitions) || p.Spec != r.Definitions[i] {
			r.Problems = append(r.Problems, "phase definition mismatch")
			continue
		}
		problems := evaluateEstateMeasuredPhase(p, r.Members, r.Tier)
		r.Problems = append(r.Problems, problems...)
		if len(problems) == 0 {
			key := map[string]string{"idle": "idle_estate", "resources": "inventory_browsing", "delivery": "delivery_status_workload", "search": "scoped_search"}[p.Spec.Mode]
			r.Pending[key] = "MEASURED: see per-phase evidence; not qualification"
		}
	}
	if len(r.Problems) > 0 {
		r.Verdict = "failed"
	}
}
func evaluateEstateMeasuredPhase(p estateMeasuredPhase, members, tier int) []string {
	problems := append([]string(nil), p.Problems...)
	add := func(message string) { problems = append(problems, p.Spec.Name+": "+message) }
	if p.Spec.WarmupSeconds < 300 || p.Spec.MeasurementSeconds < 1800 || p.Warmup.End.Sub(p.Warmup.Start).Seconds() < float64(p.Spec.WarmupSeconds) || p.Measurement.End.Sub(p.Measurement.Start).Seconds() < float64(p.Spec.MeasurementSeconds)*.98 {
		add("warmup or measurement incomplete")
	}
	if p.Totals != estateTotals(p.Spec, p.HTTP) {
		add("request total accounting differs")
	}
	seen := map[string]bool{}
	total, success := 0, 0
	for _, h := range p.HTTP {
		key := h.Member + "/" + h.Scenario + "/" + h.Assignment
		if seen[key] {
			add("duplicate coverage row")
		}
		if h.HeaderLatency.Samples != uint64(h.Completed) || h.FullResponseLatency.Samples != uint64(h.Completed) {
			add("latency histogram coverage incomplete")
		}
		if h.Completed > 0 && (h.HeaderLatency.P99UpperBoundMS == nil || h.FullResponseLatency.P99UpperBoundMS == nil) {
			add("full-window latency percentile unavailable")
		}
		seen[key] = true
		total += h.Scheduled
		success += h.Success
		if h.Scheduled == 0 || h.Completed != h.Scheduled || h.Success+h.Failed != h.Completed || h.Failed > 0 {
			add("missing coverage or HTTP failure")
		}
	}
	expectedRows := 0
	switch p.Spec.Mode {
	case "resources":
		expectedRows = members * 3
	case "delivery":
		expectedRows = members * (tier + 2)
	case "search":
		expectedRows = len(p.HTTP)
		if expectedRows < 1 || expectedRows > 3 {
			add("search coverage missing")
		}
	}
	if len(p.HTTP) != expectedRows {
		add("scenario/member/assignment coverage missing")
	}
	expected := int(math.Ceil(p.Spec.RPS * float64(p.Spec.MeasurementSeconds)))
	if total > expected || float64(success) < float64(expected)*.95 {
		add("achieved workload outside declared bounds")
	}
	if p.Spec.Mode == "idle" && (total != 0 || p.Spec.RPS != 0) {
		add("idle scheduled application work")
	}
	if p.Spec.Mode == "search" {
		if len(p.ScopeChecks) < 2+int((p.Spec.WarmupSeconds+p.Spec.MeasurementSeconds)/60)-1 {
			add("search scope coverage missing")
		}
		for _, check := range p.ScopeChecks {
			if !check.OK {
				add("search scope changed")
			}
		}
	}
	if len(p.Metrics) != members {
		add("member metric evidence missing")
	}
	for _, m := range p.Metrics {
		problems = append(problems, m.problems()...)
		if float64(m.Samples) < float64(p.Spec.MeasurementSeconds)/metricsScrape.Seconds()*.98 {
			add("insufficient metric coverage")
		}
	}
	return problems
}
func finishEstateReport(path string, r *estateReport) error {
	r.evaluate()
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	var md strings.Builder
	fmt.Fprintf(&md, "# Real-estate engineering observation\n\nVerdict: **%s**. Qualified: **false**.\n\n%d pre-provisioned members; %d assignments per member.\n\nPer-phase rates use requests/second. Header latency preserves historical semantics; full-response latency includes bounded body read and validation. Fixed full-window histograms report conservative p99 upper bounds and explicit overflow. Neither source age nor these HTTP timings establish change-to-UI latency.\n\nSearch scope is periodically checked snapshot evidence, not atomic membership fencing; empty successful clusters are not identified in search responses.\n\n", r.Verdict, r.Members, r.Tier)
	for _, p := range r.Phases {
		fmt.Fprintf(&md, "- %s (%s): %.4g requests/second; %.0fs measured.\n", p.Spec.Name, p.Spec.Mode, p.Spec.RPS, p.Measurement.End.Sub(p.Measurement.Start).Seconds())
	}
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
func comparableEstateReports(a, b estateReport) error {
	left, right := a.Environment, b.Environment
	left.Commit = ""
	right.Commit = ""
	left.ImagesSHA256 = ""
	right.ImagesSHA256 = ""
	if a.Schema != b.Schema || a.Schema != "astronomer-real-estate-report-v2" || a.Timing != b.Timing || a.Timing != estateTiming || a.FixtureSHA256 == "" || a.FixtureSHA256 != b.FixtureSHA256 || !reflect.DeepEqual(left, right) || a.Tier != b.Tier || a.Members != b.Members || !reflect.DeepEqual(a.Definitions, b.Definitions) || !reflect.DeepEqual(a.Search, b.Search) {
		return errors.New("estate environment, phase, scope or timing mismatch")
	}
	if a.Verdict == "failed" || b.Verdict == "failed" {
		return errors.New("failed reports cannot support comparison")
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
