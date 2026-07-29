package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
)

func (h *MonitoringHandler) PrometheusQuery(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	if summary, ok, err := h.realClusterSummary(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MetricsError, err.Error())
		return
	} else if ok {
		RespondJSON(w, http.StatusOK, summary)
		return
	}
	summary, err := h.clusterSummary(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MetricsError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *MonitoringHandler) PrometheusQueryRange(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	name := chi.URLParam(r, "name")
	namespace := chi.URLParam(r, "namespace")
	kind := chi.URLParam(r, "kind")
	if data, ok, err := h.realWorkloadMetrics(r.Context(), clusterID, kind, namespace, name, r.URL.Query().Get("range")); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MetricsError, err.Error())
		return
	} else if ok {
		RespondJSON(w, http.StatusOK, data)
		return
	}
	summary, err := h.workloadSummary(r.Context(), clusterID, kind, namespace, name)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MetricsError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, h.metricsSeries(summary, r.URL.Query().Get("range"), namespace+"/"+name))
}

func (h *MonitoringHandler) ListMetrics(w http.ResponseWriter, r *http.Request) {
	// Accept either {cluster_id} or {id} so this real-series handler can serve
	// the cluster-detail metrics route (which uses {id}) as well as the
	// top-level /clusters/{cluster_id}/metrics route, without a param mismatch.
	clusterID := chi.URLParam(r, "cluster_id")
	if clusterID == "" {
		clusterID = chi.URLParam(r, "id")
	}
	if r.URL.Path == "/api/v1/clusters/"+clusterID+"/metrics/summary/" {
		if summary, ok, err := h.realClusterSummary(r.Context(), clusterID); err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MetricsError, err.Error())
			return
		} else if ok {
			RespondJSON(w, http.StatusOK, summary)
			return
		}
	} else {
		if data, ok, err := h.realClusterMetrics(r.Context(), clusterID, r.URL.Query().Get("range")); err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MetricsError, err.Error())
			return
		} else if ok {
			RespondJSON(w, http.StatusOK, data)
			return
		}
	}
	summary, err := h.clusterSummary(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MetricsError, err.Error())
		return
	}

	if r.URL.Path == "/api/v1/clusters/"+clusterID+"/metrics/summary/" {
		RespondJSON(w, http.StatusOK, summary)
		return
	}

	RespondJSON(w, http.StatusOK, h.metricsSeries(summary, r.URL.Query().Get("range"), "cluster"))
}

