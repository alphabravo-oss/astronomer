package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// recorder is the single shared sink that everything in the harness writes
// to. It is concurrency-safe.
type recorder struct {
	mu sync.Mutex

	startedAt time.Time
	endedAt   time.Time

	// Per-scenario latency samples — capped so a 30-minute run at 1000 RPS
	// doesn't blow heap. p50/p95/p99 stay accurate as long as the reservoir
	// is representative.
	httpSamples map[string][]time.Duration
	httpCount   map[string]int
	httpErrors  map[string]int
	httpStatus  map[string]map[int]int

	// Agent-fleet counters.
	connectCount        int
	disconnectCount     int
	agentEndCount       int
	stateEventsEmitted  int64
	resourceCardinality map[string]int
	auditConservation   auditConservation

	// Scrape snapshots — append-only series keyed by metric name.
	scrapeSeries map[string][]scrapePoint

	// Local-process metrics — captured at the same cadence as remote scrapes
	// so the report has a baseline for the harness itself.
	driverGoroutines int
	driverHeapBytes  uint64
}

type auditConservation struct {
	RunID             string               `json:"run_id"`
	Attempted         int                  `json:"attempted"`
	Accepted          int                  `json:"accepted"`
	Rejected          int                  `json:"rejected"`
	IntentsObserved   int                  `json:"intents_observed"`
	CanonicalRows     int                  `json:"canonical_rows"`
	Duplicates        int                  `json:"duplicates"`
	Lost              int                  `json:"lost"`
	MaxDeliveryLag    time.Duration        `json:"-"`
	Reconciled        bool                 `json:"reconciled"`
	RequestAcceptedAt map[string]time.Time `json:"-"`
	WindowStartedAt   time.Time            `json:"-"`
	WindowEndedAt     time.Time            `json:"-"`
	FirstAcceptedAt   time.Time            `json:"-"`
	LastAcceptedAt    time.Time            `json:"-"`
}

func (r *recorder) beginAuditConservation(runID string) {
	r.mu.Lock()
	r.auditConservation = auditConservation{
		RunID: runID, RequestAcceptedAt: make(map[string]time.Time), WindowStartedAt: time.Now().UTC(),
	}
	r.mu.Unlock()
}

func (r *recorder) endAuditConservation() {
	r.mu.Lock()
	r.auditConservation.WindowEndedAt = time.Now().UTC()
	r.mu.Unlock()
}

func (r *recorder) recordAuditMutation(requestID string, accepted bool, acceptedAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auditConservation.Attempted++
	if accepted {
		r.auditConservation.Accepted++
		r.auditConservation.RequestAcceptedAt[requestID] = acceptedAt
		if r.auditConservation.FirstAcceptedAt.IsZero() || acceptedAt.Before(r.auditConservation.FirstAcceptedAt) {
			r.auditConservation.FirstAcceptedAt = acceptedAt
		}
		if r.auditConservation.LastAcceptedAt.IsZero() || acceptedAt.After(r.auditConservation.LastAcceptedAt) {
			r.auditConservation.LastAcceptedAt = acceptedAt
		}
	} else {
		r.auditConservation.Rejected++
	}
}

func (r *recorder) recordObservedAuditIntents(count int) {
	r.mu.Lock()
	r.auditConservation.IntentsObserved = count
	r.mu.Unlock()
}

func (r *recorder) finishAuditConservation(rows map[string][]time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	conservation := &r.auditConservation
	conservation.CanonicalRows = 0
	conservation.Duplicates = 0
	conservation.MaxDeliveryLag = 0
	for requestID, acceptedAt := range conservation.RequestAcceptedAt {
		timestamps := rows[requestID]
		if len(timestamps) == 0 {
			continue
		}
		conservation.CanonicalRows++
		if len(timestamps) > 1 {
			conservation.Duplicates += len(timestamps) - 1
		}
		for _, timestamp := range timestamps {
			if lag := timestamp.Sub(acceptedAt); lag > conservation.MaxDeliveryLag {
				conservation.MaxDeliveryLag = lag
			}
		}
	}
	conservation.Lost = conservation.Accepted - conservation.CanonicalRows
	conservation.Reconciled = conservation.Accepted > 0 && conservation.Lost == 0 && conservation.Duplicates == 0 && conservation.Rejected == 0
	return conservation.Reconciled
}

type scrapePoint struct {
	at    time.Time
	value float64
	// labels is omitted — for the metrics we care about (gauges + counters
	// without a meaningful label dimension at the fleet level), summing is
	// what we want.
}

const maxSamplesPerScenario = 50000

func newRecorder() *recorder {
	return &recorder{
		httpSamples:         make(map[string][]time.Duration),
		httpCount:           make(map[string]int),
		httpErrors:          make(map[string]int),
		httpStatus:          make(map[string]map[int]int),
		scrapeSeries:        make(map[string][]scrapePoint),
		resourceCardinality: make(map[string]int),
	}
}

func (r *recorder) RecordStateEvents(count int) {
	if count <= 0 {
		return
	}
	r.mu.Lock()
	r.stateEventsEmitted += int64(count)
	r.mu.Unlock()
}

