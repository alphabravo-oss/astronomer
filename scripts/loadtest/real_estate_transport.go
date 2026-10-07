package main

import (
	"sort"
	"time"
)

type estateTransportSeries struct {
	Consumer      string   `json:"consumer"`
	Operation     string   `json:"operation"`
	Resource      string   `json:"resource"`
	Outcome       string   `json:"outcome"`
	Samples       int      `json:"samples"`
	LateBorn      bool     `json:"late_born"`
	Missing       int      `json:"missing_samples"`
	Reappeared    int      `json:"reappearances"`
	Resets        int      `json:"observed_counter_resets"`
	First         float64  `json:"first"`
	Last          float64  `json:"last"`
	AdjacentDelta float64  `json:"observed_adjacent_delta"`
	WindowDelta   *float64 `json:"window_delta"`
	State         string   `json:"coverage_state"`
	lastSample    int
	lastProcess   float64
	lastErrors    int
}
type estateTransportAggregate struct {
	Consumer      string   `json:"consumer,omitempty"`
	Operation     string   `json:"operation,omitempty"`
	Series        int      `json:"series"`
	AdjacentDelta float64  `json:"observed_adjacent_delta"`
	WindowDelta   *float64 `json:"window_delta"`
	State         string   `json:"coverage_state"`
}
type estateTransportReport struct {
	Scope               string                            `json:"scope"`
	Schema              string                            `json:"expected_schema"`
	Samples             int                               `json:"samples"`
	ObservedSchema      string                            `json:"observed_schema,omitempty"`
	SchemaSamples       int                               `json:"valid_schema_samples"`
	ProcessSamples      int                               `json:"valid_process_start_samples"`
	FirstAt             time.Time                         `json:"first_at"`
	LastAt              time.Time                         `json:"last_at"`
	FirstProcessStart   float64                           `json:"first_process_start_seconds"`
	LastProcessStart    float64                           `json:"last_process_start_seconds"`
	Restarts            int                               `json:"observed_process_start_changes"`
	ScrapeErrors        int                               `json:"scrape_errors"`
	TimingGaps          int                               `json:"sample_timing_gaps"`
	State               string                            `json:"coverage_state"`
	Series              map[string]*estateTransportSeries `json:"series"`
	ByConsumerOperation []estateTransportAggregate        `json:"by_consumer_operation"`
	Total               estateTransportAggregate          `json:"total_family"`
}

func newEstateTransportReport() *estateTransportReport {
	return &estateTransportReport{Scope: "instrumented_shared_client_family", Schema: "v1", State: "unavailable_no_samples", Series: map[string]*estateTransportSeries{}}
}
func (r *estateTransportReport) observe(points []estatePoint, now time.Time) {
	r.Samples++
	timingGap := !r.LastAt.IsZero() && (now.Before(r.LastAt) || now.Sub(r.LastAt) > 2*metricsScrape)
	if timingGap {
		r.TimingGaps++
	}
	if r.FirstAt.IsZero() {
		r.FirstAt = now
	}
	r.LastAt = now
	marker := false
	process := float64(0)
	for _, p := range points {
		if p.Name == estateTransportSentinel && p.Value == 1 {
			marker = true
		}
		if p.Name == estateProcessStart {
			process = p.Value
		}
	}
	processValid := process > 0 && process <= float64(now.UnixNano())/1e9
	if marker {
		r.SchemaSamples++
		r.ObservedSchema = "v1"
	}
	if processValid {
		r.ProcessSamples++
		if r.FirstProcessStart == 0 {
			r.FirstProcessStart = process
		}
		if r.LastProcessStart != 0 && process != r.LastProcessStart {
			r.Restarts++
		}
		r.LastProcessStart = process
	}
	seen := map[string]bool{}
	for _, p := range points {
		if p.Name != estateTransportCounter {
			continue
		}
		key := estatePointKey(p)
		seen[key] = true
		s := r.Series[key]
		if s == nil {
			s = &estateTransportSeries{Consumer: p.Labels["consumer"], Operation: p.Labels["operation"], Resource: p.Labels["resource"], Outcome: p.Labels["outcome"], First: p.Value, Last: p.Value, LateBorn: r.Samples > 1}
			r.Series[key] = s
		}
		if s.Samples > 0 {
			if s.lastSample != r.Samples-1 {
				s.Reappeared++
			}
			if p.Value < s.Last {
				s.Resets++
			}
			// Never bridge a missing/failed scrape, restart, unavailable identity or
			// late sample boundary, even when the new value surpassed the old value.
			if s.lastSample == r.Samples-1 && marker && processValid && s.lastProcess == process && s.lastErrors == r.ScrapeErrors && !timingGap && p.Value >= s.Last {
				s.AdjacentDelta += p.Value - s.Last
			}
		}
		s.Samples++
		s.Last = p.Value
		s.lastSample = r.Samples
		s.lastErrors = r.ScrapeErrors
		s.lastProcess = 0
		if marker && processValid {
			s.lastProcess = process
		}
	}
	for key, s := range r.Series {
		if !seen[key] {
			s.Missing++
		}
	}
	r.refresh()
}
func (r estateTransportReport) globalState() string {
	switch {
	case r.Samples == 0:
		return "unavailable_no_samples"
	case r.ScrapeErrors > 0:
		return "incomplete_scrapes"
	case r.SchemaSamples != r.Samples:
		return "unavailable_schema"
	case r.ProcessSamples != r.Samples:
		return "unavailable_process_start"
	case r.Restarts > 0:
		return "incomplete_restart"
	case r.TimingGaps > 0:
		return "incomplete_sample_timing"
	case r.Samples < 2:
		return "insufficient_samples"
	default:
		return "complete_sampled_window"
	}
}
func (r *estateTransportReport) refresh() {
	state := r.globalState()
	r.State = state
	groups := map[string]*estateTransportAggregate{}
	for _, consumer := range estateConsumers {
		for _, operation := range estateOperations {
			groups[consumer+"/"+operation] = &estateTransportAggregate{Consumer: consumer, Operation: operation, State: state}
		}
	}
	r.Total = estateTransportAggregate{State: state}
	for _, s := range r.Series {
		s.State = state
		s.WindowDelta = nil
		if state == "complete_sampled_window" && (s.LateBorn || s.Missing > 0 || s.Reappeared > 0 || s.Resets > 0) {
			s.State = "incomplete_series"
		}
		if s.State == "complete_sampled_window" {
			value := s.AdjacentDelta
			s.WindowDelta = &value
		}
		group := groups[s.Consumer+"/"+s.Operation]
		group.Series++
		group.AdjacentDelta += s.AdjacentDelta
		r.Total.Series++
		r.Total.AdjacentDelta += s.AdjacentDelta
		if s.State != state {
			group.State = s.State
			r.Total.State = s.State
			r.State = s.State
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	r.ByConsumerOperation = nil
	for _, key := range keys {
		group := groups[key]
		if group.State == "complete_sampled_window" {
			value := group.AdjacentDelta
			group.WindowDelta = &value
		}
		r.ByConsumerOperation = append(r.ByConsumerOperation, *group)
	}
	if r.Total.State == "complete_sampled_window" {
		value := r.Total.AdjacentDelta
		r.Total.WindowDelta = &value
	}
}
