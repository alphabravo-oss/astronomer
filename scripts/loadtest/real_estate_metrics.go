package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const estateObservationPrefix = "astronomer_agent_delivery_observation_"

var estateKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "Pod", "PersistentVolumeClaim", "StorageClass"}
var estateSources = append(append([]string{}, estateKinds...), "controllers")

type estatePoint struct {
	Name    string            `json:"name"`
	Labels  map[string]string `json:"labels,omitempty"`
	Value   float64           `json:"value"`
	Counter bool              `json:"counter"`
}
type estateSeries struct {
	Name    string            `json:"name"`
	Labels  map[string]string `json:"labels,omitempty"`
	Samples int               `json:"samples"`
	Missing int               `json:"missing_samples"`
	Resets  int               `json:"resets"`
	First   float64           `json:"first"`
	Last    float64           `json:"last"`
	Minimum float64           `json:"minimum"`
	Maximum float64           `json:"maximum"`
	Delta   float64           `json:"delta"`
	FirstAt time.Time         `json:"first_at"`
	LastAt  time.Time         `json:"last_at"`
}
type estateMetricReport struct {
	Transport         *estateTransportReport   `json:"transport_evidence"`
	BoundarySkipped   int                      `json:"boundary_scrapes_excluded"`
	TargetSHA256      string                   `json:"target_sha256"`
	Member            string                   `json:"member"`
	Attempts          int                      `json:"attempts"`
	Samples           int                      `json:"samples"`
	Errors            int                      `json:"errors"`
	MissingRequired   int                      `json:"missing_required"`
	InvalidFreshness  int                      `json:"invalid_freshness"`
	Unavailable       int                      `json:"unavailable_source_samples"`
	MaximumSourceAge  float64                  `json:"maximum_source_age_seconds"`
	Series            map[string]*estateSeries `json:"series"`
	SampleChainSHA256 string                   `json:"sample_chain_sha256"`
}

