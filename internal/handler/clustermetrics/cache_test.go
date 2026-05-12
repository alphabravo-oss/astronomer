package clustermetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

// makeNode is a helper that builds a corev1.Node with the given allocatable
// CPU (millicores) and memory (bytes). The Quantity strings exercise the
// suffix parser (`m` for milli, `Ki` for kibibytes) — mimicking the wire
// format the kubernetes API actually emits.
func makeNode(name, cpuQty, memQty string) corev1.Node {
	return corev1.Node{
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuQty),
				corev1.ResourceMemory: resource.MustParse(memQty),
			},
		},
	}
}

func makeNodeMetrics(name, cpuUse, memUse string) metricsv1beta1.NodeMetrics {
	return metricsv1beta1.NodeMetrics{
		Usage: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuUse),
			corev1.ResourceMemory: resource.MustParse(memUse),
		},
	}
}

func TestComputePercentages_BasicMath(t *testing.T) {
	// 2 nodes, each with 1 CPU and 1 GiB memory. Cluster capacity = 2 CPU,
	// 2 GiB. Usage = 1 CPU + 0.5 GiB. Expected: 50% CPU, 25% memory.
	nodes := []corev1.Node{
		makeNode("n1", "1", "1Gi"),
		makeNode("n2", "1", "1Gi"),
	}
	usage := []metricsv1beta1.NodeMetrics{
		makeNodeMetrics("n1", "500m", "256Mi"),
		makeNodeMetrics("n2", "500m", "256Mi"),
	}
	cpu, mem := computePercentages(nodes, usage)
	if got := round(cpu, 2); got != 50.00 {
		t.Errorf("cpu pct: want 50.00, got %v", got)
	}
	if got := round(mem, 2); got != 25.00 {
		t.Errorf("mem pct: want 25.00, got %v", got)
	}
}

func TestComputePercentages_NanocoreUsage(t *testing.T) {
	// metrics-server commonly emits CPU usage in nanocores ("123456n").
	// This test confirms the Quantity parser converts that into the same
	// millicore space we use for capacity, so the ratio is correct.
	nodes := []corev1.Node{makeNode("n1", "1", "1Gi")}
	// 100m = 100,000,000 nanocores
	usage := []metricsv1beta1.NodeMetrics{makeNodeMetrics("n1", "100000000n", "0")}
	cpu, _ := computePercentages(nodes, usage)
	if got := round(cpu, 2); got != 10.00 {
		t.Errorf("cpu pct: want 10.00 from 100m/1000m, got %v", got)
	}
}

func TestComputePercentages_ZeroCapacity(t *testing.T) {
	// No nodes -> no capacity -> zero percentages, no NaN, no panic.
	cpu, mem := computePercentages(nil, nil)
	if cpu != 0 || mem != 0 {
		t.Errorf("want 0/0 for empty input, got cpu=%v mem=%v", cpu, mem)
	}
}

func TestComputePercentages_MismatchedNodes(t *testing.T) {
	// metrics-server reports usage for a node we don't see in the node list
	// (e.g. it was just removed). Capacity is from the node list only;
	// usage is summed across whatever metrics-server returned. The
	// percentage is therefore "clamped" by the *known* capacity — in this
	// test, usage > capacity gives >100%, which is the documented
	// behaviour rather than a bug.
	nodes := []corev1.Node{makeNode("n1", "1", "1Gi")}
	usage := []metricsv1beta1.NodeMetrics{
		makeNodeMetrics("n1", "500m", "512Mi"),
		makeNodeMetrics("ghost", "500m", "512Mi"),
	}
	cpu, _ := computePercentages(nodes, usage)
	if got := round(cpu, 2); got != 100.00 {
		t.Errorf("cpu pct: want 100.00 (1000m used / 1000m cap), got %v", got)
	}
}

// fakeRequester is a stand-in for the tunnel K8sRequester used in remote-
// path tests. It dispatches by request path so a single fake serves the
// nodes / metrics / pods triple-call.
type fakeRequester struct {
	nodes      corev1.NodeList
	metrics    metricsv1beta1.NodeMetricsList
	pods       corev1.PodList
	metricsErr bool
	calls      int32
}

