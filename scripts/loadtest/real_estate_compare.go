package main

import (
	"crypto/sha256"
	"fmt"
	"math"
	"slices"
	"time"
)

type estateComparison struct {
	Schema               string                     `json:"schema_version"`
	Qualified            bool                       `json:"qualified"`
	OptimizationEligible bool                       `json:"optimization_criteria_eligible"`
	Evidence             string                     `json:"evidence_scope"`
	BaselineSHA256       string                     `json:"baseline_report_sha256"`
	CandidateSHA256      string                     `json:"candidate_report_sha256"`
	BaselineCommit       string                     `json:"baseline_commit"`
	CandidateCommit      string                     `json:"candidate_commit"`
	BaselineDriver       string                     `json:"baseline_driver_revision"`
	CandidateDriver      string                     `json:"candidate_driver_revision"`
	Provenance           estateComparisonProvenance `json:"provenance"`
	Blockers             []string                   `json:"blockers"`
	Rows                 []estateComparisonRow      `json:"member_phases"`
}
type estateComparisonRow struct {
	PhaseOrdinal        int                      `json:"phase_ordinal"`
	PhaseSHA256         string                   `json:"phase_sha256"`
	MemberOrdinal       int                      `json:"member_ordinal"`
	MemberSHA256        string                   `json:"member_sha256"`
	BaselineWork        estateComparisonWork     `json:"baseline_phase_work"`
	CandidateWork       estateComparisonWork     `json:"candidate_phase_work"`
	BaselineMemberWork  estateComparisonWork     `json:"baseline_member_work"`
	CandidateMemberWork estateComparisonWork     `json:"candidate_member_work"`
	Eligible            bool                     `json:"optimization_criteria_eligible"`
	Blockers            []string                 `json:"blockers"`
	Metrics             []estateComparisonMetric `json:"transport"`
}
type estateComparisonWork struct {
	Requested     int      `json:"requested"`
	Completed     int      `json:"completed"`
	Success       int      `json:"success"`
	Failed        int      `json:"failed"`
	Seconds       float64  `json:"measurement_seconds"`
	SuccessfulRPS *float64 `json:"successful_requests_per_second"`
}
type estateComparisonMetric struct {
	Category         string               `json:"category"`
	Baseline         estateComparisonCost `json:"baseline"`
	Candidate        estateComparisonCost `json:"candidate"`
	RateDifference   *float64             `json:"descriptive_rate_difference"`
	PercentChange    *float64             `json:"descriptive_percent_change"`
	PercentageStatus string               `json:"percentage_status"`
	Eligible         bool                 `json:"optimization_criteria_eligible"`
}
type estateComparisonCost struct {
	FirstAt  time.Time           `json:"first_sample_at"`
	LastAt   time.Time           `json:"last_sample_at"`
	Seconds  *float64            `json:"sampled_seconds"`
	Delta    *float64            `json:"delta"`
	RPS      *float64            `json:"requests_per_second"`
	Outcomes map[string]*float64 `json:"outcome_deltas"`
	State    string              `json:"coverage_state"`
}