func (r *recorder) RecordResourceCardinality(kind string, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if count > r.resourceCardinality[kind] {
		r.resourceCardinality[kind] = count
	}
}

func (r *recorder) MarkStart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startedAt = time.Now()
}

func (r *recorder) MarkEnd() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endedAt = time.Now()
}

// RecordHTTP captures one HTTP request outcome. status==0 indicates a
// transport error (caller passes a non-nil err).
func (r *recorder) RecordHTTP(name string, status int, elapsed time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.httpCount[name]++
	if err != nil {
		r.httpErrors[name]++
	}
	if status != 0 {
		if r.httpStatus[name] == nil {
			r.httpStatus[name] = make(map[int]int)
		}
		r.httpStatus[name][status]++
	}
	samples := r.httpSamples[name]
	if len(samples) < maxSamplesPerScenario {
		samples = append(samples, elapsed)
		r.httpSamples[name] = samples
	}
}

func (r *recorder) RecordConnect() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connectCount++
}

func (r *recorder) RecordDisconnect() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disconnectCount++
}

func (r *recorder) RecordAgentEnd() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agentEndCount++
}

func (r *recorder) AppendScrape(metric string, at time.Time, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scrapeSeries[metric] = append(r.scrapeSeries[metric], scrapePoint{at: at, value: value})
}

func (r *recorder) snapshotDriverMetrics() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	g := runtime.NumGoroutine()
	r.mu.Lock()
	defer r.mu.Unlock()
	if uint64(g) > uint64(r.driverGoroutines) {
		r.driverGoroutines = g
	}
	if ms.HeapAlloc > r.driverHeapBytes {
		r.driverHeapBytes = ms.HeapAlloc
	}
}

// percentile returns the p-th percentile of samples (0 < p < 1). Returns 0
// for empty input. Samples are sorted in-place.
func percentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// ─────────────────────────────────────────────────────────────────────────────
// Prometheus scrape.
// ─────────────────────────────────────────────────────────────────────────────

// scrapeMetricsLoop polls the server's /metrics endpoint at metricsScrape
// cadence until ctx is cancelled.
func scrapeMetricsLoop(ctx context.Context, server, token string, rec *recorder, log *slog.Logger) {
	t := time.NewTicker(metricsScrape)
	defer t.Stop()
	if err := scrapeOnce(ctx, server, token, rec); err != nil {
		log.Warn("initial /metrics scrape failed", "error", err)
	}
	rec.snapshotDriverMetrics()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := scrapeOnce(ctx, server, token, rec); err != nil {
				log.Warn("/metrics scrape failed", "error", err)
			}
			rec.snapshotDriverMetrics()
		}
	}
}

// scrapeOnce performs a single scrape and stores selected metric snapshots.
func scrapeOnce(ctx context.Context, server, token string, rec *recorder) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/metrics", nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("scrape returned %d", resp.StatusCode)
	}

	// The zero-value TextParser carries UnsetValidation which panics on
	// IsValidMetricName. Construct it explicitly with UTF8Validation —
	// that's the default the rest of the prometheus ecosystem uses.
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return fmt.Errorf("parse metrics: %w", err)
	}
	now := time.Now()

	// For each "interesting" metric, sum across all label combinations and
	// record the aggregate. For agent_connections we want a sum (== number
	// of connected agents); for db_pool_max we want a sum (single label
	// dimension == max); for worker_queue_depth{state=pending} we filter.
	for name, fam := range families {
		switch name {
		case "astronomer_worker_queue_depth":
			// Only the pending state matters for DLQ-growth.
			var pending float64
			for _, m := range fam.GetMetric() {
				if labelValue(m, "state") == "pending" {
					pending += m.GetGauge().GetValue()
				}
			}
			rec.AppendScrape("worker_queue_pending", now, pending)
		case "astronomer_worker_queue_latency_seconds":
			rec.AppendScrape("worker_queue_age_seconds", now, maxGauge(fam))
		case "astronomer_dropped_events_total":
			var total float64
			for _, m := range fam.GetMetric() {
				total += m.GetCounter().GetValue()
			}
			rec.AppendScrape("dropped_events_total", now, total)
		case "astronomer_agent_connections":
			// Sum gauge across all clusters. updateConnectionMetrics() in
			// internal/tunnel sets value=1 per connected cluster.
			var sum float64
			for _, m := range fam.GetMetric() {
				sum += m.GetGauge().GetValue()
			}
			rec.AppendScrape("agent_connections", now, sum)
		case "astronomer_agent_reconnects_total":
			rec.AppendScrape("agent_reconnects_total", now, sumCounters(fam))
		case "astronomer_worker_jobs_total":
			rec.AppendScrape("worker_jobs_total", now, sumCountersWithLabel(fam, "status", "success"))
		case "astronomer_tunnel_state_updates_handled_total":
			rec.AppendScrape("tunnel_state_updates_handled_total", now, sumCountersWithLabel(fam, "outcome", "published"))
		case "astronomer_audit_dropped_total":
			rec.AppendScrape("audit_dropped_total", now, sumCounters(fam))
		case "astronomer_audit_write_failures_total":
			rec.AppendScrape("audit_write_failures_total", now, sumCounters(fam))
		case "astronomer_audit_outbox_rows":
			var active, dead float64
			for _, metric := range fam.GetMetric() {
				value := metric.GetGauge().GetValue()
				switch labelValue(metric, "status") {
				case "pending", "failed", "delivering":
					active += value
				case "dead":
					dead += value
				}
			}
			rec.AppendScrape("audit_outbox_active_rows", now, active)
			rec.AppendScrape("audit_outbox_dead_rows", now, dead)
		case "astronomer_delivery_cohort_latency_seconds":
			sum, count := sumHistograms(fam)
			if count > 0 {
				rec.AppendScrape("delivery_cohort_latency_avg_seconds", now, sum/count)
			}
		case "cache_invalidation_lag_seconds":
			rec.AppendScrape("event_relay_lag_seconds", now, maxGauge(fam))
		case "astronomer_db_pool_acquired_connections":
			rec.AppendScrape("db_pool_acquired", now, sumGauges(fam))
		case "astronomer_db_pool_max_connections":
			rec.AppendScrape("db_pool_max", now, sumGauges(fam))
		case "astronomer_db_pool_empty_acquire_count_total":
			rec.AppendScrape("db_pool_empty_acquire", now, sumCounters(fam))
		case "go_goroutines":
			rec.AppendScrape("server_goroutines", now, sumGauges(fam))
		case "go_memstats_alloc_bytes":
			rec.AppendScrape("server_heap_bytes", now, sumGauges(fam))
		case "process_open_fds":
			rec.AppendScrape("server_open_fds", now, sumGauges(fam))
		}
	}
	return nil
}