func (f *fakeRequester) Do(_ context.Context, _, _ , path string, _ []byte, _ map[string]string) (*RawResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	switch {
	case path == "/api/v1/nodes":
		body, _ := json.Marshal(f.nodes)
		return &RawResponse{StatusCode: http.StatusOK, Body: body}, nil
	case path == "/apis/metrics.k8s.io/v1beta1/nodes":
		if f.metricsErr {
			return &RawResponse{StatusCode: http.StatusNotFound, Body: []byte(`{}`)}, nil
		}
		body, _ := json.Marshal(f.metrics)
		return &RawResponse{StatusCode: http.StatusOK, Body: body}, nil
	default:
		body, _ := json.Marshal(f.pods)
		return &RawResponse{StatusCode: http.StatusOK, Body: body}, nil
	}
}

func TestProvider_RemoteHappyPath(t *testing.T) {
	f := &fakeRequester{
		nodes: corev1.NodeList{Items: []corev1.Node{
			makeNode("n1", "2", "2Gi"),
		}},
		metrics: metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{
			makeNodeMetrics("n1", "1", "1Gi"),
		}},
		pods: corev1.PodList{Items: []corev1.Pod{{}, {}, {}}},
	}
	p := NewProviderWithTTL(time.Hour)
	p.SetRemoteRequester(f)

	snap := p.Get(context.Background(), "cluster-1", false)
	if got := round(snap.CPUPercentage, 2); got != 50.00 {
		t.Errorf("cpu: want 50.00, got %v", got)
	}
	if got := round(snap.MemoryPercentage, 2); got != 50.00 {
		t.Errorf("mem: want 50.00, got %v", got)
	}
	if snap.PodCount != 3 {
		t.Errorf("pods: want 3, got %d", snap.PodCount)
	}
}

func TestProvider_CacheHitSuppressesRequests(t *testing.T) {
	f := &fakeRequester{
		nodes:   corev1.NodeList{Items: []corev1.Node{makeNode("n1", "1", "1Gi")}},
		metrics: metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{makeNodeMetrics("n1", "100m", "100Mi")}},
	}
	p := NewProviderWithTTL(time.Hour)
	p.SetRemoteRequester(f)

	for i := 0; i < 5; i++ {
		_ = p.Get(context.Background(), "cluster-cached", false)
	}
	// 4 calls per refresh = nodes + node-metrics + pod count + pod-metrics, then cache holds.
	if got := atomic.LoadInt32(&f.calls); got != 4 {
		t.Errorf("expected 4 underlying calls, got %d", got)
	}
}

// Peek is the cache-only accessor used by hot fan-out paths (the cluster
// List endpoint). Must NEVER trigger a transport call — that was the
// whole point of the cache-only contract. Two cases:
//   - cold miss: returns Snapshot{} without calling fakeRequester
//   - warm hit: returns the cached snapshot, also without calling
//     fakeRequester again
func TestProvider_PeekIsCacheOnly(t *testing.T) {
	f := &fakeRequester{
		nodes:   corev1.NodeList{Items: []corev1.Node{makeNode("n1", "1", "1Gi")}},
		metrics: metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{makeNodeMetrics("n1", "100m", "100Mi")}},
	}
	p := NewProviderWithTTL(time.Hour)
	p.SetRemoteRequester(f)

	// Cold miss: zero result, zero calls.
	snap := p.Peek("never-fetched")
	if snap.CPUPercentage != 0 || snap.PodCount != 0 {
		t.Errorf("cold peek should return zero snapshot, got cpu=%v pods=%d", snap.CPUPercentage, snap.PodCount)
	}
	if got := atomic.LoadInt32(&f.calls); got != 0 {
		t.Errorf("Peek on cold cache should make 0 requests, got %d", got)
	}

	// Prime the cache with a single Get.
	_ = p.Get(context.Background(), "cached", false)
	calls := atomic.LoadInt32(&f.calls)

	// Warm peek: cached snapshot returned, no new transport calls.
	for i := 0; i < 5; i++ {
		_ = p.Peek("cached")
	}
	if got := atomic.LoadInt32(&f.calls); got != calls {
		t.Errorf("Peek on warm cache should make 0 new requests, got %d (was %d before)", got, calls)
	}
}

