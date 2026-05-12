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
	connectCount    int
	disconnectCount int
	agentEndCount   int

	// Scrape snapshots — append-only series keyed by metric name.
	scrapeSeries map[string][]scrapePoint

	// Local-process metrics — captured at the same cadence as remote scrapes
	// so the report has a baseline for the harness itself.
	driverGoroutines int
	driverHeapBytes  uint64
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
		httpSamples:  make(map[string][]time.Duration),
		httpCount:    make(map[string]int),
		httpErrors:   make(map[string]int),
		httpStatus:   make(map[string]map[int]int),
		scrapeSeries: make(map[string][]scrapePoint),
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

// scrapedMetrics lists the metric names the report cares about. The scrape
// stores ALL families we see, but the report only inspects these.
var scrapedMetrics = []string{
	"astronomer_agent_connections",
	"astronomer_db_pool_acquired_connections",
	"astronomer_db_pool_max_connections",
	"astronomer_db_pool_empty_acquire_count_total",
	"astronomer_dropped_events_total",
	"astronomer_worker_queue_depth",
	"go_goroutines",
	"go_memstats_alloc_bytes",
	"astronomer_http_request_duration_seconds_count",
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
	defer resp.Body.Close()
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
