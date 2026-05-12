// Package clustermetrics provides a thin, in-process aggregator that turns
// raw kubernetes node / pod / metrics-server data into the three scalar
// fields the dashboard cluster card needs: CPU%, memory%, and pod count.
//
// It supports two transports per cluster:
//
//   - The local management cluster, where we already hold an in-process
//     *kubernetes.Clientset built from rest.InClusterConfig (the fast path).
//   - Remote agent-connected clusters, reached via the existing tunnel
//     K8sRequester (raw HTTP-over-WS to the cluster's API server).
//
// All errors collapse to (0,0,0,nil): the dashboard renders zeros instead of
// breaking when metrics-server is missing or the agent is offline. Real
// failures are swallowed at this layer because the cluster list endpoint
// would otherwise be a permanent 500 the moment one cluster's metrics
// pipeline hiccups.
//
// A 30-second TTL cache (sync.Map keyed by cluster ID) absorbs dashboard
// refresh spam — listing 10 clusters at 5s polling intervals would otherwise
// hammer each agent twice per second.
package clustermetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// DefaultTTL is the cache lifetime for a metrics snapshot. 30s matches the
// dashboard's typical refresh cadence: each card-render hits the cache,
// only the first one out of the gate within a window pays the agent round-
// trip cost.
const DefaultTTL = 30 * time.Second

// K8sRequester is the same minimal interface the rest of the handler package
// uses (see internal/handler/k8s_requester.go) — declared locally so this
// package doesn't import the parent handler package and create a cycle.
type K8sRequester interface {
	Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*RawResponse, error)
}

// RawResponse is the subset of protocol.K8sResponsePayload we consume. The
// body is base64-encoded by the tunnel transport; callers decode before
// passing it in.
type RawResponse struct {
	StatusCode int
	Body       []byte
}

// Snapshot is a single point-in-time read of a cluster's aggregate metrics.
//
// The Nodes / Pods maps carry the per-resource breakdown the node-detail and
// pod-list pages render. They share the underlying fetch with the aggregate —
// metrics-server already returns per-node and per-pod data in a single round
// trip, so populating them here costs nothing extra and lets the WorkloadHandler
// surface real CPU/memory usage instead of hardcoded zeros.
type Snapshot struct {
	CPUPercentage    float64
	MemoryPercentage float64
	PodCount         int
	// Nodes is keyed by node name; values may be partially populated (capacity
	// only) when metrics-server is missing.
	Nodes map[string]NodeMetrics
	// Pods is keyed by "<namespace>/<name>". Empty if metrics-server is not
	// installed in the cluster.
	Pods map[string]PodMetrics
}

// NodeMetrics is the per-node usage + capacity slice. All quantities are
// normalised — CPU in millicores, memory in bytes — to match what the
// frontend's formatCPU / formatBytes helpers expect.
type NodeMetrics struct {
	Name                  string
	CPUUsageMillicores    int64
	CPUCapacityMillicores int64
	MemoryUsageBytes      int64
	MemoryCapacityBytes   int64
}

// PodMetrics is the per-pod CPU/memory usage. Capacities aren't included
// because k8s pods don't have a flat capacity (the spec.containers[*].resources
// limits are per-container and may be unset); usage alone is what the UI
// pods table shows.
type PodMetrics struct {
	Namespace          string
	Name               string
	CPUUsageMillicores int64
	MemoryUsageBytes   int64
}

type cacheEntry struct {
	snap     Snapshot
	cachedAt time.Time
}

// Provider produces (and caches) Snapshots. A zero-value Provider is usable —
// it will return Snapshot{} for every cluster, which is the documented
// fallback for "no data plane available". Callers wire dependencies with
// SetLocalClient / SetRemoteRequester after construction so the same
// Provider can serve a mix of local + remote clusters.
type Provider struct {
	mu sync.Mutex

	// localClient is non-nil when the server is running inside a kubernetes
	// cluster and we have direct API access. Used only for the cluster row
	// flagged is_local=true.
	localClient    *kubernetes.Clientset
	localMetricsCS metricsv.Interface

	// remote is the tunnel-backed proxy used for any non-local cluster. When
	// nil, non-local clusters always return Snapshot{}.
	remote K8sRequester

	// ttl is the cache lifetime; tests override it via NewProviderWithTTL.
	ttl time.Duration

	// cache is keyed by cluster ID (string form). A sync.Map would also work
	// but a plain map under mu keeps the read+upsert atomic without juggling
	// LoadOrStore-vs-stale-eviction semantics.
	cache map[string]cacheEntry
}