func parseEstateMetrics(raw []byte, instance string) ([]estatePoint, error) {
	if len(raw) > 4<<20 {
		return nil, errors.New("metrics body exceeds limit")
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(raw)))
	if err != nil {
		return nil, errors.New("invalid Prometheus metrics")
	}
	points := []estatePoint{}
	seen := map[string]bool{}
	transportCount, otherCount, markerCount := 0, 0, 0
	for name, f := range families {
		if !estateMetricAllowed(name) {
			continue
		}
		for _, m := range f.Metric {
			labels := map[string]string{}
			for _, l := range m.Label {
				if _, ok := labels[l.GetName()]; ok {
					return nil, errors.New("duplicate metric label")
				}
				labels[l.GetName()] = l.GetValue()
			}
			if err := estateMetricLabels(name, labels, instance); err != nil {
				return nil, err
			}
			p := estatePoint{Name: name, Labels: labels}
			switch f.GetType() {
			case dto.MetricType_COUNTER:
				p.Value = m.GetCounter().GetValue()
				p.Counter = true
			case dto.MetricType_GAUGE:
				p.Value = m.GetGauge().GetValue()
			default:
				return nil, errors.New("unexpected metric type")
			}
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) || p.Value < 0 {
				return nil, errors.New("invalid metric value")
			}
			expectedCounter := name == "astronomer_agent_observation_requests_total" || name == "process_cpu_seconds_total" || name == estateTransportCounter
			if p.Counter != expectedCounter {
				return nil, errors.New("incorrect metric type")
			}
			if err := estateTransportValue(p); err != nil {
				return nil, err
			}
			key := estatePointKey(p)
			if seen[key] {
				return nil, errors.New("duplicate metric series")
			}
			seen[key] = true
			points = append(points, p)
			switch name {
			case estateTransportCounter:
				transportCount++
			case estateTransportSentinel, estateProcessStart:
				markerCount++
			default:
				otherCount++
			}
			if transportCount > estateTransportSeriesLimit || otherCount > estateLegacySeriesLimit || markerCount > 2 {
				return nil, errors.New("metric cardinality exceeds bound")
			}

		}
	}
	sort.Slice(points, func(i, j int) bool { return estatePointKey(points[i]) < estatePointKey(points[j]) })
	return points, nil
}
func estateMetricAllowed(n string) bool {
	return estateTransportMetric(n) || slices.Contains([]string{"astronomer_agent_observation_requests_total", "process_cpu_seconds_total", "process_resident_memory_bytes", "process_open_fds", "go_memstats_heap_alloc_bytes", "go_goroutines", estateObservationPrefix + "source_available", estateObservationPrefix + "sampled_at_timestamp_seconds", estateObservationPrefix + "observed_at_timestamp_seconds"}, n)
}
func estateMetricLabels(n string, labels map[string]string, instance string) error {
	if estateTransportMetric(n) {
		return estateTransportLabels(n, labels, instance)
	}
	if !strings.HasPrefix(n, "astronomer_") {
		if len(labels) != 0 {
			return errors.New("unexpected process metric labels")
		}
		return nil
	}
	if labels["astronomer_instance_id"] != instance {
		return errors.New("unexpected metric producer identity")
	}
	if n == "astronomer_agent_observation_requests_total" {
		if len(labels) != 4 || !slices.Contains(append(append([]string{}, estateKinds...), "GitRepository", "OCIRepository", "HelmRepository", "Kustomization", "HelmRelease"), labels["kind"]) || !slices.Contains([]string{"list", "watch"}, labels["verb"]) || !slices.Contains([]string{"success", "error", "denied", "expired", "canceled"}, labels["outcome"]) {
			return errors.New("unexpected observation request labels")
		}
	} else if len(labels) != 2 || !slices.Contains(estateSources, labels["source"]) {
		return errors.New("unexpected observation source labels")
	}
	return nil
}
func estatePointKey(p estatePoint) string {
	keys := make([]string, 0, len(p.Labels))
	for k := range p.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	key := p.Name
	for _, k := range keys {
		key += "|" + k + "=" + p.Labels[k]
	}
	return key
}
func (r *estateMetricReport) observe(raw []byte, points []estatePoint, now time.Time) {
	r.Samples++
	if r.Transport == nil {
		r.Transport = newEstateTransportReport()
	}
	r.Transport.observe(points, now)
	if r.Series == nil {
		r.Series = map[string]*estateSeries{}
	}
	h := sha256.New()
	h.Write([]byte(r.SampleChainSHA256))
	h.Write([]byte(now.UTC().Format(time.RFC3339Nano)))
	h.Write(raw)
	r.SampleChainSHA256 = hex.EncodeToString(h.Sum(nil))
	seen := map[string]bool{}
	values := map[string]float64{}
	for _, p := range points {
		if estateTransportMetric(p.Name) {
			continue
		}
		key := estatePointKey(p)
		seen[key] = true
		values[p.Name+"/"+p.Labels["source"]] = p.Value
		s := r.Series[key]
		if s == nil {
			s = &estateSeries{Name: p.Name, Labels: p.Labels, First: p.Value, Last: p.Value, Minimum: p.Value, Maximum: p.Value, FirstAt: now, Missing: r.Samples - 1}
			r.Series[key] = s
		}
		if p.Counter && s.Samples > 0 {
			if p.Value < s.Last {
				s.Resets++
			} else {
				s.Delta += p.Value - s.Last
			}
		}
		s.Samples++
		s.Last = p.Value
		s.LastAt = now
		s.Minimum = min(s.Minimum, p.Value)
		s.Maximum = max(s.Maximum, p.Value)
	}
	for key, s := range r.Series {
		if !seen[key] {
			s.Missing++
		}
	}
	for _, n := range []string{"process_cpu_seconds_total", "process_resident_memory_bytes", "process_open_fds", "go_memstats_heap_alloc_bytes", "go_goroutines"} {
		if _, ok := values[n+"/"]; !ok {
			r.MissingRequired++
		}
	}
	for _, k := range estateKinds {
		found := false
		for _, p := range points {
			if p.Name == "astronomer_agent_observation_requests_total" && p.Labels["kind"] == k && p.Labels["verb"] == "list" && p.Labels["outcome"] == "success" {
				found = true
			}
		}
		if !found {
			r.MissingRequired++
		}
	}
	seconds := float64(now.UnixNano()) / 1e9
	for _, source := range estateSources {
		sampled, ok1 := values[estateObservationPrefix+"sampled_at_timestamp_seconds/"+source]
		observed, ok2 := values[estateObservationPrefix+"observed_at_timestamp_seconds/"+source]
		available, ok3 := values[estateObservationPrefix+"source_available/"+source]
		if !ok1 || !ok2 || !ok3 {
			r.MissingRequired++
			continue
		}
		if sampled <= 0 || sampled > seconds || seconds-sampled > 60 || observed <= 0 || observed > seconds || seconds-observed > 240 || (available != 0 && available != 1) {
			r.InvalidFreshness++
			continue
		}
		r.MaximumSourceAge = max(r.MaximumSourceAge, seconds-observed)
		if available != 1 {
			r.Unavailable++
		}
	}
}
func (r estateMetricReport) problems() []string {
	problems := []string{}
	if r.Samples < minimumLeakSamples || r.Attempts == 0 || float64(r.Samples)/float64(max(1, r.Attempts)) < 0.98 || r.Errors > 0 || r.MissingRequired > 0 {
		problems = append(problems, r.Member+": incomplete metrics coverage")
	}
	if r.InvalidFreshness > 0 || r.Unavailable > 0 {
		problems = append(problems, r.Member+": stale, invalid or unavailable source samples")
	}
	for _, s := range r.Series {
		if s.Name == "astronomer_agent_observation_requests_total" && s.Labels["outcome"] != "success" && s.Delta > 0 {
			problems = append(problems, r.Member+": observed source request failures")
		}
		if s.Resets > 0 || s.Missing > 0 {
			problems = append(problems, r.Member+": reset or missing series; deltas incomplete")
			break
		}
	}
	for name, ratio := range map[string]float64{"go_goroutines": 1.5, "go_memstats_heap_alloc_bytes": 1.5, "process_open_fds": 1.25} {
		s := r.Series[name]
		if s != nil && s.First > 0 && s.Last/s.First > ratio {
			problems = append(problems, fmt.Sprintf("%s: %s growth exceeds existing budget", r.Member, name))
		}
	}
	if s := r.Series["process_open_fds"]; s != nil && s.Last-s.First > 64 {
		problems = append(problems, r.Member+": open FD growth exceeds existing budget")
	}
	return problems
}

func (r *estateMetricReport) recordScrapeError() {
	r.Errors++
	if r.Transport == nil {
		r.Transport = newEstateTransportReport()
	}
	r.Transport.ScrapeErrors++
	r.Transport.refresh()
}
