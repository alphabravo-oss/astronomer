package main

import (
	"fmt"
	"math"
	"time"
)

// evaluateWorkloadConservation applies the numerical workload obligations
// shared by engineering and signed certification runs. The caller holds rec.mu.
func (r *report) evaluateWorkloadConservation(totalRequests int) bool {
	observed := time.Duration(0)
	if !r.rec.startedAt.IsZero() && !r.rec.endedAt.IsZero() {
		observed = r.rec.endedAt.Sub(r.rec.startedAt)
	}
	if r.cfg.rps > 0 {
		if r.cfg.duration > 0 && float64(observed)/float64(r.cfg.duration) < r.thresh.durationRatioMin {
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("observed duration %s was below %.2f of declared %s", observed.Round(time.Second), r.thresh.durationRatioMin, r.cfg.duration))
		}
		expectedRequests := float64(r.cfg.rps) * minDuration(observed, r.cfg.duration).Seconds() * r.thresh.achievedRPSMin
		if float64(totalRequests) < expectedRequests {
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("recorded requests %d were below %.0f required for %.2f of target RPS", totalRequests, expectedRequests, r.thresh.achievedRPSMin))
		}
	}
	if !r.cfg.skipAgents && len(r.cfg.fixtureClusterIDs) > 0 {
		for kind, declared := range map[string]int{
			"PodList":        r.cfg.resources.PodsPerCluster,
			"DeploymentList": r.cfg.resources.DeploymentsPerCluster,
			"ServiceList":    r.cfg.resources.ServicesPerCluster,
		} {
			if declared > 0 && r.rec.resourceCardinality[kind] != declared {
				r.Reasons = append(r.Reasons,
					fmt.Sprintf("%s cardinality %d did not match declared %d", kind, r.rec.resourceCardinality[kind], declared))
			}
		}
	}
	if r.cfg.resources.EventsPerSecond > 0 {
		expectedEvents := float64(r.cfg.resources.EventsPerSecond) * minDuration(observed, r.cfg.duration).Seconds() * r.thresh.eventRateRatioMin
		if float64(r.rec.stateEventsEmitted) < expectedEvents {
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("state events emitted %d were below %.0f required for %.2f of declared rate", r.rec.stateEventsEmitted, expectedEvents, r.thresh.eventRateRatioMin))
		}
	}

	auditConfigured := r.cfg.mandatoryAudit.RatePerSecond > 0 && r.cfg.mandatoryAudit.MaxOperations > 0
	if !auditConfigured {
		return false
	}
	conservation := r.rec.auditConservation
	auditWindow := conservation.WindowEndedAt.Sub(conservation.WindowStartedAt)
	if auditWindow < 0 {
		auditWindow = 0
	}
	requiredWindow := minDuration(observed, r.cfg.duration) * time.Duration(r.thresh.durationRatioMin*1000) / 1000
	expectedAttempts := int(math.Ceil(
		float64(r.cfg.mandatoryAudit.RatePerSecond) * minDuration(observed, r.cfg.duration).Seconds() * r.thresh.durationRatioMin,
	))
	if conservation.Attempted < expectedAttempts {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("mandatory-audit attempts %d were below %d required to sustain %d/s for the measured window",
				conservation.Attempted, expectedAttempts, r.cfg.mandatoryAudit.RatePerSecond))
	}
	interval := time.Second / time.Duration(r.cfg.mandatoryAudit.RatePerSecond)
	coverage := conservation.LastAcceptedAt.Sub(conservation.FirstAcceptedAt) + 2*interval
	if coverage > auditWindow {
		coverage = auditWindow
	}
	if auditWindow < requiredWindow || conservation.FirstAcceptedAt.IsZero() || conservation.LastAcceptedAt.IsZero() || coverage < requiredWindow {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("mandatory-audit activity covered %s of a %s window; need at least %s",
				coverage.Round(time.Second), auditWindow.Round(time.Second), requiredWindow.Round(time.Second)))
	}
	if !conservation.Reconciled || conservation.Accepted != conservation.IntentsObserved ||
		conservation.Accepted != conservation.CanonicalRows || conservation.Rejected != 0 ||
		conservation.Duplicates != 0 || conservation.Lost != 0 {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("mandatory-audit conservation failed: accepted=%d intents=%d canonical=%d rejected=%d duplicates=%d lost=%d",
				conservation.Accepted, conservation.IntentsObserved, conservation.CanonicalRows,
				conservation.Rejected, conservation.Duplicates, conservation.Lost))
	}
	if len(r.rec.scrapeSeries["audit_dropped_total"]) > 0 && counterDelta(r.rec.scrapeSeries["audit_dropped_total"]) != 0 {
		r.Reasons = append(r.Reasons, fmt.Sprintf("audit dropped counter grew by %.0f", counterDelta(r.rec.scrapeSeries["audit_dropped_total"])))
	}
	if len(r.rec.scrapeSeries["audit_write_failures_total"]) > 0 && counterDelta(r.rec.scrapeSeries["audit_write_failures_total"]) != 0 {
		r.Reasons = append(r.Reasons, fmt.Sprintf("audit write-failure counter grew by %.0f", counterDelta(r.rec.scrapeSeries["audit_write_failures_total"])))
	}
	if dead := lastValue(r.rec.scrapeSeries["audit_outbox_dead_rows"]); dead != 0 {
		r.Reasons = append(r.Reasons, fmt.Sprintf("audit outbox has %.0f dead rows", dead))
	}
	if active := lastValue(r.rec.scrapeSeries["audit_outbox_active_rows"]); active != 0 {
		r.Reasons = append(r.Reasons, fmt.Sprintf("audit outbox did not drain: %.0f active rows", active))
	}
	return true
}
