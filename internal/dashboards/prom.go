// Package dashboards implements the server-side render half of the
// migration-058 dashboard widgets feature. Two subsystems live here:
//
//   - Prometheus query layer (QueryRange + EvalStat) — thin HTTP
//     client over the /api/v1/query and /api/v1/query_range endpoints
//     of any Prom-compatible TSDB (Prom, Mimir, Thanos query, VictoriaMetrics).
//     A 30s in-process LRU caches the matrix/value results so a
//     dashboard with N widgets at refresh_seconds=60 isn't punished
//     into N Prom calls per dashboard load.
//
//   - SVG sparkline renderer — server-side rasterisation to a tiny
//     200x60 SVG with a single polyline + min/max ticks. The client
//     drops the SVG straight into the DOM via dangerouslySetInnerHTML
//     so no client-side charting dependency is needed for the dashboard.
//
// Separation of concerns: this package knows nothing about HTTP
// request lifecycles, RBAC, or sqlc — it's pure rendering glue. The
// handler/dashboards.go file calls into here for the data side and
// owns all the request/response machinery.
package dashboards

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Datasource is the minimal Prometheus endpoint description. Auth is
// optional — either basic auth (username + password), bearer token, or
// none. TLSSkipVerify is opt-in for self-signed dev installs.
type Datasource struct {
	ID            string
	Name          string
	URL           string
	BasicAuthUser string
	BasicAuthPass string
	BearerToken   string
	TLSSkipVerify bool
}

// PromSample is one (timestamp, value) sample as returned by the
// Prom HTTP API. Time is in seconds-since-epoch (Prom's wire format)
// to avoid converting back to/from time.Time twice.
type PromSample struct {
	Time  float64
	Value float64
}

// PromMatrix is the matrix shape returned by /api/v1/query_range. We
// flatten Prom's [{metric, values}, ...] structure to a single slice
// of samples because the sparkline renderer doesn't distinguish series
// — it renders the sum/last/whatever the query author requested.
type PromMatrix struct {
	Samples []PromSample
}

// Min returns the minimum Value across the matrix. NaN-safe and
// zero-safe — an empty matrix returns (0, false).
func (m PromMatrix) Min() (float64, bool) {
	if len(m.Samples) == 0 {
		return 0, false
	}
	min := m.Samples[0].Value
	for _, s := range m.Samples[1:] {
		if s.Value < min {
			min = s.Value
		}
	}
	return min, true
}

// Max is the symmetric companion to Min.
func (m PromMatrix) Max() (float64, bool) {
	if len(m.Samples) == 0 {
		return 0, false
	}
	max := m.Samples[0].Value
	for _, s := range m.Samples[1:] {
		if s.Value > max {
			max = s.Value
		}
	}
	return max, true
}

// promQueryResp matches the Prometheus HTTP API envelope. We only
// decode the fields the renderer needs.
type promQueryResp struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string            `json:"resultType"`
		Result     []json.RawMessage `json:"result"`
	} `json:"data"`
	ErrorType string `json:"errorType,omitempty"`
	Error     string `json:"error,omitempty"`
}

// promMatrixResult is the per-series shape inside a matrix response.
type promMatrixResult struct {
	Metric map[string]string `json:"metric"`
	Values [][]any           `json:"values"`
}

// promVectorResult is the per-series shape inside a vector response.
type promVectorResult struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}

// httpClientFor returns a shared http.Client per (TLSSkipVerify) value.
// We don't share across skip-verify=true and skip-verify=false because
// the transport's TLS config is connection-pool-scoped.
var (
	clientMu      sync.Mutex
	httpClients   = map[bool]*http.Client{}
)

func httpClientFor(skipVerify bool) *http.Client {
	clientMu.Lock()
	defer clientMu.Unlock()
	if c, ok := httpClients[skipVerify]; ok {
		return c
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify}, // #nosec G402 — operator-opt-in
	}
	c := &http.Client{Timeout: 10 * time.Second, Transport: tr}
	httpClients[skipVerify] = c
	return c
}

// ── Cache ────────────────────────────────────────────────────────────

// cacheEntry holds a cached matrix or stat keyed by (datasource_id,
// query, duration). 30s TTL is intentionally tight — the widget
// refresh cadence at 60s defaults means at most one prom fetch per
// widget per 30s window, regardless of dashboard concurrency.
type cacheEntry struct {
	expires time.Time
	matrix  PromMatrix
	stat    float64
	statOK  bool
	kind    string // "matrix" | "stat"
}

