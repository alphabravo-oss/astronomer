package agent

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// alwaysSynced is the HasSynced predicate for a hand-seeded cache.Store, which
// has no informer behind it to report readiness.
func alwaysSynced() bool { return true }

// neverSynced models an informer that never completed its initial list — the
// RBAC-denied case the inventory readers must refuse to answer from.
func neverSynced() bool { return false }

// seededSubscriber returns a StateSubscriber whose Node and Pod stores are
// pre-populated, standing in for synced informers. It is the real production
// type so the test exercises recordStore/syncedStore, not a hand-written stub.
func seededSubscriber(t *testing.T, nodes, pods int, synced func() bool) *StateSubscriber {
	t.Helper()
	s := NewStateSubscriber(nil, nil, slog.New(slog.DiscardHandler))

	nodeStore := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for i := 0; i < nodes; i++ {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   fmt.Sprintf("node-%d", i),
				Labels: map[string]string{"eks.amazonaws.com/nodegroup": "ng-1"},
			},
		}
		node.Status.Allocatable = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("4"),
			corev1.ResourceMemory: resource.MustParse("16Gi"),
		}
		if err := nodeStore.Add(node); err != nil {
			t.Fatalf("seed node store: %v", err)
		}
	}
	s.recordStore("Node", "", "v1", nodeStore, synced)

	podStore := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for i := 0; i < pods; i++ {
		if err := podStore.Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("pod-%d", i),
			Namespace: fmt.Sprintf("ns-%d", i%7),
		}}); err != nil {
			t.Fatalf("seed pod store: %v", err)
		}
	}
	s.recordStore("Pod", "", "v1", podStore, synced)

	return s
}

// clusterWideLists returns the cluster-wide list actions the fake clientset
// recorded for the given resource. Asserting on ACTIONS is the only way to
// catch a regression here: a re-introduced List still produces a correct
// payload, it just quietly costs the apiserver an etcd quorum read every 30s.
func clusterWideLists(t *testing.T, cs *fake.Clientset, resource string) []k8stesting.ListActionImpl {
	t.Helper()
	var out []k8stesting.ListActionImpl
	for _, a := range cs.Actions() {
		list, ok := a.(k8stesting.ListActionImpl)
		if !ok || list.GetVerb() != "list" || list.GetResource().Resource != resource {
			continue
		}
		out = append(out, list)
	}
	return out
}

func seedClusterObjects(nodes, pods int) []k8sruntime.Object {
	objs := make([]k8sruntime.Object, 0, nodes+pods)
	for i := 0; i < nodes; i++ {
		objs = append(objs, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("node-%d", i)}})
	}
	for i := 0; i < pods; i++ {
		objs = append(objs, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("pod-%d", i),
			Namespace: fmt.Sprintf("ns-%d", i%7),
		}})
	}
	return objs
}

