package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func estateCompleteCollectorReport() *estateReport {
	m := estateTestManifest()
	r := newEstateReport(m, strings.Repeat("a", 64), &config{})
	start := time.Unix(1800000000, 0)
	p := estateMeasuredPhase{Spec: m.Phases[0], Warmup: estateWindow{start, start.Add(5 * time.Minute)}}
	p.Measurement = estateWindow{p.Warmup.End, p.Warmup.End.Add(30 * time.Minute)}
	for _, member := range m.Members {
		v := estateVerification{Member: member.Name, IdentityVerified: true, RenderedNamespace: "UNAVAILABLE"}
		r.StartVerification = append(r.StartVerification, v)
		r.EndVerification = append(r.EndVerification, v)
		metric := estateMetricReport{Member: member.Name}
		for i := 0; i < 120; i++ {
			now := p.Measurement.Start.Add(time.Duration(i) * 15 * time.Second)
			raw := estateMetricFixture(now)
			points, _ := parseEstateMetrics(raw, "test")
			metric.Attempts++
			metric.observe(raw, points, now)
		}
		p.Metrics = append(p.Metrics, metric)
	}
	var histogram estateLatencyHistogram
	for i := 0; i < 300; i++ {
		histogram.observe(time.Millisecond)
	}
	for _, q := range estateRequestCatalog(m, p.Spec) {
		p.HTTP = append(p.HTTP, estateRequestResult{Scenario: q.Scenario, Member: q.Member, Scheduled: 300, Completed: 300, Success: 300, HeaderLatency: histogram.distribution(), FullResponseLatency: histogram.distribution()})
	}
	p.Totals = estateTotals(p.Spec, p.HTTP)
	r.Phases = []estateMeasuredPhase{p}
	return r
}
func TestRealEstateReportNeverQualifiesMissingScenarios(t *testing.T) {
	r := estateCompleteCollectorReport()
	r.evaluate()
	if r.Verdict != "incomplete" || r.Qualified || len(r.Problems) != 0 {
		t.Fatalf("unexpected collector result %+v", r.Problems)
	}
	if !strings.HasPrefix(r.Pending["end_to_end_freshness_p95"], "NOT_RUN") || !strings.HasPrefix(r.Pending["list_reduction_80_percent"], "NOT_RUN") {
		t.Fatal("fabricated live evidence")
	}
	for _, mutate := range []func(*estateReport){
		func(r *estateReport) { r.Phases[0].Measurement.End = r.Phases[0].Measurement.Start.Add(time.Minute) },
		func(r *estateReport) { r.Phases[0].Warmup.End = r.Phases[0].Warmup.Start.Add(time.Minute) },
		func(r *estateReport) { r.Phases[0].HTTP[0].Failed = 1 },
		func(r *estateReport) {
			for i := range r.Phases[0].HTTP {
				r.Phases[0].HTTP[i].Scheduled = 1
			}
		},
		func(r *estateReport) { r.Members = 1 },
		func(r *estateReport) { r.Phases[0].Metrics[0].Samples = 10 },
	} {
		candidate := estateCompleteCollectorReport()
		mutate(candidate)
		candidate.evaluate()
		if candidate.Verdict != "failed" || candidate.Qualified {
			t.Fatal("shortcut accepted")
		}
	}
}
func TestRealEstateComparisonRejectsDifferentConditions(t *testing.T) {
	a := estateCompleteCollectorReport()
	a.evaluate()
	b := estateCompleteCollectorReport()
	b.evaluate()
	if err := comparableEstateReports(*a, *b); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*estateReport){func(r *estateReport) { r.Tier = 10 }, func(r *estateReport) { r.Environment.HardwareSHA256 = strings.Repeat("f", 64) }, func(r *estateReport) { r.FixtureSHA256 = "different" }, func(r *estateReport) {
		r.Definitions = append([]estatePhaseSpec(nil), r.Definitions...)
		r.Definitions[0].RPS = 2
	}} {
		candidate := estateCompleteCollectorReport()
		mutate(candidate)
		if comparableEstateReports(*a, *candidate) == nil {
			t.Fatal("mismatch accepted")
		}
	}
}
func TestRealEstateArtifactsAndRedaction(t *testing.T) {
	r := estateCompleteCollectorReport()
	path := filepath.Join(t.TempDir(), "estate.md")
	if err := finishEstateReport(path, r); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(path)
	raw, _ := os.ReadFile(path + ".json")
	sum, _ := os.ReadFile(path + ".sha256")
	want := fmt.Sprintf("%x  estate.md\n%x  estate.md.json\n", sha256.Sum256(md), sha256.Sum256(raw))
	if string(sum) != want {
		t.Fatal("checksum contract differs")
	}
	for _, secret := range []string{"https://member", "token_file", "metric-token", "api-test-token"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("endpoint or credential leaked")
		}
	}
}
func TestRealEstateCheckOnlyDoesNotReadTokens(t *testing.T) {
	m := estateTestManifest()
	for i := range m.Members {
		m.Members[i].Metrics.TokenFile = "/not-present"
	}
	raw, _ := json.Marshal(m)
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config{realEstate: path, checkOnly: true, tokenPath: "/not-present", server: "https://api.test", rps: 1, duration: 30 * time.Minute, warmup: 5 * time.Minute}
	if err := runRealEstate(cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
}
func TestRealEstateCancellationMakesNoNetworkRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := estateMetricReport{}
	collectEstateMetrics(ctx, ctx, time.Now(), estateHTTPClient(), estateTestManifest().Members[0], "", &r)
	if r.Attempts != 0 {
		t.Fatal("canceled collector attempted request")
	}
}