// cache is a process-local sync.Map keyed on the string key returned by
// cacheKey. We use a map+mutex pair rather than singleflight because the
// hit-rate target is "every render after the first within 30s"; the
// stampede protection that singleflight buys isn't worth the code.
type promCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
	// Metrics hooks — set by the handler init.
	onHit  func()
	onMiss func()
}

// NewCache returns a fresh cache with the given TTL. Pass 30*time.Second
// for the canonical setting.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{c: &promCache{entries: map[string]cacheEntry{}, ttl: ttl}}
}

// Cache is the exported wrapper around the package-internal promCache.
// The handler holds one of these and threads it into every QueryRange /
// EvalStat call.
type Cache struct {
	c *promCache
}

// SetMetrics wires the cache's hit/miss counter hooks. Optional —
// when nil, cache operations don't emit metrics.
func (c *Cache) SetMetrics(onHit, onMiss func()) {
	if c == nil || c.c == nil {
		return
	}
	c.c.mu.Lock()
	defer c.c.mu.Unlock()
	c.c.onHit = onHit
	c.c.onMiss = onMiss
}

func cacheKey(dsID, query, duration string) string {
	return dsID + "\x00" + query + "\x00" + duration
}

// getMatrix returns a cached matrix if present + still valid.
func (c *promCache) getMatrix(k string) (PromMatrix, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok || e.kind != "matrix" || time.Now().After(e.expires) {
		if c.onMiss != nil {
			c.onMiss()
		}
		return PromMatrix{}, false
	}
	if c.onHit != nil {
		c.onHit()
	}
	return e.matrix, true
}

func (c *promCache) putMatrix(k string, m PromMatrix) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[k] = cacheEntry{
		expires: time.Now().Add(c.ttl),
		matrix:  m,
		kind:    "matrix",
	}
}

func (c *promCache) getStat(k string) (float64, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok || e.kind != "stat" || time.Now().After(e.expires) {
		if c.onMiss != nil {
			c.onMiss()
		}
		return 0, false, false
	}
	if c.onHit != nil {
		c.onHit()
	}
	return e.stat, e.statOK, true
}

func (c *promCache) putStat(k string, v float64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[k] = cacheEntry{
		expires: time.Now().Add(c.ttl),
		stat:    v,
		statOK:  ok,
		kind:    "stat",
	}
}

// ── Query helpers ─────────────────────────────────────────────────────

// parseDuration accepts Prometheus-style suffixes (s, m, h, d, w) and
// returns a time.Duration. Used both for the lookback window and the
// step size. Unknown suffixes fall back to time.ParseDuration so
// callers can use the standard Go format too.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	// Quick fast-path for the canonical "<int><unit>" cases.
	if len(s) >= 2 {
		unit := s[len(s)-1]
		nstr := s[:len(s)-1]
		if n, err := strconv.Atoi(nstr); err == nil {
			switch unit {
			case 's':
				return time.Duration(n) * time.Second, nil
			case 'm':
				return time.Duration(n) * time.Minute, nil
			case 'h':
				return time.Duration(n) * time.Hour, nil
			case 'd':
				return time.Duration(n) * 24 * time.Hour, nil
			case 'w':
				return time.Duration(n) * 7 * 24 * time.Hour, nil
			}
		}
	}
	return time.ParseDuration(s)
}

