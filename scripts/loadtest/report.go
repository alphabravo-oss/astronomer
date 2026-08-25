package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Thresholds — all operator-tunable via env vars. Defaults chosen from the
// design doc; rationale is in scripts/loadtest/README.md.
type thresholds struct {
	clusterListP99Ms   int
	resourcesP99Ms     int
	connectedAgentsMin float64 // fraction of N — e.g. 1.0 == all agents
	dlqDepthMax        float64
	emptyAcquireMaxQPS float64
	goroutineLeakRatio float64 // 1.5 == 50% steady-state growth tolerated
	heapLeakRatio      float64
	openFDLeakRatio    float64
	openFDGrowthMax    float64
	queueAgeMaxSeconds float64
	eventLagMaxSeconds float64
	httpErrorRatioMax  float64
	achievedRPSMin     float64
	durationRatioMin   float64
	eventRateRatioMin  float64
}

func defaultThresholds() thresholds {
	return thresholds{
		clusterListP99Ms:   envOrInt("LOADTEST_THRESH_CLUSTER_P99_MS", 500),
		resourcesP99Ms:     envOrInt("LOADTEST_THRESH_RESOURCES_P99_MS", 2000),
		connectedAgentsMin: envOrFloat("LOADTEST_THRESH_CONNECTED_MIN", 1.0),
		dlqDepthMax:        envOrFloat("LOADTEST_THRESH_DLQ_MAX", 10),
		emptyAcquireMaxQPS: envOrFloat("LOADTEST_THRESH_EMPTY_ACQUIRE_QPS", 0.1),
		goroutineLeakRatio: envOrFloat("LOADTEST_THRESH_GOROUTINE_RATIO", 1.5),
		heapLeakRatio:      envOrFloat("LOADTEST_THRESH_HEAP_RATIO", 1.5),
		openFDLeakRatio:    envOrFloat("LOADTEST_THRESH_OPEN_FD_RATIO", 1.25),
		openFDGrowthMax:    envOrFloat("LOADTEST_THRESH_OPEN_FD_GROWTH", 64),
		queueAgeMaxSeconds: envOrFloat("LOADTEST_THRESH_QUEUE_AGE_SECONDS", 60),
		eventLagMaxSeconds: envOrFloat("LOADTEST_THRESH_EVENT_LAG_SECONDS", 30),
		httpErrorRatioMax:  envOrFloat("LOADTEST_THRESH_HTTP_ERROR_RATIO", 0),
		achievedRPSMin:     envOrFloat("LOADTEST_THRESH_ACHIEVED_RPS_RATIO", 0.95),
		durationRatioMin:   envOrFloat("LOADTEST_THRESH_DURATION_RATIO", 0.98),
		eventRateRatioMin:  envOrFloat("LOADTEST_THRESH_EVENT_RATE_RATIO", 0.95),
	}
}

const minimumLeakSamples = 8

const (
	drillEvidenceSchemaVersion   = "astronomer-day2-drill-evidence-v2"
	drillManifestSchemaVersion   = "astronomer-day2-drill-manifest-v1"
	trustedDrillEvidenceWorkflow = ".github/workflows/day2-drill-qualification.yaml"
	scaleReportSchemaVersion     = "astronomer-scale-report-v2"
	maximumDrillEvidenceAge      = 30 * 24 * time.Hour
	maximumClockSkew             = 5 * time.Minute
)

func envOrFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var out float64
	if _, err := fmt.Sscanf(v, "%f", &out); err != nil {
		return def
	}
	return out
}

type report struct {
	cfg     *config
	rec     *recorder
	thresh  thresholds
	Verdict string
	Reasons []string
}

func newReport(cfg *config, rec *recorder) *report {
	r := &report{
		cfg:    cfg,
		rec:    rec,
		thresh: defaultThresholds(),
	}
	r.evaluate()
	return r
}