// Peek on a nil receiver must return zero, not panic. The handler may
// construct a Provider only when wiring permits, and the list path
// reaches Peek before the nil-check on h.metrics — defense in depth.
func TestProvider_PeekNilSafe(t *testing.T) {
	var p *Provider
	snap := p.Peek("anything")
	if snap.CPUPercentage != 0 || snap.PodCount != 0 {
		t.Errorf("nil Peek should return zero snapshot, got cpu=%v pods=%d", snap.CPUPercentage, snap.PodCount)
	}
}

func TestProvider_CacheTTLExpires(t *testing.T) {
	f := &fakeRequester{
		nodes:   corev1.NodeList{Items: []corev1.Node{makeNode("n1", "1", "1Gi")}},
		metrics: metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{makeNodeMetrics("n1", "100m", "100Mi")}},
	}
	p := NewProviderWithTTL(1 * time.Millisecond)
	p.SetRemoteRequester(f)

	_ = p.Get(context.Background(), "cluster-ttl", false)
	time.Sleep(5 * time.Millisecond)
	_ = p.Get(context.Background(), "cluster-ttl", false)

	// Second pass refreshes => 8 underlying calls total (4 per refresh × 2).
	if got := atomic.LoadInt32(&f.calls); got != 8 {
		t.Errorf("expected 8 underlying calls after TTL expiry, got %d", got)
	}
}

func TestProvider_NoRequesterReturnsZeros(t *testing.T) {
	p := NewProvider()
	snap := p.Get(context.Background(), "no-deps", false)
	// Snapshot carries map fields (Nodes, Pods) now, so struct equality won't
	// compile. The "no transport wired" path returns zero scalars and nil
	// maps — which is the correct degraded state.
	if snap.CPUPercentage != 0 || snap.MemoryPercentage != 0 || snap.PodCount != 0 {
		t.Errorf("expected zero scalars, got %+v", snap)
	}
	if len(snap.Nodes) != 0 || len(snap.Pods) != 0 {
		t.Errorf("expected empty per-resource maps, got Nodes=%v Pods=%v", snap.Nodes, snap.Pods)
	}
}

func TestProvider_MetricsServerMissing(t *testing.T) {
	// metrics-server returning 404 must collapse to zeros, NOT a permanent
	// error that blocks the cluster card from rendering.
	f := &fakeRequester{
		nodes:      corev1.NodeList{Items: []corev1.Node{makeNode("n1", "1", "1Gi")}},
		pods:       corev1.PodList{Items: []corev1.Pod{{}}},
		metricsErr: true,
	}
	p := NewProviderWithTTL(time.Hour)
	p.SetRemoteRequester(f)

	snap := p.Get(context.Background(), "cluster-no-metrics", false)
	if snap.CPUPercentage != 0 || snap.MemoryPercentage != 0 {
		t.Errorf("expected 0 cpu/mem when metrics-server missing, got %+v", snap)
	}
	if snap.PodCount != 1 {
		t.Errorf("pod_count should still populate when metrics-server is missing, got %d", snap.PodCount)
	}
}

func TestProvider_Invalidate(t *testing.T) {
	f := &fakeRequester{
		nodes:   corev1.NodeList{Items: []corev1.Node{makeNode("n1", "1", "1Gi")}},
		metrics: metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{makeNodeMetrics("n1", "100m", "100Mi")}},
	}
	p := NewProviderWithTTL(time.Hour)
	p.SetRemoteRequester(f)

	_ = p.Get(context.Background(), "cluster-inv", false)
	p.Invalidate("cluster-inv")
	_ = p.Get(context.Background(), "cluster-inv", false)

	// 4 calls per refresh × 2 refreshes (initial + post-invalidate) = 8.
	if got := atomic.LoadInt32(&f.calls); got != 8 {
		t.Errorf("expected 8 underlying calls after invalidate, got %d", got)
	}
}

func round(v float64, places int) float64 {
	mul := 1.0
	for i := 0; i < places; i++ {
		mul *= 10
	}
	return float64(int64(v*mul+0.5)) / mul
}

// guard is a no-op compile-time check that fakeRequester satisfies
// K8sRequester. If the interface changes shape this stops compiling.
var _ K8sRequester = (*fakeRequester)(nil)

// keep fmt import live in case future tests need formatted output
var _ = fmt.Sprintf
