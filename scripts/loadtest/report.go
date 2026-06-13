package main

import (
	"fmt"
	"os"
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
	goroutineLeakRatio float64 // 1.5 == 50% growth tolerated
}

func defaultThresholds() thresholds {
	return thresholds{
		clusterListP99Ms:   envOrInt("LOADTEST_THRESH_CLUSTER_P99_MS", 500),
		resourcesP99Ms:     envOrInt("LOADTEST_THRESH_RESOURCES_P99_MS", 2000),
		connectedAgentsMin: envOrFloat("LOADTEST_THRESH_CONNECTED_MIN", 1.0),
		dlqDepthMax:        envOrFloat("LOADTEST_THRESH_DLQ_MAX", 10),
		emptyAcquireMaxQPS: envOrFloat("LOADTEST_THRESH_EMPTY_ACQUIRE_QPS", 0.1),
		goroutineLeakRatio: envOrFloat("LOADTEST_THRESH_GOROUTINE_RATIO", 1.5),
	}
}

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

	// 6. Goroutine leak — peak/start ratio. A small overhead is normal
	//    (the workload itself spawns goroutines), so 1.5x is the default
	//    floor.
	startG := firstValue(r.rec.scrapeSeries["server_goroutines"])
	endG := lastValue(r.rec.scrapeSeries["server_goroutines"])
	if startG > 0 && endG/startG > r.thresh.goroutineLeakRatio {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("server goroutines grew %.0f->%.0f (>%.1fx threshold)",
				startG, endG, r.thresh.goroutineLeakRatio))
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