// TestHeartbeatUsesInformerCountsAndDoesNotListPods is the core regression
// guard: with the informer-backed inventory wired, a heartbeat and a metrics
// collection must issue ZERO cluster-wide Node/Pod LISTs.
//
// Pre-fix this fails immediately — collectHeartbeat listed nodes and pods
// unconditionally (health.go:314, :323), collectMetrics listed nodes a third
// time (:377) and collectMetricsPayload listed both again (:148, :154), all
// with a bare metav1.ListOptions{}.
func TestHeartbeatUsesInformerCountsAndDoesNotListPods(t *testing.T) {
	cs := fake.NewClientset()
	hr := NewHealthReporter(cs, slog.New(slog.DiscardHandler), 30, 60)
	hr.SetInventorySource(seededSubscriber(t, 3, 21, alwaysSynced))

	hb, err := hr.collectHeartbeat(context.Background())
	if err != nil {
		t.Fatalf("collectHeartbeat: %v", err)
	}
	if hb.NodeCount != 3 {
		t.Errorf("NodeCount = %d, want 3 (from the informer cache)", hb.NodeCount)
	}
	if hb.PodCount != 21 {
		t.Errorf("PodCount = %d, want 21 (from the informer cache)", hb.PodCount)
	}
	if hb.Distribution != "eks" {
		t.Errorf("Distribution = %q, want %q — distribution must still be derived from the cached node labels", hb.Distribution, "eks")
	}
	for _, reason := range hb.DegradedReasons {
		if strings.HasPrefix(reason, "list_") {
			t.Errorf("unexpected inventory degradation: %q", reason)
		}
	}

	m, err := hr.collectMetricsPayload(context.Background())
	if err != nil {
		t.Fatalf("collectMetricsPayload: %v", err)
	}
	if m.ClusterNodeCount != 3 || m.ClusterPodCount != 21 {
		t.Errorf("metrics counts = %d nodes / %d pods, want 3 / 21", m.ClusterNodeCount, m.ClusterPodCount)
	}
	if len(m.Namespaces) != 7 {
		t.Errorf("per-namespace aggregation produced %d namespaces, want 7", len(m.Namespaces))
	}

	for _, res := range []string{"pods", "nodes"} {
		if lists := clusterWideLists(t, cs, res); len(lists) != 0 {
			t.Errorf("heartbeat path issued %d cluster-wide %s LIST(s) with an informer source wired; want 0 (opts=%+v)", len(lists), res, lists[0].ListOptions)
		}
	}
}

// TestHeartbeatFallbackListsArePagedAndFromWatchCache covers the path most
// likely to be left unpaginated: the fallback taken when no inventory source is
// wired at all, and the one taken when a source IS wired but its informer never
// synced (RBAC-denied pods/nodes). Both must page and both must ask for
// ResourceVersion "0" so the apiserver answers from its watch cache instead of
// taking an etcd quorum read.
func TestHeartbeatFallbackListsArePagedAndFromWatchCache(t *testing.T) {
	cases := []struct {
		name string
		wire func(hr *HealthReporter, t *testing.T)
	}{
		{"no inventory source", func(*HealthReporter, *testing.T) {}},
		{"source wired but never synced", func(hr *HealthReporter, t *testing.T) {
			hr.SetInventorySource(seededSubscriber(t, 3, 21, neverSynced))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := fake.NewClientset(seedClusterObjects(2, 9)...)
			hr := NewHealthReporter(cs, slog.New(slog.DiscardHandler), 30, 60)
			tc.wire(hr, t)

			hb, err := hr.collectHeartbeat(context.Background())
			if err != nil {
				t.Fatalf("collectHeartbeat: %v", err)
			}
			// The fallback must produce the same payload as the informer path,
			// never a fabricated zero from an unusable cache.
			if hb.NodeCount != 2 || hb.PodCount != 9 {
				t.Errorf("fallback counts = %d nodes / %d pods, want 2 / 9 (degraded=%v)", hb.NodeCount, hb.PodCount, hb.DegradedReasons)
			}

			if _, err := hr.collectMetricsPayload(context.Background()); err != nil {
				t.Fatalf("collectMetricsPayload: %v", err)
			}

			for _, res := range []string{"pods", "nodes"} {
				lists := clusterWideLists(t, cs, res)
				if len(lists) == 0 {
					t.Fatalf("expected the fallback to LIST %s", res)
				}
				for i, l := range lists {
					// A continuation page carries no ResourceVersion (the token
					// encodes it and the apiserver rejects both together), so
					// only the first page of each list is checked for RV.
					if l.ListOptions.Continue == "" && l.ListOptions.ResourceVersion != "0" {
						t.Errorf("%s list #%d ResourceVersion = %q, want \"0\" (etcd quorum read otherwise)", res, i, l.ListOptions.ResourceVersion)
					}
					if l.ListOptions.Limit <= 0 {
						t.Errorf("%s list #%d has no Limit: an unpaginated cluster-wide LIST", res, i)
					}
				}
			}
		})
	}
}

