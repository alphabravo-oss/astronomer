package main

import (
	"strings"
	"testing"
	"time"
)

func TestRealEstateMetricReducer(t *testing.T) {
	now := time.Unix(1800000000, 0)
	r := estateMetricReport{Member: "member"}
	for i := 0; i < 8; i++ {
		at := now.Add(time.Duration(i) * 15 * time.Second)
		raw := estateMetricFixture(at)
		raw = []byte(strings.ReplaceAll(string(raw), "} 10\n", "} 12\n"))
		points, err := parseEstateMetrics(raw, "test")
		if err != nil {
			t.Fatal(err)
		}
		r.Attempts++
		r.observe(raw, points, at)
	}
	if problems := r.problems(); len(problems) > 0 {
		t.Fatal(problems)
	}
	if len(r.Series) != 32 || r.MaximumSourceAge != 0 || len(r.SampleChainSHA256) != 64 {
		t.Fatalf("unexpected summary: %+v", r)
	}
	for _, s := range r.Series {
		if s.Name == "astronomer_agent_observation_requests_total" && s.Delta != 0 {
			t.Fatal("constant counter generated calls")
		}
	}
}
func TestRealEstateMetricFailuresNeverBecomeHealthyZero(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, test := range []struct {
		name string
		raw  []byte
		at   time.Time
	}{
		{"missing", nil, now},
		{"stopped", estateMetricFixture(now), now.Add(61 * time.Second)},
		{"future", estateMetricFixture(now.Add(time.Second)), now},
		{"unavailable", []byte(strings.ReplaceAll(string(estateMetricFixture(now)), "source=\"Pod\"} 1\n", "source=\"Pod\"} 0\n")), now},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := estateMetricReport{Member: "member", Attempts: 8}
			points, err := parseEstateMetrics(test.raw, "test")
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 8; i++ {
				r.observe(test.raw, points, test.at)
			}
			if len(r.problems()) == 0 {
				t.Fatal("invalid metrics considered complete")
			}
		})
	}
}
func TestRealEstateMetricParserRejectsUnsafeSeries(t *testing.T) {
	raw := string(estateMetricFixture(time.Now()))
	for _, candidate := range []string{
		strings.Replace(raw, "process_cpu_seconds_total 5", "process_cpu_seconds_total NaN", 1),
		strings.Replace(raw, "kind=\"Pod\"", "kind=\"private-pod-name\"", 1),
		strings.Replace(raw, "source=\"Pod\"", "source=\"Pod\",token=\"private\"", 1),
		strings.Replace(raw, "astronomer_instance_id=\"test\"", "astronomer_instance_id=\"other\"", 1),
		raw + "process_cpu_seconds_total 7\n",
	} {
		if _, err := parseEstateMetrics([]byte(candidate), "test"); err == nil {
			t.Fatal("unsafe metric accepted")
		}
	}
}
func TestRealEstateMetricCounterResetAndMissingSeries(t *testing.T) {
	now := time.Now()
	r := estateMetricReport{Member: "member"}
	raw := estateMetricFixture(now)
	points, _ := parseEstateMetrics(raw, "test")
	r.observe(raw, points, now)
	changed := []byte(strings.Replace(string(raw), "process_cpu_seconds_total 5", "process_cpu_seconds_total 1", 1))
	points, _ = parseEstateMetrics(changed, "test")
	r.observe(changed, points, now.Add(time.Second))
	if r.Series["process_cpu_seconds_total"].Resets != 1 || r.Series["process_cpu_seconds_total"].Delta != 0 {
		t.Fatal("counter reset fabricated rate")
	}
	r.observe(nil, nil, now.Add(2*time.Second))
	if r.Series["process_cpu_seconds_total"].Missing != 1 {
		t.Fatal("missing series not accounted")
	}
}