func sumGauges(fam *dto.MetricFamily) float64 {
	var sum float64
	for _, m := range fam.GetMetric() {
		if g := m.GetGauge(); g != nil {
			sum += g.GetValue()
		}
	}
	return sum
}

func sumCounters(fam *dto.MetricFamily) float64 {
	var sum float64
	for _, m := range fam.GetMetric() {
		if c := m.GetCounter(); c != nil {
			sum += c.GetValue()
		}
	}
	return sum
}

func sumCountersWithLabel(fam *dto.MetricFamily, label, value string) float64 {
	var sum float64
	for _, metric := range fam.GetMetric() {
		if labelValue(metric, label) == value && metric.GetCounter() != nil {
			sum += metric.GetCounter().GetValue()
		}
	}
	return sum
}

func maxGauge(fam *dto.MetricFamily) float64 {
	var max float64
	for _, metric := range fam.GetMetric() {
		if gauge := metric.GetGauge(); gauge != nil && gauge.GetValue() > max {
			max = gauge.GetValue()
		}
	}
	return max
}

func sumHistograms(fam *dto.MetricFamily) (sum, count float64) {
	for _, metric := range fam.GetMetric() {
		if histogram := metric.GetHistogram(); histogram != nil {
			sum += histogram.GetSampleSum()
			count += float64(histogram.GetSampleCount())
		}
	}
	return sum, count
}

func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// peakValue returns the maximum value across a series. Returns 0 for empty.
func peakValue(series []scrapePoint) float64 {
	var peak float64
	for _, p := range series {
		if p.value > peak {
			peak = p.value
		}
	}
	return peak
}

// firstValue returns the first observed sample, or 0 if none.
func firstValue(series []scrapePoint) float64 {
	if len(series) == 0 {
		return 0
	}
	return series[0].value
}

// lastValue returns the most recent sample, or 0 if none.
func lastValue(series []scrapePoint) float64 {
	if len(series) == 0 {
		return 0
	}
	return series[len(series)-1].value
}

// leakWindowAverages returns averages for a post-warm-up baseline window and
// the terminal window. It deliberately discards the first quarter of samples,
// where synthetic agents are still establishing their expected steady-state
// connections. Five points (75 seconds at the default cadence) are averaged
// when available to reduce GC and scrape jitter.
func leakWindowAverages(series []scrapePoint) (baseline, terminal float64, ok bool) {
	if len(series) < minimumLeakSamples {
		return 0, 0, false
	}
	window := len(series) / 4
	if window > 5 {
		window = 5
	}
	baselineStart := len(series) / 4
	terminalStart := len(series) - window
	for _, point := range series[baselineStart : baselineStart+window] {
		baseline += point.value
	}
	for _, point := range series[terminalStart:] {
		terminal += point.value
	}
	return baseline / float64(window), terminal / float64(window), true
}

// deltaPerSecond returns (last-first)/duration_seconds. Returns 0 if the
// series doesn't span any time.
func deltaPerSecond(series []scrapePoint) float64 {
	if len(series) < 2 {
		return 0
	}
	first := series[0]
	last := series[len(series)-1]
	dt := last.at.Sub(first.at).Seconds()
	if dt <= 0 {
		return 0
	}
	return (last.value - first.value) / dt
}