func (h *MonitoringHandler) clusterSummary(ctx context.Context, clusterID string) (map[string]any, error) {
	wh := NewWorkloadHandlerWithRequester(h.requester)
	nodes, err := wh.getNodes(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	pods, err := wh.listPods(ctx, clusterID, "", "")
	if err != nil {
		return nil, err
	}
	cpuCapacity := 0
	memoryCapacity := 0
	podCapacity := 0
	for _, node := range nodes {
		cpuCapacity += node["cpuCapacity"].(int)
		memoryCapacity += node["memoryCapacity"].(int)
		podCapacity += node["podCapacity"].(int)
	}
	cpuUsage := 0.0
	memoryUsage := 0.0
	if h.queries != nil {
		if id, err := uuid.Parse(clusterID); err == nil {
			if health, err := h.queries.GetClusterHealthStatus(ctx, id); err == nil {
				cpuUsage = float64(cpuCapacity) * (health.CpuUsagePercent / 100.0)
				memoryUsage = float64(memoryCapacity) * (health.MemoryUsagePercent / 100.0)
			}
		}
	}
	cpuPct := 0.0
	if cpuCapacity > 0 {
		cpuPct = (cpuUsage / float64(cpuCapacity)) * 100
	}
	memoryPct := 0.0
	if memoryCapacity > 0 {
		memoryPct = (memoryUsage / float64(memoryCapacity)) * 100
	}
	return map[string]any{
		"cpuUsage":         cpuUsage,
		"cpuCapacity":      cpuCapacity,
		"cpuPercentage":    cpuPct,
		"memoryUsage":      memoryUsage,
		"memoryCapacity":   memoryCapacity,
		"memoryPercentage": memoryPct,
		"podCount":         len(pods),
		"podCapacity":      podCapacity,
		"nodeCount":        len(nodes),
		"networkReceive":   0,
		"networkTransmit":  0,
		"diskUsage":        0,
		"diskCapacity":     0,
	}, nil
}

func (h *MonitoringHandler) zeroMetrics() map[string]any {
	series := func(name, unit string) map[string]any {
		return map[string]any{"name": name, "unit": unit, "data": []map[string]any{}}
	}
	return map[string]any{
		"cpuUsage":        series("CPU Usage", "cores"),
		"cpuCapacity":     series("CPU Capacity", "cores"),
		"memoryUsage":     series("Memory Usage", "bytes"),
		"memoryCapacity":  series("Memory Capacity", "bytes"),
		"networkReceive":  series("Network Receive", "bytes"),
		"networkTransmit": series("Network Transmit", "bytes"),
		"diskUsage":       series("Disk Usage", "bytes"),
		"podCount":        series("Pod Count", "count"),
	}
}

func (h *MonitoringHandler) workloadSummary(ctx context.Context, clusterID, kind, namespace, name string) (map[string]any, error) {
	clusterSummary, err := h.clusterSummary(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	wh := NewWorkloadHandlerWithRequester(h.requester)
	resource, err := wh.fetchWorkloadResource(ctx, clusterID, kind, namespace, name)
	if err != nil {
		return nil, err
	}
	workloadPods, err := wh.listPods(ctx, clusterID, namespace, labelSelector(resource.Spec.Selector.MatchLabels))
	if err != nil {
		return nil, err
	}
	allPods, err := wh.listPods(ctx, clusterID, "", "")
	if err != nil {
		return nil, err
	}
	podShare := 0.02
	if len(allPods) > 0 {
		podShare = float64(len(workloadPods)) / float64(len(allPods))
	}
	if podShare > 1 {
		podShare = 1
	}
	summary := cloneMetricSummary(clusterSummary)
	summary["cpuUsage"] = summaryFloat(clusterSummary["cpuUsage"]) * podShare
	summary["memoryUsage"] = summaryFloat(clusterSummary["memoryUsage"]) * podShare
	summary["networkReceive"] = summaryFloat(clusterSummary["networkReceive"]) * podShare
	summary["networkTransmit"] = summaryFloat(clusterSummary["networkTransmit"]) * podShare
	summary["diskUsage"] = summaryFloat(clusterSummary["diskUsage"]) * podShare
	summary["podCount"] = len(workloadPods)
	summary["podCapacity"] = len(workloadPods)
	return summary, nil
}

// metricsSeries is the fallback returned when no Prometheus/Thanos time-series
// backend is configured for the cluster. It returns empty series (preserving
// the shape the frontend expects: name/label/unit/data) plus an explicit
// "available": false flag, rather than synthesizing a fabricated CPU/mem ramp
// that would be indistinguishable from real telemetry. The scalar summary is
// still surfaced separately via the /summary endpoint; only the invented
// time-series points are dropped here.
func (h *MonitoringHandler) metricsSeries(_ map[string]any, _ string, label string) map[string]any {
	series := func(name, unit string) map[string]any {
		return map[string]any{
			"name":  name,
			"label": label,
			"unit":  unit,
			"data":  []map[string]any{},
		}
	}
	return map[string]any{
		"available":       false,
		"cpuUsage":        series("CPU Usage", "cores"),
		"cpuCapacity":     series("CPU Capacity", "cores"),
		"memoryUsage":     series("Memory Usage", "bytes"),
		"memoryCapacity":  series("Memory Capacity", "bytes"),
		"networkReceive":  series("Network Receive", "bytes"),
		"networkTransmit": series("Network Transmit", "bytes"),
		"diskUsage":       series("Disk Usage", "bytes"),
		"podCount":        series("Pod Count", "count"),
	}
}

func metricWindow(rawRange string) (int, time.Duration) {
	switch rawRange {
	case "6h":
		return 18, 6 * time.Hour
	case "24h":
		return 24, 24 * time.Hour
	case "7d":
		return 28, 7 * 24 * time.Hour
	default:
		return 12, time.Hour
	}
}

func cloneMetricSummary(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func summaryFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func (h *MonitoringHandler) realClusterSummary(ctx context.Context, clusterID string) (map[string]any, bool, error) {
	client, cfg, ok, err := h.backendClient(ctx, clusterID)
	if err != nil || !ok {
		return nil, ok, err
	}
	selector := labelSelectorForConfig(cfg)
	// These scalar queries are mutually independent, so fan them out
	// concurrently rather than paying ~11 serial Thanos round-trips. Each
	// goroutine writes to its own destination variable, so no additional
	// synchronization is required; errgroup cancels the shared context and
	// surfaces the first error.
	var (
		cpuUsage, cpuCapacity            float64
		memoryUsage, memoryCapacity      float64
		podCount, podCapacity, nodeCount float64
		networkReceive, networkTransmit  float64
		diskCapacity, diskAvail          float64
	)
	g, gctx := errgroup.WithContext(ctx)
	scalar := func(query string, dst *float64) {
		g.Go(func() error {
			v, err := client.QueryScalar(gctx, query)
			if err != nil {
				return err
			}
			*dst = v
			return nil
		})
	}
	scalar(`sum(rate(node_cpu_seconds_total{mode!="idle",`+selector+`}[5m]))`, &cpuUsage)
	scalar(`sum(machine_cpu_cores{`+selector+`})`, &cpuCapacity)
	scalar(`sum(node_memory_MemTotal_bytes{`+selector+`} - node_memory_MemAvailable_bytes{`+selector+`})`, &memoryUsage)
	scalar(`sum(node_memory_MemTotal_bytes{`+selector+`})`, &memoryCapacity)
	scalar(`count(kube_pod_info{`+selector+`})`, &podCount)
	scalar(`sum(kube_node_status_capacity{resource="pods",unit="integer",`+selector+`})`, &podCapacity)
	scalar(`count(kube_node_info{`+selector+`})`, &nodeCount)
	scalar(`sum(rate(node_network_receive_bytes_total{device!~"lo|veth.*",`+selector+`}[5m]))`, &networkReceive)
	scalar(`sum(rate(node_network_transmit_bytes_total{device!~"lo|veth.*",`+selector+`}[5m]))`, &networkTransmit)
	scalar(`sum(node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay",`+selector+`})`, &diskCapacity)
	scalar(`sum(node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay",`+selector+`})`, &diskAvail)
	if err := g.Wait(); err != nil {
		return nil, true, err
	}
	return metricSummary(cpuUsage, cpuCapacity, memoryUsage, memoryCapacity, podCount, podCapacity, nodeCount, networkReceive, networkTransmit, diskCapacity-diskAvail, diskCapacity), true, nil
}

// realNodeSummary computes per-node CPU/memory/pod/network/disk gauges from the
// cluster's Prometheus/Thanos backend, mirroring realClusterSummary but scoping
// every query to a single node. node-exporter metrics are matched on the
// `instance` label (kube-prometheus-stack relabels node-exporter's instance to
// the Node name) and kube-state metrics on the `node` label. Returns ok=false
// when no backend is configured so the caller can fall back to capacity-only.
func (h *MonitoringHandler) realNodeSummary(ctx context.Context, clusterID, node string) (map[string]any, bool, error) {
	client, cfg, ok, err := h.backendClient(ctx, clusterID)
	if err != nil || !ok {
		return nil, ok, err
	}
	selector := labelSelectorForConfig(cfg)
	instance := `instance="` + escapePromLabel(node) + `",` + selector
	nodeLabel := `node="` + escapePromLabel(node) + `",` + selector
	var (
		cpuUsage, cpuCapacity           float64
		memoryUsage, memoryCapacity     float64
		podCount, podCapacity           float64
		networkReceive, networkTransmit float64
		diskCapacity, diskAvail         float64
	)
	g, gctx := errgroup.WithContext(ctx)
	scalar := func(query string, dst *float64) {
		g.Go(func() error {
			v, err := client.QueryScalar(gctx, query)
			if err != nil {
				return err
			}
			*dst = v
			return nil
		})
	}
	scalar(`sum(rate(node_cpu_seconds_total{mode!="idle",`+instance+`}[5m]))`, &cpuUsage)
	scalar(`count(node_cpu_seconds_total{mode="idle",`+instance+`})`, &cpuCapacity)
	scalar(`sum(node_memory_MemTotal_bytes{`+instance+`} - node_memory_MemAvailable_bytes{`+instance+`})`, &memoryUsage)
	scalar(`sum(node_memory_MemTotal_bytes{`+instance+`})`, &memoryCapacity)
	scalar(`count(kube_pod_info{`+nodeLabel+`})`, &podCount)
	scalar(`sum(kube_node_status_capacity{resource="pods",unit="integer",`+nodeLabel+`})`, &podCapacity)
	scalar(`sum(rate(node_network_receive_bytes_total{device!~"lo|veth.*",`+instance+`}[5m]))`, &networkReceive)
	scalar(`sum(rate(node_network_transmit_bytes_total{device!~"lo|veth.*",`+instance+`}[5m]))`, &networkTransmit)
	scalar(`sum(node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay",`+instance+`})`, &diskCapacity)
	scalar(`sum(node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay",`+instance+`})`, &diskAvail)
	if err := g.Wait(); err != nil {
		return nil, true, err
	}
	return metricSummary(cpuUsage, cpuCapacity, memoryUsage, memoryCapacity, podCount, podCapacity, 1, networkReceive, networkTransmit, diskCapacity-diskAvail, diskCapacity), true, nil
}

// nodeSummaryFallback builds a node gauge summary from the node's advertised
// capacity (fetched over the tunnel) when no Prometheus backend is configured.
// Usage figures come from the node-detail path (metrics-server) when available;
// otherwise they are zero — capacity-only is still far more useful than the
// previous 501.
func (h *MonitoringHandler) nodeSummaryFallback(ctx context.Context, clusterID, node string) (map[string]any, error) {
	wh := NewWorkloadHandlerWithRequester(h.requester)
	detail, err := wh.getNodeDetail(ctx, clusterID, node)
	if err != nil {
		return nil, err
	}
	asFloat := func(v any) float64 {
		switch n := v.(type) {
		case int:
			return float64(n)
		case int64:
			return float64(n)
		case float64:
			return n
		default:
			return 0
		}
	}
	return metricSummary(
		asFloat(detail["cpuUsage"]), asFloat(detail["cpuCapacity"]),
		asFloat(detail["memoryUsage"]), asFloat(detail["memoryCapacity"]),
		asFloat(detail["podCount"]), asFloat(detail["podCapacity"]),
		1, 0, 0, 0, 0,
	), nil
}

func (h *MonitoringHandler) realClusterMetrics(ctx context.Context, clusterID, rawRange string) (map[string]any, bool, error) {
	client, cfg, ok, err := h.backendClient(ctx, clusterID)
	if err != nil || !ok {
		return nil, ok, err
	}
	selector := labelSelectorForConfig(cfg)
	points, span := metricWindow(rawRange)
	step := span / time.Duration(points-1)
	start := time.Now().UTC().Add(-span)
	end := time.Now().UTC()
	series, err := h.promSeriesSet(ctx, client, start, end, step, selector, "cluster", map[string]string{
		"cpuUsage":        `sum(rate(node_cpu_seconds_total{mode!="idle",%s}[5m]))`,
		"cpuCapacity":     `sum(machine_cpu_cores{%s})`,
		"memoryUsage":     `sum(node_memory_MemTotal_bytes{%s} - node_memory_MemAvailable_bytes{%s})`,
		"memoryCapacity":  `sum(node_memory_MemTotal_bytes{%s})`,
		"networkReceive":  `sum(rate(node_network_receive_bytes_total{device!~"lo|veth.*",%s}[5m]))`,
		"networkTransmit": `sum(rate(node_network_transmit_bytes_total{device!~"lo|veth.*",%s}[5m]))`,
		"diskUsage":       `sum(node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay",%s} - node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay",%s})`,
		"podCount":        `count(kube_pod_info{%s})`,
	})
	if err != nil {
		return nil, true, err
	}
	return series, true, nil
}

func (h *MonitoringHandler) realWorkloadMetrics(ctx context.Context, clusterID, kind, namespace, name, rawRange string) (map[string]any, bool, error) {
	client, cfg, ok, err := h.backendClient(ctx, clusterID)
	if err != nil || !ok {
		return nil, ok, err
	}
	if h.requester == nil {
		return nil, false, nil
	}
	wh := NewWorkloadHandlerWithRequester(h.requester)
	resource, err := wh.fetchWorkloadResource(ctx, clusterID, kind, namespace, name)
	if err != nil {
		return nil, false, err
	}
	workloadPods, err := wh.listPods(ctx, clusterID, namespace, labelSelector(resource.Spec.Selector.MatchLabels))
	if err != nil {
		return nil, false, err
	}
	regex := podRegex(workloadPods)
	if regex == "" {
		return h.zeroMetrics(), true, nil
	}
	clusterSelector := labelSelectorForConfig(cfg)
	workloadSelector := `namespace="` + escapePromLabel(namespace) + `",pod=~"` + regex + `",` + clusterSelector
	points, span := metricWindow(rawRange)
	step := span / time.Duration(points-1)
	start := time.Now().UTC().Add(-span)
	end := time.Now().UTC()
	data := h.zeroMetrics()
	cpuUsage, err := client.QueryRange(ctx, fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{container!="",container!="POD",%s}[5m]))`, workloadSelector), start, end, step)
	if err != nil {
		return nil, true, err
	}
	cpuCapacity, err := client.QueryRange(ctx, fmt.Sprintf(`sum(kube_pod_container_resource_limits{resource="cpu",unit="core",%s})`, workloadSelector), start, end, step)
	if err != nil {
		return nil, true, err
	}
	memoryUsage, err := client.QueryRange(ctx, fmt.Sprintf(`sum(container_memory_working_set_bytes{container!="",container!="POD",%s})`, workloadSelector), start, end, step)
	if err != nil {
		return nil, true, err
	}
	memoryCapacity, err := client.QueryRange(ctx, fmt.Sprintf(`sum(kube_pod_container_resource_limits{resource="memory",unit="byte",%s})`, workloadSelector), start, end, step)
	if err != nil {
		return nil, true, err
	}
	data["cpuUsage"] = rangeSeries("CPU Usage", namespace+"/"+name, "cores", cpuUsage)
	data["cpuCapacity"] = rangeSeries("CPU Capacity", namespace+"/"+name, "cores", cpuCapacity)
	data["memoryUsage"] = rangeSeries("Memory Usage", namespace+"/"+name, "bytes", memoryUsage)
	data["memoryCapacity"] = rangeSeries("Memory Capacity", namespace+"/"+name, "bytes", memoryCapacity)
	data["podCount"] = rangeSeries("Pod Count", namespace+"/"+name, "count", constantPoints(end, span, points, float64(len(workloadPods))))
	return data, true, nil
}

func (h *MonitoringHandler) backendClient(ctx context.Context, clusterID string) (*imonitoring.Client, monitoringContext, bool, error) {
	if h.queries == nil {
		return nil, monitoringContext{}, false, nil
	}
	clusterUUID, err := uuid.Parse(clusterID)
	if err != nil {
		return nil, monitoringContext{}, false, err
	}
	if joined, err := h.queries.GetClusterMonitoringContext(ctx, clusterUUID); err == nil {
		client, err := imonitoring.NewClient(imonitoring.BackendConfig{
			QueryURL:            joined.QueryUrl,
			TenantID:            joined.TenantID,
			AuthType:            joined.AuthType,
			AuthConfig:          joined.AuthConfig,
			AuthConfigEncrypted: joined.AuthConfigEncrypted,
			Decryptor:           h.monitoringDecryptor(),
			Logger:              h.log,
			DefaultStepSeconds:  joined.DefaultStepSeconds,
			TimeoutSeconds:      joined.TimeoutSeconds,
		})
		if err != nil {
			return nil, monitoringContext{}, false, err
		}
		return client, monitoringContext{
			ClusterLabel:      joined.ClusterLabel,
			ClusterLabelValue: defaultString(joined.ClusterLabelValue, clusterID),
			DefaultStep:       joined.DefaultStepSeconds,
		}, true, nil
	} else if err != pgx.ErrNoRows {
		return nil, monitoringContext{}, false, err
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, monitoringContext{}, false, nil
		}
		return nil, monitoringContext{}, false, err
	}
	client, err := imonitoring.NewClient(imonitoring.BackendConfig{
		QueryURL:            backend.QueryUrl,
		TenantID:            backend.TenantID,
		AuthType:            backend.AuthType,
		AuthConfig:          backend.AuthConfig,
		AuthConfigEncrypted: backend.AuthConfigEncrypted,
		Decryptor:           h.monitoringDecryptor(),
		Logger:              h.log,
		DefaultStepSeconds:  backend.DefaultStepSeconds,
		TimeoutSeconds:      backend.TimeoutSeconds,
	})
	if err != nil {
		return nil, monitoringContext{}, false, err
	}
	return client, monitoringContext{ClusterLabel: "cluster_id", ClusterLabelValue: clusterID, DefaultStep: backend.DefaultStepSeconds}, true, nil
}

type monitoringContext struct {
	ClusterLabel      string
	ClusterLabelValue string
	DefaultStep       int32
}

func labelSelectorForConfig(cfg monitoringContext) string {
	return cfg.ClusterLabel + `="` + escapePromLabel(cfg.ClusterLabelValue) + `"`
}

func escapePromLabel(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return replacer.Replace(value)
}

func podRegex(items []map[string]any) string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		if name, ok := item["name"].(string); ok && name != "" {
			names = append(names, regexpEscape(name))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "^(" + strings.Join(names, "|") + ")$"
}

func regexpEscape(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `.`, `\.`, `+`, `\+`, `*`, `\*`, `?`, `\?`, `(`, `\(`, `)`, `\)`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`, `^`, `\^`, `$`, `\$`, `|`, `\|`, `-`, `\-`)
	return replacer.Replace(value)
}

func (h *MonitoringHandler) promSeriesSet(ctx context.Context, client *imonitoring.Client, start, end time.Time, step time.Duration, selector, label string, queries map[string]string) (map[string]any, error) {
	data := h.zeroMetrics()
	// The range queries are mutually independent, so fan them out concurrently
	// rather than issuing them serially. Each goroutine builds its own series
	// value and only takes the mutex to publish into the shared data map (Go
	// map writes are not concurrency-safe even for distinct keys).
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for key, queryFmt := range queries {
		g.Go(func() error {
			query := fmt.Sprintf(queryFmt, selector, selector)
			points, err := client.QueryRange(gctx, query, start, end, step)
			if err != nil {
				return err
			}
			var series map[string]any
			switch key {
			case "cpuUsage":
				series = rangeSeries("CPU Usage", label, "cores", points)
			case "cpuCapacity":
				series = rangeSeries("CPU Capacity", label, "cores", points)
			case "memoryUsage":
				series = rangeSeries("Memory Usage", label, "bytes", points)
			case "memoryCapacity":
				series = rangeSeries("Memory Capacity", label, "bytes", points)
			case "networkReceive":
				series = rangeSeries("Network Receive", label, "bytes", points)
			case "networkTransmit":
				series = rangeSeries("Network Transmit", label, "bytes", points)
			case "diskUsage":
				series = rangeSeries("Disk Usage", label, "bytes", points)
			case "podCount":
				series = rangeSeries("Pod Count", label, "count", points)
			default:
				return nil
			}
			mu.Lock()
			data[key] = series
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return data, nil
}

func metricSummary(cpuUsage, cpuCapacity, memoryUsage, memoryCapacity, podCount, podCapacity, nodeCount, networkReceive, networkTransmit, diskUsage, diskCapacity float64) map[string]any {
	cpuPct := 0.0
	if cpuCapacity > 0 {
		cpuPct = (cpuUsage / cpuCapacity) * 100
	}
	memoryPct := 0.0
	if memoryCapacity > 0 {
		memoryPct = (memoryUsage / memoryCapacity) * 100
	}
	return map[string]any{
		"cpuUsage":         cpuUsage,
		"cpuCapacity":      cpuCapacity,
		"cpuPercentage":    cpuPct,
		"memoryUsage":      memoryUsage,
		"memoryCapacity":   memoryCapacity,
		"memoryPercentage": memoryPct,
		"podCount":         int(podCount),
		"podCapacity":      int(podCapacity),
		"nodeCount":        int(nodeCount),
		"networkReceive":   networkReceive,
		"networkTransmit":  networkTransmit,
		"diskUsage":        diskUsage,
		"diskCapacity":     diskCapacity,
	}
}

func rangeSeries(name, label, unit string, points []imonitoring.TimeSeriesPoint) map[string]any {
	items := make([]map[string]any, 0, len(points))
	for _, point := range points {
		items = append(items, map[string]any{"timestamp": point.Timestamp, "value": point.Value})
	}
	return map[string]any{"name": name, "label": label, "unit": unit, "data": items}
}

func constantPoints(now time.Time, span time.Duration, count int, value float64) []imonitoring.TimeSeriesPoint {
	if count < 2 {
		count = 2
	}
	step := span / time.Duration(count-1)
	out := make([]imonitoring.TimeSeriesPoint, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, imonitoring.TimeSeriesPoint{
			Timestamp: now.Add(-span + step*time.Duration(i)).UTC().Format(time.RFC3339),
			Value:     value,
		})
	}
	return out
}

// --- Legacy path aliases ---
// Python registered the metrics ViewSet at /api/v1/monitoring/metrics/{action}/{cluster_id}/...
// These wrappers extract the path params and delegate to the existing
// cluster-scoped handlers.

// LegacyMetricsQuery proxies POST /api/v1/monitoring/metrics/query/{cluster_id}/.
func (h *MonitoringHandler) LegacyMetricsQuery(w http.ResponseWriter, r *http.Request) {
	h.PrometheusQuery(w, r)
}

// LegacyClusterOverview proxies GET /api/v1/monitoring/metrics/cluster-overview/{cluster_id}/.
func (h *MonitoringHandler) LegacyClusterOverview(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	if summary, ok, err := h.realClusterSummary(r.Context(), clusterID); err == nil && ok {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "success", "data": summary})
		return
	}
	summary, err := h.clusterSummary(r.Context(), clusterID)
	if err != nil {
		RespondJSON(w, http.StatusBadGateway, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"status": "success", "data": summary})
}

// LegacyWorkloadMetrics proxies GET /api/v1/monitoring/metrics/workload/{cluster_id}/{namespace}/{workload}/.
func (h *MonitoringHandler) LegacyWorkloadMetrics(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	namespace := chi.URLParam(r, "namespace")
	workload := chi.URLParam(r, "workload")
	if data, ok, err := h.realWorkloadMetrics(r.Context(), clusterID, "", namespace, workload, r.URL.Query().Get("range")); err == nil && ok {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "success", "data": data})
		return
	}
	summary, err := h.workloadSummary(r.Context(), clusterID, "", namespace, workload)
	if err != nil {
		RespondJSON(w, http.StatusBadGateway, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"status": "success", "data": h.metricsSeries(summary, r.URL.Query().Get("range"), namespace+"/"+workload)})
}

// LegacyNodeMetrics proxies GET /api/v1/monitoring/metrics/node/{cluster_id}/{node}/.
// Serves live per-node CPU/memory/pod/network/disk gauges from the cluster's
// Prometheus/Thanos backend when one is configured, falling back to the node's
// advertised capacity (plus metrics-server usage when wired) otherwise.
func (h *MonitoringHandler) LegacyNodeMetrics(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	node := chi.URLParam(r, "node")
	if summary, ok, err := h.realNodeSummary(r.Context(), clusterID, node); err == nil && ok {
		RespondJSON(w, http.StatusOK, map[string]any{"status": "success", "data": summary})
		return
	}
	summary, err := h.nodeSummaryFallback(r.Context(), clusterID, node)
	if err != nil {
		RespondJSON(w, http.StatusBadGateway, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"status": "success", "data": summary})
}
