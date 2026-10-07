package main

import (
	"errors"
	"reflect"
	"slices"
	"strings"
)

// Recompute complete deltas from endpoint values. Partial adjacent deltas can
// only be checked for internal conservation: raw scrape samples are absent.
func recomputeEstateTransport(input *estateTransportReport, window estateWindow) (*estateTransportReport, error) {
	invalid := errors.New("invalid_transport_evidence")
	if input == nil {
		return nil, nil
	}
	if input.Scope != "instrumented_shared_client_family" || input.Schema != "v1" || len(input.Series) > estateTransportSeriesLimit || len(input.ByConsumerOperation) != len(estateConsumers)*len(estateOperations) {
		return nil, invalid
	}
	for _, n := range []int{input.Samples, input.SchemaSamples, input.ProcessSamples, input.Restarts, input.ScrapeErrors, input.TimingGaps} {
		if !estateCount(n) {
			return nil, invalid
		}
	}
	if input.SchemaSamples > input.Samples || input.ProcessSamples > input.Samples || input.Restarts > input.ProcessSamples || input.TimingGaps > input.Samples {
		return nil, invalid
	}
	if (input.SchemaSamples == 0 && input.ObservedSchema != "") || (input.SchemaSamples > 0 && input.ObservedSchema != "v1") {
		return nil, invalid
	}
	if !estateFinite(input.FirstProcessStart) || !estateFinite(input.LastProcessStart) {
		return nil, invalid
	}
	if input.Samples == 0 {
		if !input.FirstAt.IsZero() || !input.LastAt.IsZero() || len(input.Series) != 0 {
			return nil, invalid
		}
	} else if input.FirstAt.IsZero() || input.LastAt.Before(input.FirstAt) || input.FirstAt.Before(window.Start) || input.LastAt.After(window.End) {
		return nil, invalid
	}
	if input.globalState() == "complete_sampled_window" && (input.FirstProcessStart <= 0 || input.FirstProcessStart != input.LastProcessStart || input.FirstProcessStart > float64(input.FirstAt.UnixNano())/1e9 || !input.LastAt.After(input.FirstAt)) {
		return nil, invalid
	}
	clone := *input
	clone.Series = map[string]*estateTransportSeries{}
	instance := ""
	for key, series := range input.Series {
		if series == nil {
			return nil, invalid
		}
		if !slices.Contains(estateConsumers, series.Consumer) || !slices.Contains(estateOperations, series.Operation) || !slices.Contains(estateResources, series.Resource) || !slices.Contains(estateOutcomes, series.Outcome) {
			return nil, invalid
		}
		labels := map[string]string{}
		parts := strings.Split(key, "|")
		if len(parts) != 6 || parts[0] != estateTransportCounter {
			return nil, invalid
		}
		for _, part := range parts[1:] {
			pair := strings.SplitN(part, "=", 2)
			if len(pair) != 2 || labels[pair[0]] != "" {
				return nil, invalid
			}
			labels[pair[0]] = pair[1]
		}
		id := labels["astronomer_instance_id"]
		if !estateName.MatchString(id) || estateTransportLabels(estateTransportCounter, labels, id) != nil || estatePointKey(estatePoint{Name: estateTransportCounter, Labels: labels}) != key {
			return nil, invalid
		}
		if instance != "" && id != instance {
			return nil, invalid
		}
		instance = id
		if labels["consumer"] != series.Consumer || labels["operation"] != series.Operation || labels["resource"] != series.Resource || labels["outcome"] != series.Outcome {
			return nil, invalid
		}
		for _, n := range []int{series.Samples, series.Missing, series.Reappeared, series.Resets} {
			if !estateCount(n) {
				return nil, invalid
			}
		}
		if series.Samples < 1 || series.Samples+series.Missing > input.Samples || series.Reappeared > series.Missing || series.Resets >= series.Samples {
			return nil, invalid
		}
		if series.LateBorn == (series.Samples+series.Missing == input.Samples) {
			return nil, invalid
		}
		for _, n := range []float64{series.First, series.Last, series.AdjacentDelta} {
			if !estateFinite(n) || estateTransportValue(estatePoint{Name: estateTransportCounter, Value: n}) != nil {
				return nil, invalid
			}
		}
		copy := *series
		if input.globalState() == "complete_sampled_window" && !series.LateBorn && series.Missing == 0 && series.Resets == 0 && series.Reappeared == 0 {
			if series.Last < series.First || series.Samples != input.Samples {
				return nil, invalid
			}
			copy.AdjacentDelta = series.Last - series.First
			if copy.AdjacentDelta != series.AdjacentDelta {
				return nil, errors.New("transport_delta_inconsistent")
			}
		}
		clone.Series[key] = &copy
	}
	clone.refresh()
	for key, s := range clone.Series {
		original := input.Series[key]
		if s.State != original.State || !sameEstateNumber(s.WindowDelta, original.WindowDelta) {
			return nil, errors.New("transport_series_summary_inconsistent")
		}
	}
	if clone.State != input.State || !reflect.DeepEqual(clone.Total, input.Total) {
		return nil, errors.New("transport_total_inconsistent")
	}
	groups := map[string]estateTransportAggregate{}
	for _, group := range input.ByConsumerOperation {
		key := group.Consumer + "/" + group.Operation
		if _, duplicate := groups[key]; duplicate {
			return nil, invalid
		}
		groups[key] = group
	}
	for _, group := range clone.ByConsumerOperation {
		if expected, ok := groups[group.Consumer+"/"+group.Operation]; !ok || !reflect.DeepEqual(group, expected) {
			return nil, errors.New("transport_group_inconsistent")
		}
	}
	return &clone, nil
}
