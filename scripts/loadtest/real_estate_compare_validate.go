package main

import (
	"errors"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

var estateCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)

func estateCount(n int) bool      { return n >= 0 && n <= 20000000 }
func estateFinite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 }
func validateEstateComparisonReport(r estateReport) error {
	invalid := errors.New("invalid_report_contract")
	if r.Schema != "astronomer-real-estate-report-v2" || r.Timing != estateTiming || r.Scope != "engineering_preprovisioned_observation" || r.Qualified || !slices.Contains([]string{"failed", "incomplete"}, r.Verdict) {
		return invalid
	}
	if !estateDigest.MatchString(r.FixtureSHA256) || !estateDigest.MatchString(r.ManifestSHA256) || r.Members < 2 || r.Members > 10 || !slices.Contains([]int{1, 10, 100}, r.Tier) {
		return invalid
	}
	if (!estateCommit.MatchString(r.DriverRevision) && r.DriverRevision != "unavailable") || !slices.Contains([]string{"true", "false", "unavailable"}, r.DriverModified) {
		return invalid
	}
	env := r.Environment
	if !estateName.MatchString(env.ID) || !estateCommit.MatchString(env.Commit) || len(env.KubernetesVersion) > 128 || env.KubernetesVersion == "" || len(env.Replicas) != 2 || env.Replicas["server"] < 1 || env.Replicas["worker"] < 1 {
		return invalid
	}
	for _, d := range []string{env.ImagesSHA256, env.ValuesSHA256, env.DatasetSHA256, env.HardwareSHA256} {
		if !estateDigest.MatchString(d) {
			return invalid
		}
	}
	manifest := estateManifest{Phases: r.Definitions, Search: r.Search, Members: make([]estateMember, r.Members), Tier: r.Tier}
	if manifest.validatePhases() != nil || (r.Search != nil && r.Search.TokenFile != "") || len(r.Phases) > len(r.Definitions) || len(r.Problems) > 16384 || len(r.Pending) > 64 {
		return invalid
	}
	for _, set := range [][]estateVerification{r.StartVerification, r.EndVerification} {
		seen := map[string]bool{}
		if len(set) > r.Members {
			return invalid
		}
		for _, v := range set {
			if !estateName.MatchString(v.Member) || seen[v.Member] || len(v.Census) > 3 {
				return invalid
			}
			seen[v.Member] = true
		}
	}
	for i, p := range r.Phases {
		if p.Spec != r.Definitions[i] || len(p.Problems) > 4096 || len(p.ScopeChecks) > 1000 || len(p.Metrics) > r.Members {
			return invalid
		}
		if !validEstateWindow(p.Warmup) || !validEstateWindow(p.WarmupDrain) || !validEstateWindow(p.Measurement) || !validEstateWindow(p.Drain) {
			return invalid
		}
		if p.Totals != estateTotals(p.Spec, p.HTTP) {
			return errors.New("request_totals_inconsistent")
		}
		for _, rows := range [][]estateRequestResult{p.HTTP, p.WarmupHTTP} {
			if err := validateEstateHTTPRows(rows, r.Members, r.Tier); err != nil {
				return err
			}
		}
		seen := map[string]bool{}
		for _, m := range p.Metrics {
			if !estateName.MatchString(m.Member) || seen[m.Member] || !estateDigest.MatchString(m.TargetSHA256) || len(m.Series) > estateLegacySeriesLimit {
				return invalid
			}
			seen[m.Member] = true
			for _, n := range []int{m.Attempts, m.Samples, m.Errors, m.MissingRequired, m.InvalidFreshness, m.Unavailable, m.BoundarySkipped} {
				if !estateCount(n) {
					return invalid
				}
			}
			if m.Samples+m.Errors != m.Attempts || !estateFinite(m.MaximumSourceAge) {
				return invalid
			}
			identity := ""
			for key, s := range m.Series {
				if s == nil || len(key) > 512 || len(s.Labels) > 4 || !estateMetricAllowed(s.Name) || estateTransportMetric(s.Name) {
					return invalid
				}
				instance := s.Labels["astronomer_instance_id"]
				if instance != "" {
					if identity != "" && identity != instance {
						return invalid
					}
					identity = instance
				}
				if stringsMetricIdentityInvalid(s.Name, instance) || estateMetricLabels(s.Name, s.Labels, instance) != nil {
					return invalid
				}
				if key != estatePointKey(estatePoint{Name: s.Name, Labels: s.Labels}) {
					return invalid
				}
				for _, v := range []float64{s.First, s.Last, s.Minimum, s.Maximum, s.Delta} {
					if !estateFinite(v) {
						return invalid
					}
				}
				if s.Samples < 1 || s.FirstAt.IsZero() || s.LastAt.Before(s.FirstAt) || s.FirstAt.Before(p.Measurement.Start) || s.LastAt.After(p.Measurement.End) || s.Minimum > s.First || s.Minimum > s.Last || s.Maximum < s.First || s.Maximum < s.Last || s.Samples+s.Missing > m.Samples {
					return invalid
				}
				if !estateCount(s.Samples) || !estateCount(s.Missing) || !estateCount(s.Resets) || s.Samples > m.Samples {
					return invalid
				}
			}
			if m.Transport != nil {
				if m.Transport.Samples != m.Samples || m.Transport.ScrapeErrors != m.Errors {
					return errors.New("transport_parent_counts_inconsistent")
				}
				for key := range m.Transport.Series {
					if identity != "" && !strings.Contains(key, "|astronomer_instance_id="+identity+"|") {
						return errors.New("metric_instance_inconsistent")
					}
				}
				if _, err := recomputeEstateTransport(m.Transport, p.Measurement); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func stringsMetricIdentityInvalid(name, instance string) bool {
	return len(name) >= 11 && name[:11] == "astronomer_" && !estateName.MatchString(instance)
}
func validEstateWindow(w estateWindow) bool {
	if w.Start.IsZero() || w.End.IsZero() {
		return w.Start.IsZero() && w.End.IsZero()
	}
	return !w.End.Before(w.Start) && w.End.Sub(w.Start) <= 24*time.Hour
}
func validateEstateHTTPRows(rows []estateRequestResult, members, tier int) error {
	invalid := errors.New("invalid_http_accounting")
	if len(rows) > members*(tier+2) {
		return invalid
	}
	seen := map[string]bool{}
	for _, h := range rows {
		key := h.Member + "/" + h.Scenario + "/" + h.Assignment
		if seen[key] || (h.Member != "" && !estateName.MatchString(h.Member)) || (h.Assignment != "" && !validEstateUUID(h.Assignment)) || !slices.Contains([]string{"namespace_pods", "namespace_deployments", "namespace_services", "delivery_inventory", "delivery_list", "delivery_detail", "search_pods", "search_deployments", "search_services"}, h.Scenario) {
			return invalid
		}
		seen[key] = true
		for _, n := range []int{h.Scheduled, h.Completed, h.Success, h.Failed} {
			if !estateCount(n) {
				return invalid
			}
		}
		if h.Completed != h.Scheduled || h.Success+h.Failed != h.Completed || len(h.Diagnostics) > 32 {
			return invalid
		}
		diagnostics := 0
		for key, n := range h.Diagnostics {
			if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(key) || !estateCount(n) {
				return invalid
			}
			diagnostics += n
		}
		if diagnostics != h.Failed {
			return invalid
		}
		for _, hist := range []estateLatencyDistribution{h.HeaderLatency, h.FullResponseLatency} {
			if !reflect.DeepEqual(hist.UpperBoundsMS, estateLatencyBoundsMS[:]) || len(hist.Counts) != len(estateLatencyBoundsMS) || hist.Samples != uint64(h.Completed) || hist.Overflow > hist.Samples {
				return invalid
			}
			rebuilt := estateLatencyHistogram{total: hist.Samples, overflow: hist.Overflow}
			var sum uint64
			for i, n := range hist.Counts {
				if n > hist.Samples {
					return invalid
				}
				sum += n
				rebuilt.counts[i] = n
			}
			if sum+hist.Overflow != hist.Samples || !sameEstateNumber(rebuilt.distribution().P99UpperBoundMS, hist.P99UpperBoundMS) {
				return invalid
			}
		}
	}
	return nil
}
func sameEstateNumber(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return estateFinite(*a) && estateFinite(*b) && *a == *b
}