// WriteFile renders the markdown report to path.
func (r *report) WriteFile(path string) error {
	var sb strings.Builder

	sb.WriteString("# Astronomer Go — load-test report\n\n")
	sb.WriteString(fmt.Sprintf("- Generated: `%s`\n", time.Now().UTC().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("- Server: `%s`\n", r.cfg.server))
	if r.cfg.profileName != "" {
		sb.WriteString(fmt.Sprintf("- Profile: `%s`\n", r.cfg.profileName))
	}
	sb.WriteString(fmt.Sprintf("- Clusters: `%d`\n", r.cfg.clusters))
	sb.WriteString(fmt.Sprintf("- Target RPS: `%d`\n", r.cfg.rps))
	sb.WriteString(fmt.Sprintf("- Configured duration: `%s`\n", r.cfg.duration))
	sb.WriteString(fmt.Sprintf("- Resources per cluster: pods `%d`, deployments `%d`, services `%d`\n",
		r.cfg.resources.PodsPerCluster,
		r.cfg.resources.DeploymentsPerCluster,
		r.cfg.resources.ServicesPerCluster,
	))
	if r.cfg.reconnectStorm.Enabled {
		sb.WriteString(fmt.Sprintf("- Reconnect storm: `%d%%` of agents at `%s` with `%s` jitter\n",
			r.cfg.reconnectStorm.BatchPercent,
			r.cfg.reconnectStorm.AtDuration,
			r.cfg.reconnectStorm.JitterDuration,
		))
	}
	r.rec.mu.Lock()
	if !r.rec.startedAt.IsZero() && !r.rec.endedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("- Observed run window: `%s` (%s → %s)\n",
			r.rec.endedAt.Sub(r.rec.startedAt).Round(time.Second),
			r.rec.startedAt.UTC().Format(time.RFC3339),
			r.rec.endedAt.UTC().Format(time.RFC3339)))
	}
	r.rec.mu.Unlock()
	sb.WriteString("\n")

	// ── Verdict block — surfaced first so a grep on the file reads as a
	//    quick pass/fail without parsing the rest.
	sb.WriteString(fmt.Sprintf("VERDICT: %s\n\n", r.Verdict))
	if len(r.Reasons) > 0 {
		sb.WriteString("## Notes\n\n")
		for _, reason := range r.Reasons {
			sb.WriteString("- ")
			sb.WriteString(reason)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(r.cfg.day2FailureDrill) > 0 {
		sb.WriteString("## Day-2 failure drills in profile\n\n")
		sb.WriteString("| Drill | Status |\n|---|---|\n")
		for _, drill := range r.cfg.day2FailureDrill {
			sb.WriteString(fmt.Sprintf("| %s | planned |\n", drill))
		}
		sb.WriteString("\n")
	}

	// ── Thresholds applied
	sb.WriteString("## Thresholds\n\n")
	sb.WriteString("| Threshold | Value |\n|---|---|\n")
	sb.WriteString(fmt.Sprintf("| cluster_list p99 | <= %dms |\n", r.thresh.clusterListP99Ms))
	sb.WriteString(fmt.Sprintf("| cluster_pods p99 | <= %dms |\n", r.thresh.resourcesP99Ms))
	sb.WriteString(fmt.Sprintf("| connected agents fraction | >= %.2f |\n", r.thresh.connectedAgentsMin))
	sb.WriteString(fmt.Sprintf("| worker pending depth | <= %.0f |\n", r.thresh.dlqDepthMax))
	sb.WriteString(fmt.Sprintf("| db pool empty-acquire rate | <= %.3f /s |\n", r.thresh.emptyAcquireMaxQPS))
	sb.WriteString(fmt.Sprintf("| goroutine peak/start ratio | <= %.2fx |\n", r.thresh.goroutineLeakRatio))
	sb.WriteString("\n")

	// ── Per-scenario latency table
	r.rec.mu.Lock()
	scenarioNames := make([]string, 0, len(r.rec.httpCount))
	for name := range r.rec.httpCount {
		scenarioNames = append(scenarioNames, name)
	}
	sort.Strings(scenarioNames)

	sb.WriteString("## HTTP latency per scenario\n\n")
	sb.WriteString("| Scenario | Requests | Errors | p50 | p95 | p99 |\n|---|---:|---:|---:|---:|---:|\n")
	for _, name := range scenarioNames {
		samples := r.rec.httpSamples[name]
		sb.WriteString(fmt.Sprintf("| %s | %d | %d | %v | %v | %v |\n",
			name,
			r.rec.httpCount[name],
			r.rec.httpErrors[name],
			percentile(samples, 0.50).Round(time.Millisecond),
			percentile(samples, 0.95).Round(time.Millisecond),
			percentile(samples, 0.99).Round(time.Millisecond),
		))
	}
	sb.WriteString("\n")

	// ── Status code breakdown
	sb.WriteString("## HTTP status codes\n\n")
	sb.WriteString("| Scenario | Codes |\n|---|---|\n")
	for _, name := range scenarioNames {
		codes := r.rec.httpStatus[name]
		if len(codes) == 0 {
			sb.WriteString(fmt.Sprintf("| %s | (no responses) |\n", name))
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
		sb.WriteString(fmt.Sprintf("| %s | %s |\n", name, strings.Join(parts, " ")))
	}
	sb.WriteString("\n")

	// ── Agent fleet
	sb.WriteString("## Agent fleet\n\n")
	sb.WriteString("| Metric | Value |\n|---|---:|\n")
	sb.WriteString(fmt.Sprintf("| Synthetic agents launched | %d |\n", r.cfg.clusters))
	sb.WriteString(fmt.Sprintf("| Successful CONNECT_ACKs | %d |\n", r.rec.connectCount))
	sb.WriteString(fmt.Sprintf("| Disconnects (reconnect attempts) | %d |\n", r.rec.disconnectCount))
	sb.WriteString(fmt.Sprintf("| Connected at end (gauge) | %.0f |\n", lastValue(r.rec.scrapeSeries["agent_connections"])))
	sb.WriteString("\n")

	// ── Server resource peaks
	sb.WriteString("## Server resource snapshot\n\n")
	sb.WriteString("| Metric | Peak | First | Last |\n|---|---:|---:|---:|\n")
	for _, m := range []string{
		"server_goroutines",
		"server_heap_bytes",
		"db_pool_acquired",
		"db_pool_max",
		"db_pool_empty_acquire",
		"worker_queue_pending",
		"agent_connections",
		"dropped_events_total",
	} {
		s := r.rec.scrapeSeries[m]
		sb.WriteString(fmt.Sprintf("| %s | %.0f | %.0f | %.0f |\n", m, peakValue(s), firstValue(s), lastValue(s)))
	}
	sb.WriteString("\n")

	// ── Driver resource peaks
	sb.WriteString("## Driver resource peaks\n\n")
	sb.WriteString("| Metric | Value |\n|---|---:|\n")
	sb.WriteString(fmt.Sprintf("| Driver peak goroutines | %d |\n", r.rec.driverGoroutines))
	sb.WriteString(fmt.Sprintf("| Driver peak heap bytes | %d |\n", r.rec.driverHeapBytes))
	sb.WriteString("\n")
	r.rec.mu.Unlock()

	sb.WriteString("---\n")
	sb.WriteString("_Generated by `scripts/loadtest`. See `docs/scale-baseline.md` for the cluster-fleet envelope._\n")

	return os.WriteFile(path, []byte(sb.String()), 0o644)
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
