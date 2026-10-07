package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func transportPoints(start float64, values ...float64) []estatePoint {
	p := []estatePoint{{Name: estateTransportSentinel, Value: 1}, {Name: estateProcessStart, Value: start}}
	for i, value := range values {
		p = append(p, estatePoint{Name: estateTransportCounter, Counter: true, Value: value, Labels: map[string]string{"consumer": "delivery_inventory", "operation": "list", "resource": estateResources[i], "outcome": "2xx", "astronomer_instance_id": "test"}})
	}
	return p
}
func TestRealEstateTransportSparseWindowSemantics(t *testing.T) {
	now := time.Unix(1800000000, 0)
	start := float64(now.Add(-time.Hour).Unix())
	tests := []struct {
		name                 string
		first, second, third []estatePoint
		want                 *float64
		adjacent             float64
		state                string
	}{
		{"stable_zero", transportPoints(start, 8), transportPoints(start, 8), transportPoints(start, 8), transportNumber(0), 0, "complete_sampled_window"},
		{"stable_absent", transportPoints(start), transportPoints(start), transportPoints(start), transportNumber(0), 0, "complete_sampled_window"},
		{"stable_increment", transportPoints(start, 10), transportPoints(start, 12), transportPoints(start, 17), transportNumber(7), 7, "complete_sampled_window"},
		{"late_born", transportPoints(start), transportPoints(start, 90), transportPoints(start, 95), nil, 5, "incomplete_series"},
		{"missing_reappeared", transportPoints(start, 10), transportPoints(start), transportPoints(start, 20), nil, 0, "incomplete_series"},
		{"disappeared", transportPoints(start, 10), transportPoints(start, 12), transportPoints(start), nil, 2, "incomplete_series"},
		{"counter_reset", transportPoints(start, 10), transportPoints(start, 2), transportPoints(start, 5), nil, 3, "incomplete_series"},
		{"process_restart", transportPoints(start, 10), transportPoints(start+20, 2), transportPoints(start+20, 5), nil, 3, "incomplete_restart"},
		{"restart_surpassed", transportPoints(start, 10), transportPoints(start+20, 100), transportPoints(start+20, 105), nil, 5, "incomplete_restart"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := newEstateTransportReport()
			r.observe(test.first, now)
			r.observe(test.second, now.Add(15*time.Second))
			r.observe(test.third, now.Add(30*time.Second))
			if r.State != test.state || r.Total.AdjacentDelta != test.adjacent {
				t.Fatalf("state/delta: %+v", r)
			}
			if test.want == nil {
				if r.Total.WindowDelta != nil {
					t.Fatal("incomplete window manufactured delta")
				}
			} else if r.Total.WindowDelta == nil || *r.Total.WindowDelta != *test.want {
				t.Fatal("complete delta differs")
			}
			if len(r.ByConsumerOperation) != len(estateConsumers)*len(estateOperations) {
				t.Fatal("aggregate scope missing")
			}
			sum := 0.
			for _, group := range r.ByConsumerOperation {
				sum += group.AdjacentDelta
			}
			if sum != r.Total.AdjacentDelta {
				t.Fatal("aggregate conservation failed")
			}
		})
	}
}
func transportNumber(n float64) *float64 { return &n }
func TestRealEstateTransportGapSchemaAndIdentity(t *testing.T) {
	now := time.Unix(1800000000, 0)
	start := float64(now.Add(-time.Hour).Unix())
	for _, kind := range []string{"scrape_error", "missing_schema", "missing_start", "future_start", "timing_gap"} {
		t.Run(kind, func(t *testing.T) {
			r := newEstateTransportReport()
			r.observe(transportPoints(start, 10), now)
			middle := transportPoints(start, 20)
			next := now.Add(15 * time.Second)
			switch kind {
			case "scrape_error":
				r.ScrapeErrors++
			case "missing_schema":
				middle = middle[1:]
			case "missing_start":
				middle[1].Value = 0
			case "future_start":
				middle[1].Value = float64(now.Add(time.Hour).Unix())
			case "timing_gap":
				next = now.Add(time.Minute)
			}
			r.observe(middle, next)
			r.observe(transportPoints(start, 25), next.Add(15*time.Second))
			if r.Total.WindowDelta != nil || r.State == "complete_sampled_window" {
				t.Fatal("gap qualified complete")
			}
			want := 0.
			if kind == "scrape_error" || kind == "timing_gap" {
				want = 5
			}
			if r.Total.AdjacentDelta != want {
				t.Fatalf("bridged invalid boundary: %v", r.Total.AdjacentDelta)
			}
		})
	}
}
func transportMetricText(start float64, entries string) string {
	return fmt.Sprintf("# TYPE %s gauge\n%s{astronomer_instance_id=\"test\",schema=\"v1\"} 1\n# TYPE %s gauge\n%s %v\n# TYPE %s counter\n%s", estateTransportSentinel, estateTransportSentinel, estateProcessStart, estateProcessStart, start, estateTransportCounter, entries)
}
func transportMetricEntry(consumer, operation, resource, outcome, value string) string {
	return fmt.Sprintf("%s{astronomer_instance_id=\"test\",consumer=%q,operation=%q,resource=%q,outcome=%q} %s\n", estateTransportCounter, consumer, operation, resource, outcome, value)
}
func TestRealEstateTransportParserFixedCardinalityAndLabels(t *testing.T) {
	var entries strings.Builder
	for _, c := range estateConsumers {
		for _, o := range estateOperations {
			for _, r := range estateResources {
				for _, outcome := range estateOutcomes {
					entries.WriteString(transportMetricEntry(c, o, r, outcome, "0"))
				}
			}
		}
	}
	raw := transportMetricText(1, entries.String())
	points, err := parseEstateMetrics([]byte(raw), "test")
	if err != nil || len(points) != estateTransportSeriesLimit+2 {
		t.Fatalf("fixed Cartesian contract %d: %v", len(points), err)
	}
	if len(estateConsumers)*len(estateOperations)*len(estateResources)*len(estateOutcomes) != estateTransportSeriesLimit {
		t.Fatal("cardinality bound differs from enums")
	}
	valid := transportMetricText(1, transportMetricEntry("delivery_inventory", "list", "pods", "2xx", "1"))
	for _, bad := range []string{
		strings.Replace(valid, "consumer=\"delivery_inventory\"", "consumer=\"unbounded-name\"", 1),
		strings.Replace(valid, "operation=\"list\"", "operation=\"namespace-secret\"", 1),
		strings.Replace(valid, "resource=\"pods\"", "resource=\"secrets\"", 1),
		strings.Replace(valid, "outcome=\"2xx\"", "outcome=\"url-error\"", 1),
		strings.Replace(valid, "schema=\"v1\"", "schema=\"v2\"", 1),
		strings.Replace(valid, "} 1\n", "} 0\n", 1),
		strings.Replace(valid, estateTransportCounter+" counter", estateTransportCounter+" gauge", 1),
		valid + transportMetricEntry("delivery_inventory", "list", "pods", "2xx", "2"),
		transportMetricText(1, transportMetricEntry("delivery_inventory", "list", "pods", "2xx", "1.5")),
		transportMetricText(1, transportMetricEntry("delivery_inventory", "list", "pods", "2xx", "9007199254740992")),
	} {
		if _, err := parseEstateMetrics([]byte(bad), "test"); err == nil {
			t.Fatal("unbounded or invalid transport sample accepted")
		}
	}
}
func TestRealEstateTransportDoesNotQualifyMissingFreshness(t *testing.T) {
	now := time.Unix(1800000000, 0)
	raw := []byte(transportMetricText(float64(now.Add(-time.Hour).Unix()), transportMetricEntry("delivery_inventory", "list", "pods", "2xx", "10")))
	points, err := parseEstateMetrics(raw, "test")
	if err != nil {
		t.Fatal(err)
	}
	r := estateMetricReport{}
	for i := 0; i < 10; i++ {
		r.Attempts++
		r.observe(raw, points, now.Add(time.Duration(i)*15*time.Second))
	}
	if r.Transport.Total.WindowDelta == nil || *r.Transport.Total.WindowDelta != 0 {
		t.Fatal("transport sample reduction absent")
	}
	if r.MissingRequired == 0 || len(r.problems()) == 0 {
		t.Fatal("transport-only baseline became freshness qualified")
	}
	r.recordScrapeError()
	if r.Transport.Total.WindowDelta != nil || r.Transport.State != "incomplete_scrapes" {
		t.Fatal("trailing scrape error ignored")
	}
}