// NewProvider returns a Provider with the default TTL. Dependencies are set
// after construction; until they are, every cluster collapses to zeros.
func NewProvider() *Provider {
	return NewProviderWithTTL(DefaultTTL)
}

// NewProviderWithTTL returns a Provider with a caller-specified cache TTL.
// Used in tests to force expiry without sleeping.
func NewProviderWithTTL(ttl time.Duration) *Provider {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Provider{
		ttl:   ttl,
		cache: make(map[string]cacheEntry),
	}
}

// SetLocalClient wires the in-process kubernetes clientset used for the
// local cluster fast path. metricsClient may be nil when metrics-server
// isn't installed; in that case CPU/memory percentages stay at zero.
func (p *Provider) SetLocalClient(cs *kubernetes.Clientset, metricsClient metricsv.Interface) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.localClient = cs
	p.localMetricsCS = metricsClient
}

// SetRemoteRequester wires the tunnel K8sRequester used for non-local
// clusters.
func (p *Provider) SetRemoteRequester(r K8sRequester) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.remote = r
}

// Invalidate drops the cached snapshot for clusterID. Useful when a caller
// knows something has changed (e.g. the agent just reconnected) and doesn't
// want to wait for the natural TTL.
func (p *Provider) Invalidate(clusterID string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cache, clusterID)
}

// GetNode returns the per-node usage / capacity for a single node within a
// cluster. Nil-safe; missing data collapses to a zero NodeMetrics — the node
// detail page renders zero gauges rather than 500ing if metrics-server is
// missing or the agent is offline.
func (p *Provider) GetNode(ctx context.Context, clusterID, nodeName string, isLocal bool) NodeMetrics {
	if p == nil {
		return NodeMetrics{Name: nodeName}
	}
	snap := p.Get(ctx, clusterID, isLocal)
	if nm, ok := snap.Nodes[nodeName]; ok {
		return nm
	}
	return NodeMetrics{Name: nodeName}
}

// GetPod returns the per-pod usage for a single pod within a cluster. Nil-safe.
func (p *Provider) GetPod(ctx context.Context, clusterID, namespace, name string, isLocal bool) PodMetrics {
	if p == nil {
		return PodMetrics{Namespace: namespace, Name: name}
	}
	snap := p.Get(ctx, clusterID, isLocal)
	if pm, ok := snap.Pods[namespace+"/"+name]; ok {
		return pm
	}
	return PodMetrics{Namespace: namespace, Name: name}
}

// PodsByNode returns per-pod usage for pods scheduled on a specific node.
// Useful for the node-detail pods table. Walks the cluster-wide pod map; the
// caller is expected to filter by the node it owns.
func (p *Provider) PodsByNode(ctx context.Context, clusterID, nodeName string, podNames []string, isLocal bool) []PodMetrics {
	if p == nil || len(podNames) == 0 {
		return nil
	}
	snap := p.Get(ctx, clusterID, isLocal)
	out := make([]PodMetrics, 0, len(podNames))
	for _, key := range podNames {
		if pm, ok := snap.Pods[key]; ok {
			out = append(out, pm)
		}
	}
	return out
}

