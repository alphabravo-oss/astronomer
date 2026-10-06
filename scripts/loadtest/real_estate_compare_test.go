package main

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func comparisonFixture(t *testing.T, step float64) estateReport {
	t.Helper()
	r := estateCompleteCollectorReport()
	r.DriverRevision = strings.Repeat("b", 40)
	r.DriverModified = "false"
	for i := range r.StartVerification {
		r.StartVerification[i].RenderedNamespace = "VERIFIED"
		r.EndVerification[i].RenderedNamespace = "VERIFIED"
	}
	for i := range r.Phases[0].Metrics {
		m := &r.Phases[0].Metrics[i]
		m.TargetSHA256 = strings.Repeat("c", 64)
		m.Transport = newEstateTransportReport()
		for j := 0; j < 120; j++ {
			now := r.Phases[0].Measurement.Start.Add(time.Duration(j) * 15 * time.Second)
			m.Transport.observe(transportPoints(float64(r.Phases[0].Measurement.Start.Add(-time.Hour).Unix()), 100+float64(j)*step), now)
		}
	}
	r.evaluate()
	if err := validateEstateComparisonReport(*r); err != nil {
		t.Fatal(err)
	}
	return *r
}
func comparisonProof() estateComparisonProvenance {
	return estateComparisonProvenance{ImageEvidence: "hash_matched_user_reviewed_changes", Attribution: "declared_matching"}
}
func TestEstateComparisonRatesAndEligibility(t *testing.T) {
	a, b := comparisonFixture(t, 10), comparisonFixture(t, 2)
	r := compareEstateReports(a, b, "a", "b", comparisonProof())
	if r.Qualified || !r.OptimizationEligible {
		t.Fatalf("eligibility: %+v / %+v", r.Blockers, r.Rows[0].Blockers)
	}
	row := r.Rows[0]
	m := row.Metrics[len(row.Metrics)-1]
	if m.PercentChange == nil || math.Abs(*m.PercentChange+80) > 1e-9 || *m.Baseline.Delta != 1190 || *m.Baseline.Seconds != 1785 {
		t.Fatalf("rate normalization: %+v", m)
	}
	if row.BaselineMemberWork.Success*2 != row.BaselineWork.Success {
		t.Fatal("member counts repeat fleet counts")
	}
	for _, category := range []string{"delivery_lists", "delivery_observation_total"} {
		if !estateCategoryIncludes(category, "shared_observation", "list") {
			t.Fatal("shared cost excluded")
		}
	}
}
func TestEstateComparisonBlocksGapsAndAttribution(t *testing.T) {
	for _, kind := range []string{"freshness", "failure", "short_interval", "attribution", "zero"} {
		t.Run(kind, func(t *testing.T) {
			a, b := comparisonFixture(t, 10), comparisonFixture(t, 2)
			proof := comparisonProof()
			switch kind {
			case "freshness":
				a.Phases[0].Metrics[0].MissingRequired++
				a.evaluate()
			case "failure":
				a.Problems = append(a.Problems, "untrusted arbitrary report text")
			case "short_interval":
				m := &a.Phases[0].Metrics[0]
				m.Transport.LastAt = m.Transport.FirstAt.Add(time.Minute)
			case "attribution":
				proof.Attribution = "declared_mismatch"
			case "zero":
				a = comparisonFixture(t, 0)
			}
			r := compareEstateReports(a, b, "a", "b", proof)
			m := r.Rows[0].Metrics[len(r.Rows[0].Metrics)-1]
			if kind != "zero" && r.OptimizationEligible {
				t.Fatal("gap eligible")
			}
			if kind == "zero" && (m.PercentChange != nil || m.PercentageStatus != "undefined_zero_baseline") {
				t.Fatal("zero percentage fabricated")
			}
			if kind == "attribution" {
				if r.Rows[0].Metrics[0].PercentChange != nil || m.PercentChange == nil {
					t.Fatal("attribution mismatch hidden")
				}
			}
			if strings.Contains(string(estateComparisonMarkdown(r)), "untrusted arbitrary") {
				t.Fatal("raw report echoed")
			}
		})
	}
}
func TestEstateComparisonRejectsInconsistentAccounting(t *testing.T) {
	for _, kind := range []string{"delta", "total", "group", "interval", "histogram", "requests", "labels"} {
		t.Run(kind, func(t *testing.T) {
			r := comparisonFixture(t, 10)
			tr := r.Phases[0].Metrics[0].Transport
			switch kind {
			case "delta":
				for _, s := range tr.Series {
					s.AdjacentDelta++
				}
			case "total":
				tr.Total.AdjacentDelta++
			case "group":
				tr.ByConsumerOperation[0].State = "invented"
			case "interval":
				tr.FirstAt = r.Phases[0].Measurement.Start.Add(-time.Second)
			case "histogram":
				r.Phases[0].HTTP[0].HeaderLatency.Counts[0]++
			case "requests":
				r.Phases[0].HTTP[0].Success++
			case "labels":
				for _, s := range tr.Series {
					s.Consumer = "invented"
				}
			}
			if validateEstateComparisonReport(r) == nil {
				t.Fatal("inconsistent report accepted")
			}
		})
	}
}
func TestEstateComparisonStrictInputAndOfflineFlags(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{} {}`, `{"unknown":true}`, strings.Repeat("[", 34) + strings.Repeat("]", 34)} {
		var dst struct{}
		if decodeEstateStrict([]byte(raw), &dst) == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
	f := flag.NewFlagSet("test", flag.ContinueOnError)
	f.String("token", "", "")
	_ = f.Parse([]string{"-token", "/must-not-read"})
	c := &config{compareBaseline: "a", compareCandidate: "b", outPath: "out"}
	if validateEstateComparisonFlags(c, f, nil) == nil {
		t.Fatal("online flag accepted")
	}
	empty := flag.NewFlagSet("test", flag.ContinueOnError)
	if validateEstateComparisonFlags(c, empty, []string{"LOADTEST_PROFILE=/must-not-read"}) == nil {
		t.Fatal("online environment accepted")
	}
	if validateEstateComparisonFlags(c, empty, nil) != nil {
		t.Fatal("offline rejected")
	}
	path := filepath.Join(t.TempDir(), "large")
	if err := os.WriteFile(path, make([]byte, 100), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readEstateBounded(path, 10); err == nil {
		t.Fatal("oversized input accepted")
	}
}
func TestEstateComparisonArtifactsDeterministic(t *testing.T) {
	dir := t.TempDir()
	r := comparisonFixture(t, 2)
	raw, _ := json.Marshal(r)
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := &config{compareBaseline: a, compareCandidate: b, outPath: filepath.Join(dir, "comparison.md")}
	if err := runEstateComparison(c); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(c.outPath + ".json")
	if err := runEstateComparison(c); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(c.outPath + ".json")
	if string(first) != string(second) {
		t.Fatal("nondeterministic")
	}
	var result estateComparison
	if json.Unmarshal(first, &result) != nil || result.Qualified || result.OptimizationEligible {
		t.Fatal("missing provenance qualified")
	}
	c.outPath = a
	if runEstateComparison(c) == nil {
		t.Fatal("input overwritten")
	}
}

func TestEstateComparisonImageReview(t *testing.T) {
	dir := t.TempDir()
	a, b := comparisonFixture(t, 10), comparisonFixture(t, 2)
	write := func(name string, v any) string {
		raw, _ := json.Marshal(v)
		p := filepath.Join(dir, name)
		if os.WriteFile(p, raw, 0600) != nil {
			t.Fatal("write")
		}
		return estateComparisonHash(string(raw))
	}
	a.Environment.ImagesSHA256 = write("a.json", estateImageInventory{Schema: "astronomer-image-inventory-v1", Images: map[string]string{"agent": "sha256:" + strings.Repeat("a", 64), "database": "sha256:" + strings.Repeat("b", 64)}})
	b.Environment.ImagesSHA256 = write("b.json", estateImageInventory{Schema: "astronomer-image-inventory-v1", Images: map[string]string{"agent": "sha256:" + strings.Repeat("c", 64), "database": "sha256:" + strings.Repeat("b", 64)}})
	review := estateImageReview{Schema: "astronomer-estate-image-review-v1", Baseline: "a.json", Candidate: "b.json", Allowed: []string{"agent"}, BaselineAttribution: "legacy_callbacks_other", CandidateAttribution: "tagged_callbacks_v1"}
	path := filepath.Join(dir, "review.json")
	write("review.json", review)
	proof, err := compareEstateImages(path, a, b)
	if err != nil || proof.ImageEvidence != "hash_matched_user_reviewed_changes" || proof.Attribution != "declared_mismatch" {
		t.Fatalf("review: %+v %v", proof, err)
	}
	review.Allowed = nil
	write("review.json", review)
	proof, err = compareEstateImages(path, a, b)
	if err != nil || proof.ImageEvidence != "unreviewed_image_change" {
		t.Fatal("unreviewed change accepted")
	}
	b.Environment.ImagesSHA256 = strings.Repeat("d", 64)
	if _, err = compareEstateImages(path, a, b); err == nil {
		t.Fatal("inventory hash mismatch accepted")
	}
}
func TestEstateComparisonPartialEvidenceConservation(t *testing.T) {
	r := comparisonFixture(t, 10)
	m := &r.Phases[0].Metrics[0]
	m.Transport = newEstateTransportReport()
	now := r.Phases[0].Measurement.Start
	start := float64(now.Add(-time.Hour).Unix())
	m.Transport.observe(transportPoints(start, 10), now)
	m.Transport.observe(transportPoints(start), now.Add(15*time.Second))
	m.Transport.observe(transportPoints(start, 20), now.Add(30*time.Second))
	if err := func() error { _, err := recomputeEstateTransport(m.Transport, r.Phases[0].Measurement); return err }(); err != nil {
		t.Fatal(err)
	}
	cost := estateTransportCost(m.Transport, "total_family")
	if cost.Delta != nil || cost.RPS != nil {
		t.Fatal("partial window bridged")
	}
	m.Transport.Total.AdjacentDelta++
	if func() error { _, err := recomputeEstateTransport(m.Transport, r.Phases[0].Measurement); return err }() == nil {
		t.Fatal("partial sums trusted")
	}
}

func TestEstateComparisonExactMemberCoverage(t *testing.T) {
	for _, kind := range []string{"rendered", "end_members", "catalog_member", "catalog_assignment"} {
		t.Run(kind, func(t *testing.T) {
			a, b := comparisonFixture(t, 10), comparisonFixture(t, 2)
			switch kind {
			case "rendered":
				a.StartVerification[0].RenderedNamespace = "UNAVAILABLE"
			case "end_members":
				a.EndVerification[0].Member = "foreign"
			case "catalog_member":
				a.Phases[0].HTTP[0].Member = "foreign"
				b.Phases[0].HTTP[0].Member = "foreign"
			case "catalog_assignment":
				a.Phases[0].HTTP[0].Assignment = "11111111-1111-4111-8111-111111111111"
				b.Phases[0].HTTP[0].Assignment = a.Phases[0].HTTP[0].Assignment
			}
			r := compareEstateReports(a, b, "a", "b", comparisonProof())
			if r.OptimizationEligible {
				t.Fatal("incomplete member proof eligible")
			}
		})
	}
}

func TestEstateComparisonParentTransportConsistency(t *testing.T) {
	for _, kind := range []string{"samples", "errors", "identity", "source_interval"} {
		t.Run(kind, func(t *testing.T) {
			r := comparisonFixture(t, 2)
			m := &r.Phases[0].Metrics[0]
			switch kind {
			case "source_interval":
				for _, s := range m.Series {
					s.FirstAt = r.Phases[0].Measurement.Start.Add(-time.Second)
					break
				}
			case "samples":
				m.Transport.Samples++
			case "errors":
				m.Transport.ScrapeErrors++
			case "identity":
				for key, s := range m.Series {
					if s.Labels["astronomer_instance_id"] != "" {
						delete(m.Series, key)
						s.Labels["astronomer_instance_id"] = "foreign"
						m.Series[estatePointKey(estatePoint{Name: s.Name, Labels: s.Labels})] = s
						break
					}
				}
			}
			if validateEstateComparisonReport(r) == nil {
				t.Fatal("parent mismatch accepted")
			}
		})
	}
}