// evaluate fills Verdict + Reasons from the recorded data.
func (r *report) evaluate() {
	r.rec.mu.Lock()
	defer r.rec.mu.Unlock()

	// 1. p99 cluster_list latency
	if samples, ok := r.rec.httpSamples["cluster_list"]; ok && len(samples) > 0 {
		p99 := percentile(samples, 0.99)
		if p99 > time.Duration(r.thresh.clusterListP99Ms)*time.Millisecond {
			r.Reasons = append(r.Reasons, fmt.Sprintf("cluster_list p99 %v exceeded %dms", p99, r.thresh.clusterListP99Ms))
		}
	}

	// 2. p99 resources latency (cluster_pods scenario)
	if samples, ok := r.rec.httpSamples["cluster_pods"]; ok && len(samples) > 0 {
		p99 := percentile(samples, 0.99)
		if p99 > time.Duration(r.thresh.resourcesP99Ms)*time.Millisecond {
			r.Reasons = append(r.Reasons, fmt.Sprintf("cluster_pods p99 %v exceeded %dms", p99, r.thresh.resourcesP99Ms))
		}
	}

	// 3. Connected agents at end. Tolerate brief flaps during the run — what
	//    matters is the steady-state count when the workload finishes.
	if !r.cfg.skipAgents && r.cfg.clusters > 0 {
		expected := float64(r.cfg.clusters) * r.thresh.connectedAgentsMin
		connected := lastValue(r.rec.scrapeSeries["agent_connections"])
		if connected < expected {
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("only %.0f/%d agents connected at end (threshold %.0f)",
					connected, r.cfg.clusters, expected))
		}
	}

	// 4. DLQ growth — measured as worker_queue_pending at end. Asynq's
	//    "pending" is the queued-but-not-running count, which is what we
	//    want for "is the worker keeping up?".
	dlqEnd := lastValue(r.rec.scrapeSeries["worker_queue_pending"])
	if dlqEnd > r.thresh.dlqDepthMax {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("worker queue pending depth %.0f exceeded %.0f", dlqEnd, r.thresh.dlqDepthMax))
	}

	// 5. DB pool acquires-blocking rate. Calculated as the delta on the
	//    empty_acquire counter over the run duration.
	emptyRate := deltaPerSecond(r.rec.scrapeSeries["db_pool_empty_acquire"])
	if emptyRate > r.thresh.emptyAcquireMaxQPS {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("db pool empty-acquire rate %.3f/s exceeded %.3f/s", emptyRate, r.thresh.emptyAcquireMaxQPS))
	}

	// 6. Process leak budgets. Compare a post-warm-up window with the terminal
	//    window instead of the first and last individual samples. The first
	//    quarter is intentionally excluded because agent registration creates
	//    the expected steady-state goroutines and file descriptors.
	startG, endG, goroutineEvidence := leakWindowAverages(r.rec.scrapeSeries["server_goroutines"])
	if goroutineEvidence && startG > 0 && endG/startG > r.thresh.goroutineLeakRatio {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("server goroutine steady-state average grew %.0f->%.0f (>%.2fx threshold)",
				startG, endG, r.thresh.goroutineLeakRatio))
	}
	startHeap, endHeap, heapEvidence := leakWindowAverages(r.rec.scrapeSeries["server_heap_bytes"])
	if heapEvidence && startHeap > 0 && endHeap/startHeap > r.thresh.heapLeakRatio {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("server heap steady-state average grew %.0f->%.0f bytes (>%.2fx threshold)",
				startHeap, endHeap, r.thresh.heapLeakRatio))
	}
	startFD, endFD, fdEvidence := leakWindowAverages(r.rec.scrapeSeries["server_open_fds"])
	fdGrowth := endFD - startFD
	if fdEvidence && startFD > 0 && (fdGrowth > r.thresh.openFDGrowthMax || (fdGrowth > 8 && endFD/startFD > r.thresh.openFDLeakRatio)) {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("server open-FD steady-state average grew %.0f->%.0f (growth %.0f; limits %.2fx or +%.0f)",
				startFD, endFD, fdGrowth, r.thresh.openFDLeakRatio, r.thresh.openFDGrowthMax))
	}

	// 7. Tunnel dropped events — any growth is suspicious for a load test.
	//    We don't fail-fast (drops can happen under legitimate congestion)
	//    but call it out in the report.
	dropped := lastValue(r.rec.scrapeSeries["dropped_events_total"])
	startDrop := firstValue(r.rec.scrapeSeries["dropped_events_total"])
	if dropped-startDrop > 0 {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("INFO: dropped_events_total grew by %.0f during the run", dropped-startDrop))
	}
	if age := peakValue(r.rec.scrapeSeries["worker_queue_age_seconds"]); age > r.thresh.queueAgeMaxSeconds {
		r.Reasons = append(r.Reasons, fmt.Sprintf("worker queue age %.1fs exceeded %.1fs", age, r.thresh.queueAgeMaxSeconds))
	}
	if lag := peakValue(r.rec.scrapeSeries["event_relay_lag_seconds"]); lag > r.thresh.eventLagMaxSeconds {
		r.Reasons = append(r.Reasons, fmt.Sprintf("event relay lag %.1fs exceeded %.1fs", lag, r.thresh.eventLagMaxSeconds))
	}
	if r.cfg.certification {
		if r.cfg.skipAgents || len(r.cfg.fixtureClusterIDs) == 0 {
			r.Reasons = append(r.Reasons, "certification requires provisioned connected fixture clusters")
		}
		for _, scenario := range defaultScenarios() {
			if scenario.weight > 0 && r.rec.httpCount[scenario.name] == 0 {
				r.Reasons = append(r.Reasons, "required scenario has no samples: "+scenario.name)
			}
		}
		totalRequests, failedRequests := 0, 0
		for name, count := range r.rec.httpCount {
			totalRequests += count
			failedRequests += r.rec.httpErrors[name]
			for status, statusCount := range r.rec.httpStatus[name] {
				if status < 200 || status >= 300 {
					failedRequests += statusCount
				}
			}
		}
		if totalRequests == 0 {
			r.Reasons = append(r.Reasons, "certification produced zero HTTP requests")
		} else if ratio := float64(failedRequests) / float64(totalRequests); ratio > r.thresh.httpErrorRatioMax {
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("HTTP failure ratio %.6f exceeded %.6f (%d/%d)", ratio, r.thresh.httpErrorRatioMax, failedRequests, totalRequests))
		}
		observed := time.Duration(0)
		if !r.rec.startedAt.IsZero() && !r.rec.endedAt.IsZero() {
			observed = r.rec.endedAt.Sub(r.rec.startedAt)
		}
		if r.cfg.duration > 0 && float64(observed)/float64(r.cfg.duration) < r.thresh.durationRatioMin {
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("observed duration %s was below %.2f of declared %s", observed.Round(time.Second), r.thresh.durationRatioMin, r.cfg.duration))
		}
		expectedRequests := float64(r.cfg.rps) * minDuration(observed, r.cfg.duration).Seconds() * r.thresh.achievedRPSMin
		if float64(totalRequests) < expectedRequests {
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("recorded requests %d were below %.0f required for %.2f of target RPS", totalRequests, expectedRequests, r.thresh.achievedRPSMin))
		}
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
		if r.cfg.resources.EventsPerSecond > 0 {
			expectedEvents := float64(r.cfg.resources.EventsPerSecond) * minDuration(observed, r.cfg.duration).Seconds() * r.thresh.eventRateRatioMin
			if float64(r.rec.stateEventsEmitted) < expectedEvents {
				r.Reasons = append(r.Reasons,
					fmt.Sprintf("state events emitted %d were below %.0f required for %.2f of declared rate", r.rec.stateEventsEmitted, expectedEvents, r.thresh.eventRateRatioMin))
			}
		}
		if r.cfg.mandatoryAudit.RatePerSecond <= 0 || r.cfg.mandatoryAudit.MaxOperations <= 0 {
			r.Reasons = append(r.Reasons, "certification profile does not declare a mandatory-audit workload")
		} else {
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
					fmt.Sprintf("mandatory-audit attempts %d were below %d required to sustain %d/s for the certified window",
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
			for _, evidence := range []struct {
				name string
				got  int
			}{
				{name: "audit_dropped_total", got: len(r.rec.scrapeSeries["audit_dropped_total"])},
				{name: "audit_write_failures_total", got: len(r.rec.scrapeSeries["audit_write_failures_total"])},
				{name: "audit_outbox_active_rows", got: len(r.rec.scrapeSeries["audit_outbox_active_rows"])},
				{name: "audit_outbox_dead_rows", got: len(r.rec.scrapeSeries["audit_outbox_dead_rows"])},
			} {
				if evidence.got < 2 {
					r.Reasons = append(r.Reasons,
						fmt.Sprintf("certification audit evidence %s has %d samples; need at least 2", evidence.name, evidence.got))
				}
			}
			if delta := counterDelta(r.rec.scrapeSeries["audit_dropped_total"]); delta != 0 {
				r.Reasons = append(r.Reasons, fmt.Sprintf("audit dropped counter grew by %.0f", delta))
			}
			if delta := counterDelta(r.rec.scrapeSeries["audit_write_failures_total"]); delta != 0 {
				r.Reasons = append(r.Reasons, fmt.Sprintf("audit write-failure counter grew by %.0f", delta))
			}
			if dead := lastValue(r.rec.scrapeSeries["audit_outbox_dead_rows"]); dead != 0 {
				r.Reasons = append(r.Reasons, fmt.Sprintf("audit outbox has %.0f dead rows", dead))
			}
			if active := lastValue(r.rec.scrapeSeries["audit_outbox_active_rows"]); active != 0 {
				r.Reasons = append(r.Reasons, fmt.Sprintf("audit outbox did not drain: %.0f active rows", active))
			}
		}
		if missing := loadCertificationMetadata().missing(); len(missing) > 0 {
			r.Reasons = append(r.Reasons, "certification metadata missing: "+strings.Join(missing, ", "))
		}
		metadata := loadCertificationMetadata()
		if _, err := metadata.componentReplicaCounts(); err != nil {
			r.Reasons = append(r.Reasons, "certification component replica metadata invalid: "+err.Error())
		}
		for _, drill := range r.cfg.day2FailureDrill {
			if result := loadDrillEvidence(drill, r.cfg.profileName, metadata, time.Now().UTC()); result.Status != "pass" {
				r.Reasons = append(r.Reasons, fmt.Sprintf("day-2 drill %s evidence rejected: %s", drill, result.Detail))
			}
		}
		for _, evidence := range []struct {
			name string
			ok   bool
			got  int
		}{
			{name: "server_goroutines", ok: goroutineEvidence, got: len(r.rec.scrapeSeries["server_goroutines"])},
			{name: "server_heap_bytes", ok: heapEvidence, got: len(r.rec.scrapeSeries["server_heap_bytes"])},
			{name: "server_open_fds", ok: fdEvidence, got: len(r.rec.scrapeSeries["server_open_fds"])},
		} {
			if !evidence.ok {
				r.Reasons = append(r.Reasons,
					fmt.Sprintf("certification leak evidence %s has %d samples; need at least %d", evidence.name, evidence.got, minimumLeakSamples))
			}
		}
	}

	// Only the "INFO:" reasons leave Verdict==pass; any other entry flips it.
	failure := false
	for _, reason := range r.Reasons {
		if !strings.HasPrefix(reason, "INFO:") {
			failure = true
			break
		}
	}
	if failure {
		r.Verdict = "fail"
	} else {
		r.Verdict = "pass"
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func counterDelta(series []scrapePoint) float64 {
	if len(series) < 2 {
		return 0
	}
	delta := lastValue(series) - firstValue(series)
	if delta < 0 {
		return 0
	}
	return delta
}

// WriteFile renders the markdown report to path.
func (r *report) WriteFile(path string) error {
	var sb strings.Builder

	sb.WriteString("# Astronomer Go — load-test report\n\n")
	fmt.Fprintf(&sb, "- Generated: `%s`\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&sb, "- Server: `%s`\n", r.cfg.server)
	if r.cfg.profileName != "" {
		fmt.Fprintf(&sb, "- Profile: `%s`\n", r.cfg.profileName)
	}
	fmt.Fprintf(&sb, "- Clusters: `%d`\n", r.cfg.clusters)
	fmt.Fprintf(&sb, "- Target RPS: `%d`\n", r.cfg.rps)
	fmt.Fprintf(&sb, "- Configured duration: `%s`\n", r.cfg.duration)
	fmt.Fprintf(&sb, "- Certification mode: `%t`\n", r.cfg.certification)
	fmt.Fprintf(&sb, "- Resources per cluster: pods `%d`, deployments `%d`, services `%d`\n",
		r.cfg.resources.PodsPerCluster,
		r.cfg.resources.DeploymentsPerCluster,
		r.cfg.resources.ServicesPerCluster,
	)
	if r.cfg.reconnectStorm.Enabled {
		fmt.Fprintf(&sb, "- Reconnect storm: `%d%%` of agents at `%s` with `%s` jitter\n",
			r.cfg.reconnectStorm.BatchPercent,
			r.cfg.reconnectStorm.AtDuration,
			r.cfg.reconnectStorm.JitterDuration,
		)
	}
	r.rec.mu.Lock()
	if !r.rec.startedAt.IsZero() && !r.rec.endedAt.IsZero() {
		fmt.Fprintf(&sb, "- Observed run window: `%s` (%s → %s)\n",
			r.rec.endedAt.Sub(r.rec.startedAt).Round(time.Second),
			r.rec.startedAt.UTC().Format(time.RFC3339),
			r.rec.endedAt.UTC().Format(time.RFC3339))
	}
	r.rec.mu.Unlock()
	metadata := loadCertificationMetadata()
	sb.WriteString("\n")

	// ── Verdict block — surfaced first so a grep on the file reads as a
	//    quick pass/fail without parsing the rest.
	fmt.Fprintf(&sb, "VERDICT: %s\n\n", r.Verdict)
	if len(r.Reasons) > 0 {
		sb.WriteString("## Notes\n\n")
		for _, reason := range r.Reasons {
			sb.WriteString("- ")
			sb.WriteString(reason)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Reproducibility metadata\n\n")
	sb.WriteString("| Field | Value |\n|---|---|\n")
	for _, row := range [][2]string{
		{"Commit", metadata.Commit}, {"Images", metadata.Images}, {"Chart values", metadata.ChartValues},
		{"Kubernetes", metadata.KubernetesVersion}, {"PostgreSQL", metadata.PostgresVersion},
		{"Redis", metadata.RedisVersion}, {"Hardware", metadata.Hardware}, {"Go", metadata.GoVersion},
	} {
		value := row[1]
		if value == "" {
			value = "not recorded"
		}
		fmt.Fprintf(&sb, "| %s | `%s` |\n", row[0], strings.ReplaceAll(value, "|", "\\|"))
	}
	sb.WriteString("\n")

	if len(r.cfg.day2FailureDrill) > 0 {
		sb.WriteString("## Day-2 failure drills in profile\n\n")
		sb.WriteString("| Drill | Status |\n|---|---|\n")
		for _, drill := range r.cfg.day2FailureDrill {
			result := loadDrillEvidence(drill, r.cfg.profileName, metadata, time.Now().UTC())
			fmt.Fprintf(&sb, "| %s | %s%s |\n", drill, result.Status, result.Suffix)
		}
		sb.WriteString("\n")
	}

	// ── Thresholds applied
	sb.WriteString("## Thresholds\n\n")
	sb.WriteString("| Threshold | Value |\n|---|---|\n")
	fmt.Fprintf(&sb, "| cluster_list p99 | <= %dms |\n", r.thresh.clusterListP99Ms)
	fmt.Fprintf(&sb, "| cluster_pods p99 | <= %dms |\n", r.thresh.resourcesP99Ms)
	fmt.Fprintf(&sb, "| connected agents fraction | >= %.2f |\n", r.thresh.connectedAgentsMin)
	fmt.Fprintf(&sb, "| worker pending depth | <= %.0f |\n", r.thresh.dlqDepthMax)
	fmt.Fprintf(&sb, "| db pool empty-acquire rate | <= %.3f /s |\n", r.thresh.emptyAcquireMaxQPS)
	fmt.Fprintf(&sb, "| goroutine terminal/baseline window ratio | <= %.2fx |\n", r.thresh.goroutineLeakRatio)
	fmt.Fprintf(&sb, "| heap terminal/baseline window ratio | <= %.2fx |\n", r.thresh.heapLeakRatio)
	fmt.Fprintf(&sb, "| open-FD terminal/baseline window ratio | <= %.2fx after +8 FDs |\n", r.thresh.openFDLeakRatio)
	fmt.Fprintf(&sb, "| open-FD terminal/baseline absolute growth | <= %.0f |\n", r.thresh.openFDGrowthMax)
	fmt.Fprintf(&sb, "| oldest worker queue item | <= %.0fs |\n", r.thresh.queueAgeMaxSeconds)
	fmt.Fprintf(&sb, "| event relay lag | <= %.0fs |\n", r.thresh.eventLagMaxSeconds)
	fmt.Fprintf(&sb, "| HTTP failure ratio | <= %.6f |\n", r.thresh.httpErrorRatioMax)
	fmt.Fprintf(&sb, "| achieved request rate | >= %.2f of target |\n", r.thresh.achievedRPSMin)
	fmt.Fprintf(&sb, "| observed duration | >= %.2f of configured |\n", r.thresh.durationRatioMin)
	fmt.Fprintf(&sb, "| emitted state-event rate | >= %.2f of declared |\n", r.thresh.eventRateRatioMin)
	sb.WriteString("\n")

	// ── Per-scenario latency table
	r.rec.mu.Lock()
	totalRequests := 0
	scenarioNames := make([]string, 0, len(r.rec.httpCount))
	for name, count := range r.rec.httpCount {
		scenarioNames = append(scenarioNames, name)
		totalRequests += count
	}
	sort.Strings(scenarioNames)

	sb.WriteString("## HTTP latency per scenario\n\n")
	sb.WriteString("| Scenario | Requests | Errors | p50 | p95 | p99 |\n|---|---:|---:|---:|---:|---:|\n")
	for _, name := range scenarioNames {
		samples := r.rec.httpSamples[name]
		fmt.Fprintf(&sb, "| %s | %d | %d | %v | %v | %v |\n",
			name,
			r.rec.httpCount[name],
			r.rec.httpErrors[name],
			percentile(samples, 0.50).Round(time.Millisecond),
			percentile(samples, 0.95).Round(time.Millisecond),
			percentile(samples, 0.99).Round(time.Millisecond),
		)
	}
	sb.WriteString("\n")

	observedDuration := r.rec.endedAt.Sub(r.rec.startedAt)
	observedRPS := 0.0
	if observedDuration > 0 {
		observedRPS = float64(totalRequests) / observedDuration.Seconds()
	}
	sb.WriteString("## Qualification conservation evidence\n\n")
	sb.WriteString("| Metric | Declared | Observed |\n|---|---:|---:|\n")
	fmt.Fprintf(&sb, "| HTTP requests / rate | %d RPS | %d / %.2f RPS |\n", r.cfg.rps, totalRequests, observedRPS)
	fmt.Fprintf(&sb, "| Duration | %s | %s |\n", r.cfg.duration, observedDuration.Round(time.Second))
	fmt.Fprintf(&sb, "| Pod cardinality | %d | %d |\n", r.cfg.resources.PodsPerCluster, r.rec.resourceCardinality["PodList"])
	fmt.Fprintf(&sb, "| Deployment cardinality | %d | %d |\n", r.cfg.resources.DeploymentsPerCluster, r.rec.resourceCardinality["DeploymentList"])
	fmt.Fprintf(&sb, "| Service cardinality | %d | %d |\n", r.cfg.resources.ServicesPerCluster, r.rec.resourceCardinality["ServiceList"])
	fmt.Fprintf(&sb, "| State events / second | %d | %d total |\n", r.cfg.resources.EventsPerSecond, r.rec.stateEventsEmitted)
	fmt.Fprintf(&sb, "| Mandatory audit attempted | %d | %d |\n", r.cfg.mandatoryAudit.MaxOperations, r.rec.auditConservation.Attempted)
	fmt.Fprintf(&sb, "| Mandatory audit accepted/intents/canonical | equal | %d/%d/%d |\n",
		r.rec.auditConservation.Accepted, r.rec.auditConservation.IntentsObserved, r.rec.auditConservation.CanonicalRows)
	fmt.Fprintf(&sb, "| Mandatory audit rejected/duplicate/lost | 0/0/0 | %d/%d/%d |\n",
		r.rec.auditConservation.Rejected, r.rec.auditConservation.Duplicates, r.rec.auditConservation.Lost)
	fmt.Fprintf(&sb, "| Mandatory audit max delivery lag | bounded by drain deadline | %s |\n", r.rec.auditConservation.MaxDeliveryLag.Round(time.Millisecond))
	fmt.Fprintf(&sb, "| Audit outbox active/dead at end | 0/0 | %.0f/%.0f |\n",
		lastValue(r.rec.scrapeSeries["audit_outbox_active_rows"]), lastValue(r.rec.scrapeSeries["audit_outbox_dead_rows"]))
	sb.WriteString("\n")

	// ── Status code breakdown
	sb.WriteString("## HTTP status codes\n\n")
	sb.WriteString("| Scenario | Codes |\n|---|---|\n")
	for _, name := range scenarioNames {
		codes := r.rec.httpStatus[name]
		if len(codes) == 0 {
			fmt.Fprintf(&sb, "| %s | (no responses) |\n", name)
			continue
		}
		codeList := make([]int, 0, len(codes))
		for code := range codes {
			codeList = append(codeList, code)
		}
		sort.Ints(codeList)
		parts := make([]string, 0, len(codeList))
		for _, code := range codeList {
			parts = append(parts, fmt.Sprintf("%d=%d", code, codes[code]))
		}
		fmt.Fprintf(&sb, "| %s | %s |\n", name, strings.Join(parts, " "))
	}
	sb.WriteString("\n")

	// ── Agent fleet
	sb.WriteString("## Agent fleet\n\n")
	sb.WriteString("| Metric | Value |\n|---|---:|\n")
	fmt.Fprintf(&sb, "| Synthetic agents launched | %d |\n", r.cfg.clusters)
	fmt.Fprintf(&sb, "| Successful CONNECT_ACKs | %d |\n", r.rec.connectCount)
	fmt.Fprintf(&sb, "| Disconnects (reconnect attempts) | %d |\n", r.rec.disconnectCount)
	fmt.Fprintf(&sb, "| Connected at end (gauge) | %.0f |\n", lastValue(r.rec.scrapeSeries["agent_connections"]))
	sb.WriteString("\n")

	// ── Server resource peaks
	sb.WriteString("## Server resource snapshot\n\n")
	sb.WriteString("| Metric | Peak | First | Last |\n|---|---:|---:|---:|\n")
	for _, m := range []string{
		"server_goroutines",
		"server_heap_bytes",
		"server_open_fds",
		"db_pool_acquired",
		"db_pool_max",
		"db_pool_empty_acquire",
		"worker_queue_pending",
		"worker_queue_age_seconds",
		"agent_connections",
		"agent_reconnects_total",
		"dropped_events_total",
		"event_relay_lag_seconds",
		"delivery_cohort_latency_avg_seconds",
	} {
		s := r.rec.scrapeSeries[m]
		fmt.Fprintf(&sb, "| %s | %.0f | %.0f | %.0f |\n", m, peakValue(s), firstValue(s), lastValue(s))
	}
	sb.WriteString("\n")

	// ── Driver resource peaks
	sb.WriteString("## Driver resource peaks\n\n")
	sb.WriteString("| Metric | Value |\n|---|---:|\n")
	fmt.Fprintf(&sb, "| Driver peak goroutines | %d |\n", r.rec.driverGoroutines)
	fmt.Fprintf(&sb, "| Driver peak heap bytes | %d |\n", r.rec.driverHeapBytes)
	sb.WriteString("\n")
	r.rec.mu.Unlock()

	sb.WriteString("---\n")
	sb.WriteString("_Generated by `scripts/loadtest`. See `docs/scale-baseline.md` for the cluster-fleet envelope._\n")

	body := []byte(sb.String())
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return err
	}
	return r.writeMachineArtifacts(path, body, metadata, time.Now().UTC())
}

type drillEvidence struct {
	SchemaVersion           string            `json:"schema_version"`
	Drill                   string            `json:"drill"`
	Status                  string            `json:"status"`
	Commit                  string            `json:"commit"`
	Profile                 string            `json:"profile"`
	SourceWorkflow          string            `json:"source_workflow"`
	SourceRunID             string            `json:"source_run_id"`
	SourceCommit            string            `json:"source_commit"`
	SourceConclusion        string            `json:"source_conclusion"`
	SourceEvent             string            `json:"source_event"`
	Images                  string            `json:"images"`
	Environment             string            `json:"environment"`
	ExecutionEvidencePath   string            `json:"execution_evidence_path"`
	ExecutionEvidenceSHA256 string            `json:"execution_evidence_sha256"`
	DriverResult            drillDriverResult `json:"driver_result"`
	StartedAt               time.Time         `json:"started_at"`
	CompletedAt             time.Time         `json:"completed_at"`
}

type drillDriverResult struct {
	SchemaVersion int    `json:"schema_version"`
	Drill         string `json:"drill"`
	Status        string `json:"status"`
	Target        struct {
		Environment           string `json:"environment"`
		Profile               string `json:"profile"`
		ReleaseManifestDigest string `json:"release_manifest_digest"`
	} `json:"target"`
	Precondition struct {
		ObservedAt time.Time `json:"observed_at"`
		Metric     string    `json:"metric"`
		Value      any       `json:"value"`
	} `json:"precondition"`
	Injection struct {
		OperationID      string    `json:"operation_id"`
		InjectedAt       time.Time `json:"injected_at"`
		EffectObservedAt time.Time `json:"effect_observed_at"`
		Metric           string    `json:"metric"`
		Value            any       `json:"value"`
	} `json:"injection"`
	Recovery struct {
		RecoveredAt time.Time `json:"recovered_at"`
		Metric      string    `json:"metric"`
		Value       any       `json:"value"`
	} `json:"recovery"`
	Cleanup struct {
		CompletedAt time.Time `json:"completed_at"`
		Restored    bool      `json:"restored"`
	} `json:"cleanup"`
}

type drillEvidenceResult struct {
	Status string
	Detail string
	Suffix string
}

type drillEvidenceManifest struct {
	SchemaVersion           string    `json:"schema_version"`
	QualificationWorkflow   string    `json:"qualification_workflow"`
	QualificationRunID      string    `json:"qualification_run_id"`
	QualificationConclusion string    `json:"qualification_conclusion"`
	QualificationCommit     string    `json:"qualification_commit"`
	RawExecutionWorkflow    string    `json:"raw_execution_workflow"`
	RawExecutionRunID       string    `json:"raw_execution_run_id"`
	RawExecutionConclusion  string    `json:"raw_execution_conclusion"`
	RawExecutionCommit      string    `json:"raw_execution_commit"`
	RawExecutionEvent       string    `json:"raw_execution_event"`
	RawArtifact             string    `json:"raw_artifact"`
	Artifact                string    `json:"artifact"`
	CompletedAt             time.Time `json:"completed_at"`
	Files                   []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func loadDrillEvidenceManifest(dir string, metadata certificationMetadata, now time.Time) (*drillEvidenceManifest, error) {
	path := filepath.Join(dir, "drill-evidence-manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("signed drill evidence manifest is missing: %w", err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if digest != metadata.DrillEvidenceManifestSHA256 {
		return nil, fmt.Errorf("drill evidence manifest digest does not match the verified signature input")
	}
	var manifest drillEvidenceManifest
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("drill evidence manifest schema is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("drill evidence manifest contains trailing content")
	}
	checks := []struct{ field, got, want string }{
		{"schema_version", manifest.SchemaVersion, drillManifestSchemaVersion},
		{"qualification_workflow", manifest.QualificationWorkflow, trustedDrillEvidenceWorkflow},
		{"source_workflow_metadata", metadata.DrillEvidenceWorkflow, trustedDrillEvidenceWorkflow},
		{"qualification_run_id", manifest.QualificationRunID, metadata.DrillEvidenceRunID},
		{"qualification_conclusion", manifest.QualificationConclusion, "success"},
		{"source_conclusion_metadata", metadata.DrillEvidenceConclusion, "success"},
		{"qualification_commit", manifest.QualificationCommit, metadata.Commit},
		{"raw_execution_workflow", manifest.RawExecutionWorkflow, ".github/workflows/day2-drill-execution.yaml"},
		{"raw_execution_conclusion", manifest.RawExecutionConclusion, "success"},
		{"raw_execution_commit", manifest.RawExecutionCommit, metadata.Commit},
		{"raw_execution_event", manifest.RawExecutionEvent, "workflow_dispatch"},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.got) == "" || check.got != check.want {
			return nil, fmt.Errorf("%s does not match the verified certification provenance", check.field)
		}
	}
	if strings.TrimSpace(manifest.Artifact) == "" || strings.TrimSpace(manifest.RawArtifact) == "" ||
		strings.TrimSpace(manifest.RawExecutionRunID) == "" || len(manifest.Files) == 0 {
		return nil, fmt.Errorf("drill evidence manifest has no artifact or files")
	}
	if manifest.CompletedAt.IsZero() || manifest.CompletedAt.After(now.Add(maximumClockSkew)) || manifest.CompletedAt.Before(now.Add(-maximumDrillEvidenceAge)) {
		return nil, fmt.Errorf("drill evidence manifest completion timestamp is invalid or stale")
	}
	return &manifest, nil
}

func loadDrillEvidence(drill, profile string, metadata certificationMetadata, now time.Time) drillEvidenceResult {
	dir := strings.TrimSpace(os.Getenv("LOADTEST_DRILL_EVIDENCE_DIR"))
	if dir == "" {
		return drillEvidenceResult{Status: "planned", Detail: "evidence directory is not configured"}
	}
	manifest, err := loadDrillEvidenceManifest(dir, metadata, now)
	if err != nil {
		return drillEvidenceResult{Status: "invalid", Detail: err.Error()}
	}
	path := filepath.Join(dir, drill+".json")
	suffix := " (`" + filepath.Base(path) + "`)"
	raw, err := os.ReadFile(path)
	if err != nil {
		return drillEvidenceResult{Status: "missing", Detail: "file is missing", Suffix: suffix}
	}
	wantDigest := ""
	for _, file := range manifest.Files {
		if file.Path == filepath.Base(path) {
			wantDigest = file.SHA256
			break
		}
	}
	if wantDigest == "" || fmt.Sprintf("%x", sha256.Sum256(raw)) != wantDigest {
		return drillEvidenceResult{Status: "invalid", Detail: "file digest is absent from or mismatches the signed manifest", Suffix: suffix}
	}
	var evidence drillEvidence
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return drillEvidenceResult{Status: "invalid", Detail: "JSON does not match the evidence schema", Suffix: suffix}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return drillEvidenceResult{Status: "invalid", Detail: "JSON contains trailing content", Suffix: suffix}
	}
	checks := []struct {
		field string
		got   string
		want  string
	}{
		{"schema_version", evidence.SchemaVersion, drillEvidenceSchemaVersion},
		{"drill", evidence.Drill, drill},
		{"status", evidence.Status, "pass"},
		{"commit", evidence.Commit, metadata.Commit},
		{"profile", evidence.Profile, profile},
		{"source_workflow", evidence.SourceWorkflow, manifest.RawExecutionWorkflow},
		{"source_run_id", evidence.SourceRunID, manifest.RawExecutionRunID},
		{"source_commit", evidence.SourceCommit, manifest.RawExecutionCommit},
		{"source_conclusion", evidence.SourceConclusion, manifest.RawExecutionConclusion},
		{"source_event", evidence.SourceEvent, manifest.RawExecutionEvent},
		{"images", evidence.Images, metadata.Images},
		{"environment", evidence.Environment, metadata.Environment},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.got) == "" || check.got != check.want {
			return drillEvidenceResult{Status: "invalid", Detail: check.field + " does not match the certification run", Suffix: suffix}
		}
	}
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(evidence.Images) || strings.TrimSpace(evidence.Environment) == "" {
		return drillEvidenceResult{Status: "invalid", Detail: "release-manifest digest or environment is invalid", Suffix: suffix}
	}
	if evidence.ExecutionEvidencePath != drill+".log" ||
		!regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(evidence.ExecutionEvidenceSHA256) {
		return drillEvidenceResult{Status: "invalid", Detail: "execution evidence reference is invalid", Suffix: suffix}
	}
	executionRaw, executionErr := os.ReadFile(filepath.Join(dir, evidence.ExecutionEvidencePath))
	if executionErr != nil || fmt.Sprintf("%x", sha256.Sum256(executionRaw)) != evidence.ExecutionEvidenceSHA256 {
		return drillEvidenceResult{Status: "invalid", Detail: "execution evidence log digest mismatch", Suffix: suffix}
	}
	driver := evidence.DriverResult
	if driver.SchemaVersion != 1 || driver.Drill != drill || driver.Status != "pass" ||
		driver.Target.Environment != metadata.Environment || driver.Target.Profile != profile ||
		driver.Target.ReleaseManifestDigest != metadata.Images || !driver.Cleanup.Restored ||
		strings.TrimSpace(driver.Injection.OperationID) == "" || strings.TrimSpace(driver.Precondition.Metric) == "" ||
		strings.TrimSpace(driver.Injection.Metric) == "" || strings.TrimSpace(driver.Recovery.Metric) == "" ||
		driver.Precondition.Value == nil || driver.Injection.Value == nil || driver.Recovery.Value == nil {
		return drillEvidenceResult{Status: "invalid", Detail: "driver lifecycle result is incomplete or mismatched", Suffix: suffix}
	}
	timeline := []time.Time{driver.Precondition.ObservedAt, driver.Injection.InjectedAt, driver.Injection.EffectObservedAt, driver.Recovery.RecoveredAt, driver.Cleanup.CompletedAt}
	for index, timestamp := range timeline {
		if timestamp.IsZero() || (index > 0 && timestamp.Before(timeline[index-1])) {
			return drillEvidenceResult{Status: "invalid", Detail: "driver lifecycle timeline is invalid", Suffix: suffix}
		}
	}
	if evidence.StartedAt.IsZero() || evidence.CompletedAt.IsZero() {
		return drillEvidenceResult{Status: "invalid", Detail: "started_at and completed_at must be RFC3339 timestamps", Suffix: suffix}
	}
	if evidence.CompletedAt.Before(evidence.StartedAt) {
		return drillEvidenceResult{Status: "invalid", Detail: "completed_at precedes started_at", Suffix: suffix}
	}
	if evidence.CompletedAt.After(now.Add(maximumClockSkew)) {
		return drillEvidenceResult{Status: "invalid", Detail: "completed_at is in the future", Suffix: suffix}
	}
	if evidence.CompletedAt.Before(now.Add(-maximumDrillEvidenceAge)) {
		return drillEvidenceResult{Status: "stale", Detail: "completed_at is older than 30 days", Suffix: suffix}
	}
	return drillEvidenceResult{Status: "pass", Detail: "provenance matched", Suffix: suffix}
}

func validateConfiguredDrillEvidence(cfg *config, now time.Time) error {
	if cfg == nil || strings.TrimSpace(cfg.profileName) == "" {
		return fmt.Errorf("a named scale profile is required")
	}
	if len(cfg.day2FailureDrill) == 0 {
		return fmt.Errorf("profile %s defines no day-2 drills", cfg.profileName)
	}
	metadata := loadCertificationMetadata()
	if missing := metadata.missing(); len(missing) > 0 {
		return fmt.Errorf("certification metadata missing: %s", strings.Join(missing, ", "))
	}
	if _, err := metadata.componentReplicaCounts(); err != nil {
		return err
	}
	for _, drill := range cfg.day2FailureDrill {
		result := loadDrillEvidence(drill, cfg.profileName, metadata, now)
		if result.Status != "pass" {
			return fmt.Errorf("%s: %s", drill, result.Detail)
		}
	}
	return nil
}

func (r *report) writeMachineArtifacts(path string, markdown []byte, metadata certificationMetadata, generatedAt time.Time) error {
	r.rec.mu.Lock()
	totalRequests := 0
	scenarios := map[string]any{}
	for name, count := range r.rec.httpCount {
		totalRequests += count
		samples := r.rec.httpSamples[name]
		scenarios[name] = map[string]any{
			"requests": count, "errors": r.rec.httpErrors[name], "status_codes": r.rec.httpStatus[name],
			"p50_ms": percentile(samples, .50).Milliseconds(), "p95_ms": percentile(samples, .95).Milliseconds(),
			"p99_ms": percentile(samples, .99).Milliseconds(),
		}
	}
	observedDuration := r.rec.endedAt.Sub(r.rec.startedAt)
	observedRPS := 0.0
	if observedDuration > 0 {
		observedRPS = float64(totalRequests) / observedDuration.Seconds()
	}
	cardinality := map[string]int{
		"pods":        r.rec.resourceCardinality["PodList"],
		"deployments": r.rec.resourceCardinality["DeploymentList"],
		"services":    r.rec.resourceCardinality["ServiceList"],
	}
	conservation := map[string]any{
		"run_id": r.rec.auditConservation.RunID, "attempted": r.rec.auditConservation.Attempted,
		"accepted": r.rec.auditConservation.Accepted, "rejected": r.rec.auditConservation.Rejected,
		"intents_observed": r.rec.auditConservation.IntentsObserved, "canonical_rows": r.rec.auditConservation.CanonicalRows,
		"duplicates": r.rec.auditConservation.Duplicates, "lost": r.rec.auditConservation.Lost,
		"reconciled":          r.rec.auditConservation.Reconciled,
		"max_delivery_lag_ms": r.rec.auditConservation.MaxDeliveryLag.Milliseconds(),
		"window_started_at":   r.rec.auditConservation.WindowStartedAt.UTC().Format(time.RFC3339Nano),
		"window_ended_at":     r.rec.auditConservation.WindowEndedAt.UTC().Format(time.RFC3339Nano),
		"first_accepted_at":   r.rec.auditConservation.FirstAcceptedAt.UTC().Format(time.RFC3339Nano),
		"last_accepted_at":    r.rec.auditConservation.LastAcceptedAt.UTC().Format(time.RFC3339Nano),
		"intent_observer":     "read-only-postgresql-audit_outbox",
	}
	auditMetrics := map[string]any{
		"dropped_delta":        counterDelta(r.rec.scrapeSeries["audit_dropped_total"]),
		"write_failures_delta": counterDelta(r.rec.scrapeSeries["audit_write_failures_total"]),
		"active_rows_final":    lastValue(r.rec.scrapeSeries["audit_outbox_active_rows"]),
		"dead_rows_final":      lastValue(r.rec.scrapeSeries["audit_outbox_dead_rows"]),
	}
	componentMetrics := map[string]any{
		"server": map[string]any{
			"observed_rate": observedRPS, "rate_unit": "http_requests_per_second",
			"declared_load": float64(r.cfg.rps), "sample_count": totalRequests, "source": "traffic.observed_rps",
		},
		"worker": componentRateEvidence(r.rec.scrapeSeries["worker_jobs_total"], "jobs_per_second", float64(r.cfg.rps)),
		"tunnel": componentRateEvidence(r.rec.scrapeSeries["tunnel_state_updates_handled_total"], "state_updates_per_second", float64(r.cfg.resources.EventsPerSecond)),
		"audit": map[string]any{
			"observed_rate": safeRate(float64(r.rec.auditConservation.CanonicalRows), observedDuration),
			"declared_load": float64(r.cfg.mandatoryAudit.RatePerSecond),
			"rate_unit":     "canonical_audit_rows_per_second", "sample_count": r.rec.auditConservation.CanonicalRows,
			"source": "mandatory_audit.canonical_rows",
		},
	}
	leakEvidence := map[string]any{}
	for _, name := range []string{"server_goroutines", "server_heap_bytes", "server_open_fds"} {
		series := r.rec.scrapeSeries[name]
		baseline, terminal, ok := leakWindowAverages(series)
		leakEvidence[name] = map[string]any{
			"samples": len(series), "baseline_window_average": baseline,
			"terminal_window_average": terminal, "sufficient_evidence": ok,
		}
	}
	rawScrapes := map[string]any{}
	for name, series := range r.rec.scrapeSeries {
		points := make([]map[string]any, 0, len(series))
		for _, point := range series {
			points = append(points, map[string]any{
				"at": point.at.UTC().Format(time.RFC3339Nano), "value": point.value,
			})
		}
		rawScrapes[name] = points
	}
	r.rec.mu.Unlock()
	raw := map[string]any{
		"schema_version": scaleReportSchemaVersion, "generated_at": generatedAt.Format(time.RFC3339),
		"verdict": r.Verdict, "reasons": r.Reasons,
		"profile": r.cfg.profileName, "clusters": r.cfg.clusters, "target_rps": r.cfg.rps,
		"duration_seconds": r.cfg.duration.Seconds(), "certification": r.cfg.certification,
		"metadata": metadata, "scenarios": scenarios, "day2_failure_drills": r.cfg.day2FailureDrill,
		"traffic":              map[string]any{"requests": totalRequests, "observed_rps": observedRPS, "observed_duration_seconds": observedDuration.Seconds()},
		"resource_cardinality": cardinality, "state_events_emitted": r.rec.stateEventsEmitted,
		"mandatory_audit": conservation, "audit_metrics": auditMetrics,
		"component_metrics": componentMetrics,
		"scrape_series":     rawScrapes,
		"leak_evidence":     leakEvidence,
		"thresholds": map[string]any{
			"goroutine_ratio": r.thresh.goroutineLeakRatio, "heap_ratio": r.thresh.heapLeakRatio,
			"open_fd_ratio": r.thresh.openFDLeakRatio, "open_fd_growth": r.thresh.openFDGrowthMax,
		},
	}
	jsonBody, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".json", append(jsonBody, '\n'), 0o644); err != nil {
		return err
	}
	digest := sha256.Sum256(markdown)
	digestLine := fmt.Sprintf("%x  %s\n", digest, filepath.Base(path))
	return os.WriteFile(path+".sha256", []byte(digestLine), 0o644)
}

func componentRateEvidence(series []scrapePoint, unit string, declaredLoad float64) map[string]any {
	return map[string]any{
		"observed_rate": deltaPerSecond(series), "rate_unit": unit,
		"declared_load": declaredLoad, "sample_count": len(series), "source": "scrape_series",
	}
}

func safeRate(count float64, duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return count / duration.Seconds()
}

// writeFailureReport emits a minimal report containing only a VERDICT line
// when the harness aborts before producing real data (e.g. server
// unreachable). The VERDICT contract still holds: the file is grep-able.
func writeFailureReport(path string, runErr error) error {
	if path == "" {
		path = defaultOut
	}
	body := fmt.Sprintf(
		"# Astronomer Go — load-test report\n\nVERDICT: fail (harness error: %s)\n",
		runErr.Error(),
	)
	return os.WriteFile(path, []byte(body), 0o644)
}