// Peek returns the cached snapshot (regardless of TTL) without ever making
// a synchronous transport call. Stale or missing entries collapse to
// Snapshot{}. Use this on hot fan-out paths (e.g. ClusterHandler.List
// iterating every cluster row) where blocking the response on a single
// slow agent would be a much worse failure mode than showing stale or
// zero metrics. The background publisher keeps the cache warm.
//
// Use Get instead when the caller really does need a fresh snapshot
// and is willing to wait — single-cluster detail views, the metrics
// endpoint, etc.
func (p *Provider) Peek(clusterID string) Snapshot {
	if p == nil {
		return Snapshot{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.cache[clusterID]; ok {
		return entry.snap
	}
	return Snapshot{}
}

// Get returns the cached snapshot if fresh, otherwise refreshes synchronously.
// isLocal selects between the in-process fast path and the tunnel transport.
//
// Errors from the underlying transport are intentionally swallowed: a missing
// metrics-server, a disconnected agent, or a transient timeout all collapse
// to Snapshot{}. The dashboard prefers "0%" over a broken card.
func (p *Provider) Get(ctx context.Context, clusterID string, isLocal bool) Snapshot {
	if p == nil {
		return Snapshot{}
	}

	// Cache hit fast path.
	p.mu.Lock()
	if entry, ok := p.cache[clusterID]; ok && time.Since(entry.cachedAt) < p.ttl {
		p.mu.Unlock()
		return entry.snap
	}
	p.mu.Unlock()

	// Snapshot the current dependency set under the lock so we don't race
	// with SetLocalClient / SetRemoteRequester. The actual network calls
	// happen unlocked.
	p.mu.Lock()
	localCS := p.localClient
	localMC := p.localMetricsCS
	remote := p.remote
	p.mu.Unlock()

	var snap Snapshot
	if isLocal && localCS != nil {
		snap = collectLocal(ctx, localCS, localMC)
	} else if !isLocal && remote != nil {
		snap = collectRemote(ctx, remote, clusterID)
	}
	// snap is already zero on the no-transport branch, no else needed.

	p.mu.Lock()
	p.cache[clusterID] = cacheEntry{snap: snap, cachedAt: time.Now()}
	p.mu.Unlock()

	return snap
}

// collectLocal reads node capacity + metrics-server usage + pod count using
// the in-process clientset. metricsClient may be nil; in that case CPU/memory
// stay at zero but pod_count is still populated.
func collectLocal(ctx context.Context, cs *kubernetes.Clientset, mc metricsv.Interface) Snapshot {
	snap := Snapshot{
		Nodes: map[string]NodeMetrics{},
		Pods:  map[string]PodMetrics{},
	}

	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return snap
	}
	// Seed per-node capacities from the nodes list; usage is layered on once
	// metrics-server data arrives.
	for _, n := range nodes.Items {
		snap.Nodes[n.Name] = NodeMetrics{
			Name:                  n.Name,
			CPUCapacityMillicores: n.Status.Allocatable.Cpu().MilliValue(),
			MemoryCapacityBytes:   n.Status.Allocatable.Memory().Value(),
		}
	}

	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{Limit: 500})
	if err == nil {
		snap.PodCount = len(pods.Items)
	}

	if mc == nil {
		return snap
	}
	nodeMetrics, err := mc.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err == nil {
		snap.CPUPercentage, snap.MemoryPercentage = computePercentages(nodes.Items, nodeMetrics.Items)
		layerNodeUsage(snap.Nodes, nodeMetrics.Items)
	}
	if podMetrics, err := mc.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{}); err == nil {
		layerPodUsage(snap.Pods, podMetrics.Items)
	}
	return snap
}

// collectRemote issues the same three logical reads (nodes, node-metrics,
// pod-count) over the tunnel. All requests are GETs against the standard
// kubernetes API surface — there is no agent-side helper required.
//
// The metrics-server endpoint may legitimately 404 (metrics-server not
// installed); we treat that as zeros, not an error.
func collectRemote(ctx context.Context, req K8sRequester, clusterID string) Snapshot {
	snap := Snapshot{
		Nodes: map[string]NodeMetrics{},
		Pods:  map[string]PodMetrics{},
	}

	headers := map[string]string{"Accept": "application/json"}

	// Nodes (capacity).
	nodes, err := fetchNodes(ctx, req, clusterID, headers)
	if err != nil {
		return snap
	}
	for _, n := range nodes {
		snap.Nodes[n.Name] = NodeMetrics{
			Name:                  n.Name,
			CPUCapacityMillicores: n.Status.Allocatable.Cpu().MilliValue(),
			MemoryCapacityBytes:   n.Status.Allocatable.Memory().Value(),
		}
	}

	// Pod count. Failure here is non-fatal — a partial snapshot with
	// CPU/memory but pod_count=0 is still better than nothing.
	if pc, err := fetchPodCount(ctx, req, clusterID, headers); err == nil {
		snap.PodCount = pc
	}

	// Node metrics (usage). When metrics-server is missing we just stop
	// here; capacity-only data is meaningless without usage.
	nodeMetrics, err := fetchNodeMetrics(ctx, req, clusterID, headers)
	if err == nil {
		snap.CPUPercentage, snap.MemoryPercentage = computePercentages(nodes, nodeMetrics)
		layerNodeUsage(snap.Nodes, nodeMetrics)
	}
	// Pod-level usage is best-effort; a 404 here is a normal "metrics-server
	// not installed" signal and shouldn't block the rest of the snapshot.
	if podMetrics, err := fetchPodMetrics(ctx, req, clusterID, headers); err == nil {
		layerPodUsage(snap.Pods, podMetrics)
	}
	return snap
}