func TestRealEstateTransportFailedScrapesRemainUnavailable(t *testing.T) {
	for _, firstValid := range []bool{false, true} {
		t.Run(fmt.Sprint(firstValid), func(t *testing.T) {
			r := estateMetricReport{}
			now := time.Unix(1800000000, 0)
			if firstValid {
				r.Attempts++
				r.observe(nil, transportPoints(float64(now.Add(-time.Hour).Unix()), 9), now)
			}
			for i := 0; i < 3; i++ {
				r.Attempts++
				r.recordScrapeError()
			}
			if r.Transport == nil || r.Transport.Total.WindowDelta != nil || r.Transport.ScrapeErrors != 3 || r.Errors != 3 {
				t.Fatal("failed scrape window manufactured transport zero")
			}
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var serialized struct {
				Transport struct {
					Total struct {
						Delta *float64 `json:"window_delta"`
						State string   `json:"coverage_state"`
					} `json:"total_family"`
				} `json:"transport_evidence"`
			}
			if json.Unmarshal(raw, &serialized) != nil || serialized.Transport.Total.Delta != nil || serialized.Transport.Total.State == "complete_sampled_window" {
				t.Fatal("unavailable transport serialized as complete")
			}
		})
	}
}
func TestRealEstateTransportCoexistsWithObservationBoundAndCountsErrors(t *testing.T) {
	now := time.Unix(1800000000, 0)
	first := transportMetricText(float64(now.Add(-time.Hour).Unix()), transportMetricEntry("delivery_assignment_observation", "get", "gitrepositories", "transport_error", "2"))
	raw := append(estateMetricFixture(now), []byte(first)...)
	points, err := parseEstateMetrics(raw, "test")
	if err != nil {
		t.Fatal(err)
	}
	r := estateMetricReport{}
	r.Attempts++
	r.observe(raw, points, now)
	next := strings.Replace(first, "} 2\n", "} 5\n", 1)
	raw = append(estateMetricFixture(now.Add(15*time.Second)), []byte(next)...)
	points, err = parseEstateMetrics(raw, "test")
	if err != nil {
		t.Fatal(err)
	}
	r.Attempts++
	r.observe(raw, points, now.Add(15*time.Second))
	if r.MissingRequired != 0 || r.InvalidFreshness != 0 {
		t.Fatal("transport adoption damaged existing freshness samples")
	}
	if r.Transport.Total.WindowDelta == nil || *r.Transport.Total.WindowDelta != 3 {
		t.Fatal("transport errors omitted from family total")
	}
	for _, s := range r.Transport.Series {
		if s.Outcome != "transport_error" || s.WindowDelta == nil || *s.WindowDelta != 3 {
			t.Fatal("outcome attribution lost")
		}
	}
}
func TestRealEstateTransportMissingSentinelDoesNotMeanZero(t *testing.T) {
	now := time.Unix(1800000000, 0)
	r := newEstateTransportReport()
	for i := 0; i < 2; i++ {
		r.observe([]estatePoint{{Name: estateProcessStart, Value: float64(now.Add(-time.Hour).Unix())}}, now.Add(time.Duration(i)*15*time.Second))
	}
	if r.State != "unavailable_schema" || r.ObservedSchema != "" || r.Total.WindowDelta != nil {
		t.Fatal("missing sentinel treated as installed zero family")
	}
}