func estateComparisonHash(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) }
func comparisonNumber(value float64) *float64 { return &value }
func compareEstateReports(a, b estateReport, ah, bh string, provenance estateComparisonProvenance) estateComparison {
	out := estateComparison{Schema: "astronomer-estate-comparison-v1", Evidence: "offline_report_consistency_only", BaselineSHA256: ah, CandidateSHA256: bh, BaselineCommit: a.Environment.Commit, CandidateCommit: b.Environment.Commit, BaselineDriver: a.DriverRevision, CandidateDriver: b.DriverRevision, Provenance: provenance, Blockers: []string{}, Rows: []estateComparisonRow{}}
	if comparableEstateReports(a, b) != nil {
		out.Blockers = append(out.Blockers, "strict_report_comparability_failed")
	}
	if a.DriverModified != "false" || b.DriverModified != "false" || a.DriverRevision == "unavailable" || a.DriverRevision != b.DriverRevision {
		out.Blockers = append(out.Blockers, "driver_provenance_unmatched")
	}
	if provenance.ImageEvidence != "hash_matched_user_reviewed_changes" {
		out.Blockers = append(out.Blockers, "image_provenance_unverified")
	}
	if !estateVerificationComplete(a) || !estateVerificationComplete(b) {
		out.Blockers = append(out.Blockers, "member_verification_incomplete")
	}
	if len(a.Problems) > 0 || len(b.Problems) > 0 {
		out.Blockers = append(out.Blockers, "reported_collection_failures")
	}
	phases := map[string]estateMeasuredPhase{}
	for _, p := range b.Phases {
		phases[p.Spec.Name] = p
	}
	names := estateComparisonMembers(a, b)
	if len(names) != a.Members || len(names) != b.Members {
		out.Blockers = append(out.Blockers, "member_identity_set_incomplete")
	}
	for i, spec := range a.Definitions {
		var left estateMeasuredPhase
		for _, p := range a.Phases {
			if p.Spec.Name == spec.Name {
				left = p
				break
			}
		}
		right := phases[spec.Name]
		for memberIndex, name := range names {
			row := estateComparisonRow{PhaseOrdinal: i + 1, PhaseSHA256: estateComparisonHash(spec.Name), MemberOrdinal: memberIndex + 1, MemberSHA256: estateComparisonHash(name), Blockers: append([]string(nil), out.Blockers...), Metrics: []estateComparisonMetric{}}
			row.BaselineWork = estateWork(left)
			row.CandidateWork = estateWork(right)
			row.BaselineMemberWork = estateMemberWork(left, name)
			row.CandidateMemberWork = estateMemberWork(right, name)
			if left.Spec != spec || right.Spec != spec {
				row.Blockers = append(row.Blockers, "phase_missing_or_mismatched")
			}
			if !estateWorkMatched(left, right, a.Members, a.Tier) || !estateCatalogComplete(a, left) || !estateCatalogComplete(b, right) {
				row.Blockers = append(row.Blockers, "workload_incomplete_or_unmatched")
			}
			lm, rm := estateMemberMetric(left, name), estateMemberMetric(right, name)
			if lm == nil || rm == nil {
				row.Blockers = append(row.Blockers, "member_metrics_missing")
			} else {
				if lm.TargetSHA256 != rm.TargetSHA256 {
					row.Blockers = append(row.Blockers, "metric_target_provenance_changed")
				}
				if !estateComparisonFreshness(*lm) || !estateComparisonFreshness(*rm) {
					row.Blockers = append(row.Blockers, "freshness_or_metric_coverage_incomplete")
				}
			}
			var lt, rt *estateTransportReport
			if lm != nil {
				lt, _ = recomputeEstateTransport(lm.Transport, left.Measurement)
			}
			if rm != nil {
				rt, _ = recomputeEstateTransport(rm.Transport, right.Measurement)
			}
			if !estateCompleteTransportInterval(lt, left.Measurement) || !estateCompleteTransportInterval(rt, right.Measurement) {
				row.Blockers = append(row.Blockers, "transport_window_incomplete")
			}
			if estateTransportHasFailures(lt) || estateTransportHasFailures(rt) {
				row.Blockers = append(row.Blockers, "transport_failure_outcomes")
			}
			row.Eligible = len(row.Blockers) == 0 && provenance.Attribution == "declared_matching"
			if provenance.Attribution != "declared_matching" {
				row.Blockers = append(row.Blockers, "consumer_attribution_unmatched")
			}
			for _, category := range estateCostCategories() {
				metric := estateComparisonMetric{Category: category, Baseline: estateTransportCost(lt, category), Candidate: estateTransportCost(rt, category), Eligible: row.Eligible, PercentageStatus: "unavailable_transport"}
				if metric.Baseline.RPS != nil && metric.Candidate.RPS != nil {
					metric.RateDifference = comparisonNumber(*metric.Candidate.RPS - *metric.Baseline.RPS)
					switch {
					case category != "total_family" && provenance.Attribution != "declared_matching":
						metric.PercentageStatus = "consumer_attribution_unmatched"
					case *metric.Baseline.RPS == 0:
						metric.PercentageStatus = "undefined_zero_baseline"
					default:
						metric.PercentChange = comparisonNumber(100 * (*metric.Candidate.RPS - *metric.Baseline.RPS) / *metric.Baseline.RPS)
						metric.PercentageStatus = "descriptive_only"
					}
				}
				row.Metrics = append(row.Metrics, metric)
			}
			out.Rows = append(out.Rows, row)
		}
	}
	out.OptimizationEligible = len(out.Blockers) == 0 && len(out.Rows) == len(a.Definitions)*a.Members
	for _, row := range out.Rows {
		out.OptimizationEligible = out.OptimizationEligible && row.Eligible
	}
	return out
}
func estateCostCategories() []string {
	out := []string{}
	for _, consumer := range estateConsumers {
		for _, operation := range estateOperations {
			out = append(out, consumer+"/"+operation)
		}
	}
	return append(out, "delivery_lists", "delivery_observation_total", "total_family")
}
func estateCategoryIncludes(category, consumer, operation string) bool {
	switch category {
	case "total_family":
		return true
	case "delivery_observation_total":
		return consumer == "shared_observation" || consumer == "delivery_inventory" || consumer == "delivery_assignment_observation"
	case "delivery_lists":
		return operation == "list" && (consumer == "shared_observation" || consumer == "delivery_inventory" || consumer == "delivery_assignment_observation")
	}
	return category == consumer+"/"+operation
}
func estateTransportCost(r *estateTransportReport, category string) estateComparisonCost {
	cost := estateComparisonCost{State: "unavailable_transport", Outcomes: map[string]*float64{}}
	for _, outcome := range estateOutcomes {
		cost.Outcomes[outcome] = nil
	}
	if r == nil {
		return cost
	}
	cost.FirstAt = r.FirstAt
	cost.LastAt = r.LastAt
	seconds := r.LastAt.Sub(r.FirstAt).Seconds()
	if seconds <= 0 {
		return cost
	}
	cost.Seconds = &seconds
	total := 0.
	cost.State = "complete_sampled_window"
	for _, group := range r.ByConsumerOperation {
		if estateCategoryIncludes(category, group.Consumer, group.Operation) {
			if group.WindowDelta == nil {
				cost.State = group.State
				return cost
			}
			total += *group.WindowDelta
		}
	}
	cost.Delta = &total
	cost.RPS = comparisonNumber(total / seconds)
	for _, outcome := range estateOutcomes {
		cost.Outcomes[outcome] = comparisonNumber(0)
	}
	for _, series := range r.Series {
		if estateCategoryIncludes(category, series.Consumer, series.Operation) {
			*cost.Outcomes[series.Outcome] += *series.WindowDelta
		}
	}
	return cost
}
func estateWork(p estateMeasuredPhase) estateComparisonWork {
	totals := estateTotals(p.Spec, p.HTTP)
	w := estateComparisonWork{Requested: totals.Requested, Completed: totals.Completed, Success: totals.Success, Failed: totals.Failed, Seconds: p.Measurement.End.Sub(p.Measurement.Start).Seconds()}
	if w.Seconds > 0 {
		w.SuccessfulRPS = comparisonNumber(float64(w.Success) / w.Seconds)
	}
	return w
}
func estateMemberMetric(p estateMeasuredPhase, name string) *estateMetricReport {
	for i := range p.Metrics {
		if p.Metrics[i].Member == name {
			return &p.Metrics[i]
		}
	}
	return nil
}
func estateComparisonMembers(a, b estateReport) []string {
	set := map[string]bool{}
	for _, r := range []estateReport{a, b} {
		for _, v := range r.StartVerification {
			set[v.Member] = true
		}
		for _, p := range r.Phases {
			for _, m := range p.Metrics {
				set[m.Member] = true
			}
		}
	}
	names := []string{}
	for name := range set {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
func estateVerificationComplete(r estateReport) bool {
	names := map[string]bool{}
	for _, v := range r.StartVerification {
		if !v.IdentityVerified || v.RenderedNamespace != "VERIFIED" || names[v.Member] {
			return false
		}
		names[v.Member] = true
	}
	if len(names) != r.Members || len(r.EndVerification) != r.Members {
		return false
	}
	for _, v := range r.EndVerification {
		if !v.IdentityVerified || v.RenderedNamespace != "VERIFIED" || !names[v.Member] {
			return false
		}
		delete(names, v.Member)
	}
	return len(names) == 0
}
func estateWorkMatched(a, b estateMeasuredPhase, members, tier int) bool {
	if len(evaluateEstateMeasuredPhase(a, members, tier)) > 0 || len(evaluateEstateMeasuredPhase(b, members, tier)) > 0 {
		return false
	}
	wa, wb := estateWork(a), estateWork(b)
	if wa.SuccessfulRPS == nil || wb.SuccessfulRPS == nil || !estateRatesMatch(*wa.SuccessfulRPS, *wb.SuccessfulRPS) {
		return false
	}
	rows := map[string]estateRequestResult{}
	for _, r := range b.HTTP {
		rows[r.Member+"/"+r.Scenario+"/"+r.Assignment] = r
	}
	if len(rows) != len(a.HTTP) {
		return false
	}
	for _, r := range a.HTTP {
		other, ok := rows[r.Member+"/"+r.Scenario+"/"+r.Assignment]
		if !ok || !estateRatesMatch(float64(r.Success)/wa.Seconds, float64(other.Success)/wb.Seconds) {
			return false
		}
	}
	return true
}
func estateRatesMatch(a, b float64) bool {
	if a == 0 || b == 0 {
		return a == b
	}
	return math.Abs(a-b)/math.Max(a, b) <= .01
}
func estateCompleteTransportInterval(r *estateTransportReport, w estateWindow) bool {
	return r != nil && r.State == "complete_sampled_window" && !w.Start.IsZero() && !r.FirstAt.Before(w.Start) && !r.LastAt.After(w.End) && r.FirstAt.Sub(w.Start) <= 30*time.Second && w.End.Sub(r.LastAt) <= 30*time.Second && r.LastAt.Sub(r.FirstAt).Seconds() >= .98*w.End.Sub(w.Start).Seconds()
}
func estateTransportHasFailures(r *estateTransportReport) bool {
	if r == nil {
		return false
	}
	for _, s := range r.Series {
		if s.Outcome != "2xx" && s.WindowDelta != nil && *s.WindowDelta > 0 {
			return true
		}
	}
	return false
}
func estateComparisonFreshness(m estateMetricReport) bool {
	if len(m.problems()) > 0 || m.Samples < minimumLeakSamples || m.MaximumSourceAge > 240 {
		return false
	}
	for _, name := range []string{"process_cpu_seconds_total", "process_resident_memory_bytes", "process_open_fds", "go_memstats_heap_alloc_bytes", "go_goroutines"} {
		s := m.Series[name]
		if s == nil || s.Samples != m.Samples || s.Missing != 0 || s.Resets != 0 {
			return false
		}
	}
	for _, kind := range estateKinds {
		found := false
		for _, s := range m.Series {
			if s.Name == "astronomer_agent_observation_requests_total" && s.Labels["kind"] == kind && s.Labels["verb"] == "list" && s.Labels["outcome"] == "success" && s.Samples == m.Samples && s.Missing == 0 && s.Resets == 0 {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	for _, source := range estateSources {
		found := map[string]*estateSeries{}
		for _, s := range m.Series {
			if s.Labels["source"] == source {
				found[s.Name] = s
			}
		}
		for _, suffix := range []string{"source_available", "sampled_at_timestamp_seconds", "observed_at_timestamp_seconds"} {
			s := found[estateObservationPrefix+suffix]
			if s == nil || s.Samples != m.Samples || s.Missing != 0 || s.Resets != 0 {
				return false
			}
			if suffix == "source_available" {
				if s.Minimum != 1 || s.Maximum != 1 {
					return false
				}
			} else {
				bound := 240.
				if suffix == "sampled_at_timestamp_seconds" {
					bound = 60
				}
				for _, edge := range []struct {
					value float64
					at    time.Time
				}{{s.First, s.FirstAt}, {s.Last, s.LastAt}} {
					age := float64(edge.at.UnixNano())/1e9 - edge.value
					if edge.value <= 0 || age < 0 || age > bound {
						return false
					}
				}
			}
		}
	}
	return true
}

// Member requested is observed scheduled work, not an invented share of phase RPS.
func estateMemberWork(p estateMeasuredPhase, name string) estateComparisonWork {
	w := estateComparisonWork{Seconds: p.Measurement.End.Sub(p.Measurement.Start).Seconds()}
	for _, r := range p.HTTP {
		if r.Member == name {
			w.Requested += r.Scheduled
			w.Completed += r.Completed
			w.Success += r.Success
			w.Failed += r.Failed
		}
	}
	if w.Seconds > 0 {
		w.SuccessfulRPS = comparisonNumber(float64(w.Success) / w.Seconds)
	}
	return w
}