// layerNodeUsage merges metrics-server usage into the per-node capacity map
// produced by the nodes list. Nodes not present in the metrics list keep
// their zero usage values — useful when metrics-server lags reality.
func layerNodeUsage(nodes map[string]NodeMetrics, items []metricsv1beta1.NodeMetrics) {
	for _, nm := range items {
		entry := nodes[nm.Name]
		entry.Name = nm.Name
		entry.CPUUsageMillicores = nm.Usage.Cpu().MilliValue()
		entry.MemoryUsageBytes = nm.Usage.Memory().Value()
		nodes[nm.Name] = entry
	}
}

// layerPodUsage sums per-container pod metrics into a single per-pod entry.
// Pods are keyed by "<namespace>/<name>" — matching the format the workload
// handler emits.
func layerPodUsage(pods map[string]PodMetrics, items []metricsv1beta1.PodMetrics) {
	for _, pm := range items {
		var cpu, mem int64
		for _, c := range pm.Containers {
			cpu += c.Usage.Cpu().MilliValue()
			mem += c.Usage.Memory().Value()
		}
		pods[pm.Namespace+"/"+pm.Name] = PodMetrics{
			Namespace:          pm.Namespace,
			Name:               pm.Name,
			CPUUsageMillicores: cpu,
			MemoryUsageBytes:   mem,
		}
	}
}

func fetchPodMetrics(ctx context.Context, req K8sRequester, clusterID string, headers map[string]string) ([]metricsv1beta1.PodMetrics, error) {
	resp, err := req.Do(ctx, clusterID, http.MethodGet, "/apis/metrics.k8s.io/v1beta1/pods", nil, headers)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("pod metrics list failed")
	}
	var list metricsv1beta1.PodMetricsList
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func fetchNodes(ctx context.Context, req K8sRequester, clusterID string, headers map[string]string) ([]corev1.Node, error) {
	resp, err := req.Do(ctx, clusterID, http.MethodGet, "/api/v1/nodes", nil, headers)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("nodes list failed")
	}
	var list corev1.NodeList
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func fetchNodeMetrics(ctx context.Context, req K8sRequester, clusterID string, headers map[string]string) ([]metricsv1beta1.NodeMetrics, error) {
	resp, err := req.Do(ctx, clusterID, http.MethodGet, "/apis/metrics.k8s.io/v1beta1/nodes", nil, headers)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("node metrics list failed")
	}
	var list metricsv1beta1.NodeMetricsList
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// fetchPodCount uses the standard core/v1 pods list with a small page size.
// We filter Failed/Succeeded so the count reflects "running" workloads, which
// is what the dashboard renders. The list metadata's Continue token is
// ignored — the dashboard only needs an at-a-glance count, and capping at
// 500 keeps the wire payload bounded for very large clusters.
func fetchPodCount(ctx context.Context, req K8sRequester, clusterID string, headers map[string]string) (int, error) {
	path := "/api/v1/pods?fieldSelector=status.phase!=Failed,status.phase!=Succeeded&limit=500"
	resp, err := req.Do(ctx, clusterID, http.MethodGet, path, nil, headers)
	if err != nil {
		return 0, err
	}
	if resp == nil || resp.StatusCode >= 400 {
		return 0, fmt.Errorf("pod list failed")
	}
	var list corev1.PodList
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return 0, err
	}
	return len(list.Items), nil
}

// computePercentages turns raw node + node-metrics lists into cluster-wide
// CPU / memory percentages. CPU is summed in millicores (1 core = 1000m) and
// memory in bytes — both come from corev1.ResourceList.Quantity, which the
// k8s API returns as strings like "123456n" (nanocores) and "1024Ki"
// (kibibytes). The Quantity decoder handles all SI/binary suffixes for us;
// MilliValue() and Value() are the canonical normalised accessors.
//
// Returns 0 for either dimension if its capacity is zero (e.g. nodes list
// raced ahead of metrics list and the cluster names don't match yet).
func computePercentages(nodes []corev1.Node, nodeMetrics []metricsv1beta1.NodeMetrics) (cpuPct, memPct float64) {
	var totalCPUCap, totalMemCap int64
	for _, n := range nodes {
		totalCPUCap += n.Status.Allocatable.Cpu().MilliValue()
		totalMemCap += n.Status.Allocatable.Memory().Value()
	}

	var totalCPUUse, totalMemUse int64
	for _, nm := range nodeMetrics {
		totalCPUUse += nm.Usage.Cpu().MilliValue()
		totalMemUse += nm.Usage.Memory().Value()
	}

	if totalCPUCap > 0 {
		cpuPct = float64(totalCPUUse) / float64(totalCPUCap) * 100.0
	}
	if totalMemCap > 0 {
		memPct = float64(totalMemUse) / float64(totalMemCap) * 100.0
	}
	return cpuPct, memPct
}