// QueryRange runs a range query against the datasource and returns the
// flattened sample list. The cache key uses (datasource_id, query,
// duration) so consecutive client polls inside the TTL share one
// upstream fetch. `now` is the right-edge of the range — pass
// time.Now() in production; tests pin it to a fixed time for golden
// matrix shapes.
func QueryRange(ctx context.Context, cache *Cache, ds Datasource, query, duration, step string, now time.Time) (PromMatrix, error) {
	dur, err := parseDuration(duration)
	if err != nil {
		return PromMatrix{}, fmt.Errorf("invalid duration: %w", err)
	}
	stepDur, err := parseDuration(step)
	if err != nil {
		return PromMatrix{}, fmt.Errorf("invalid step: %w", err)
	}
	if stepDur <= 0 {
		stepDur = 60 * time.Second
	}
	key := cacheKey(ds.ID, query, duration+"|"+step)
	if cache != nil {
		if m, ok := cache.c.getMatrix(key); ok {
			return m, nil
		}
	}
	base := strings.TrimRight(ds.URL, "/")
	endpoint := base + "/api/v1/query_range"
	startT := now.Add(-dur)
	values := url.Values{}
	values.Set("query", query)
	values.Set("start", strconv.FormatFloat(float64(startT.Unix()), 'f', -1, 64))
	values.Set("end", strconv.FormatFloat(float64(now.Unix()), 'f', -1, 64))
	values.Set("step", strconv.FormatFloat(stepDur.Seconds(), 'f', -1, 64))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+values.Encode(), nil)
	if err != nil {
		return PromMatrix{}, err
	}
	applyAuth(req, ds)
	resp, err := httpClientFor(ds.TLSSkipVerify).Do(req)
	if err != nil {
		return PromMatrix{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return PromMatrix{}, err
	}
	if resp.StatusCode/100 != 2 {
		return PromMatrix{}, fmt.Errorf("prometheus %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var env promQueryResp
	if err := json.Unmarshal(body, &env); err != nil {
		return PromMatrix{}, fmt.Errorf("prometheus decode: %w", err)
	}
	if env.Status != "success" {
		return PromMatrix{}, fmt.Errorf("prometheus error %s: %s", env.ErrorType, env.Error)
	}
	var samples []PromSample
	for _, raw := range env.Data.Result {
		var r promMatrixResult
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		for _, pair := range r.Values {
			s, ok := parseSample(pair)
			if !ok {
				continue
			}
			samples = append(samples, s)
		}
	}
	m := PromMatrix{Samples: samples}
	if cache != nil {
		cache.c.putMatrix(key, m)
	}
	return m, nil
}

// EvalStat runs an instant query and returns the last sample's value.
// (value, ok=true, nil) when the query returned at least one numeric
// sample; (0, false, nil) when the query succeeded but had no samples;
// (0, false, err) on transport / decode errors.
func EvalStat(ctx context.Context, cache *Cache, ds Datasource, query string) (float64, bool, error) {
	key := cacheKey(ds.ID, query, "instant")
	if cache != nil {
		if v, ok, hit := cache.c.getStat(key); hit {
			return v, ok, nil
		}
	}
	base := strings.TrimRight(ds.URL, "/")
	endpoint := base + "/api/v1/query"
	values := url.Values{}
	values.Set("query", query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+values.Encode(), nil)
	if err != nil {
		return 0, false, err
	}
	applyAuth(req, ds)
	resp, err := httpClientFor(ds.TLSSkipVerify).Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, false, err
	}
	if resp.StatusCode/100 != 2 {
		return 0, false, fmt.Errorf("prometheus %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var env promQueryResp
	if err := json.Unmarshal(body, &env); err != nil {
		return 0, false, fmt.Errorf("prometheus decode: %w", err)
	}
	if env.Status != "success" {
		return 0, false, fmt.Errorf("prometheus error %s: %s", env.ErrorType, env.Error)
	}
	var lastVal float64
	have := false
	for _, raw := range env.Data.Result {
		var r promVectorResult
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		s, ok := parseSample(r.Value)
		if !ok {
			continue
		}
		lastVal = s.Value
		have = true
	}
	if cache != nil {
		cache.c.putStat(key, lastVal, have)
	}
	return lastVal, have, nil
}

// applyAuth attaches the per-datasource credentials to the outbound
// request. Bearer wins over basic auth when both are set; an empty
// Datasource passes through unauthenticated.
func applyAuth(req *http.Request, ds Datasource) {
	if ds.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+ds.BearerToken)
		return
	}
	if ds.BasicAuthUser != "" || ds.BasicAuthPass != "" {
		req.SetBasicAuth(ds.BasicAuthUser, ds.BasicAuthPass)
	}
}

// parseSample handles Prom's `[<unix_seconds>, "<float_as_string>"]`
// representation. The float is shipped as a string so JSON's "all
// numbers are floats" precision loss doesn't apply.
func parseSample(pair []any) (PromSample, bool) {
	if len(pair) != 2 {
		return PromSample{}, false
	}
	t, ok := toFloat(pair[0])
	if !ok {
		return PromSample{}, false
	}
	switch v := pair[1].(type) {
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return PromSample{}, false
		}
		if math.IsNaN(f) {
			return PromSample{}, false
		}
		return PromSample{Time: t, Value: f}, true
	}
	if f, ok := toFloat(pair[1]); ok {
		return PromSample{Time: t, Value: f}, true
	}
	return PromSample{}, false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