// TestCollectorsSkipWhenDisconnected asserts a disconnected agent does no
// collection work at all: over dozens of ticks it must not touch the apiserver.
// The second half proves the gate is a gate and not a permanent off switch.
func TestCollectorsSkipWhenDisconnected(t *testing.T) {
	cs := fake.NewClientset(seedClusterObjects(2, 9)...)
	hr := NewHealthReporter(cs, slog.New(slog.DiscardHandler), 30, 60)
	// In-package override: production intervals would make this test a
	// minute long.
	hr.heartbeatInterval = 2 * time.Millisecond
	hr.metricsInterval = 2 * time.Millisecond

	var sent atomic.Int64
	sendFn := func(*protocol.Message) error {
		sent.Add(1)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		hr.Start(ctx, sendFn)
	}()

	// ~50 ticks with the tunnel down.
	time.Sleep(100 * time.Millisecond)
	if n := len(cs.Actions()); n != 0 {
		t.Errorf("disconnected agent made %d apiserver calls over ~50 ticks, want 0", n)
	}
	if n := sent.Load(); n != 0 {
		t.Errorf("disconnected agent emitted %d frames, want 0", n)
	}

	hr.SetConnected(true)
	deadline := time.Now().Add(2 * time.Second)
	for sent.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if sent.Load() == 0 {
		t.Fatal("collection never resumed after the tunnel reconnected")
	}

	cancel()
	<-done
}

// TestHeartbeatWorkIsBoundedAtScale is the scale-shaped case: 10k pods and 500
// nodes in the informer caches. Per beat the agent must make no apiserver call
// and allocate a small, roughly constant amount — versus the pre-fix path,
// which deserialized every Pod and Node object in the cluster twice a minute
// (hundreds of MB against a 512Mi container limit).
func TestHeartbeatWorkIsBoundedAtScale(t *testing.T) {
	const (
		pods  = 10000
		nodes = 500
		beats = 5
		// Generous ceiling: the informer path allocates one key slice per beat
		// (~160 KiB at 10k pods) plus the payload. Anything that re-introduces
		// object copying at cluster scale blows past this by orders of
		// magnitude.
		maxBytesPerBeat = 4 << 20
	)

	cs := fake.NewClientset()
	hr := NewHealthReporter(cs, slog.New(slog.DiscardHandler), 30, 60)
	hr.SetInventorySource(seededSubscriber(t, nodes, pods, alwaysSynced))

	// Warm up so first-call one-off allocations aren't charged to the budget.
	if _, err := hr.collectHeartbeat(context.Background()); err != nil {
		t.Fatalf("collectHeartbeat: %v", err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < beats; i++ {
		hb, err := hr.collectHeartbeat(context.Background())
		if err != nil {
			t.Fatalf("collectHeartbeat: %v", err)
		}
		if hb.PodCount != pods || hb.NodeCount != nodes {
			t.Fatalf("counts = %d pods / %d nodes, want %d / %d", hb.PodCount, hb.NodeCount, pods, nodes)
		}
	}
	runtime.ReadMemStats(&after)

	perBeat := (after.TotalAlloc - before.TotalAlloc) / beats
	t.Logf("heartbeat allocated %d bytes per beat at %d pods / %d nodes", perBeat, pods, nodes)
	if perBeat > maxBytesPerBeat {
		t.Errorf("heartbeat allocated %d bytes per beat at %d pods, want <= %d", perBeat, pods, maxBytesPerBeat)
	}
	// Discovery (ServerVersion/ServerGroups) still runs per beat; it is not an
	// etcd read and not in scope here. What must be zero at any scale is the
	// cluster-wide inventory LIST.
	for _, res := range []string{"pods", "nodes"} {
		if lists := clusterWideLists(t, cs, res); len(lists) != 0 {
			t.Errorf("heartbeat issued %d cluster-wide %s LIST(s) at scale, want 0", len(lists), res)
		}
	}
}
