//go:build integration

package wire_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/alphabravocompany/astronomer-go/internal/operator/lifecycle"
	"github.com/alphabravocompany/astronomer-go/internal/operator/wire"
)

const testNamespace = "astronomer-mgmt"

// callTag marks a reconcile invoked directly by the test (as opposed to one
// the manager's workers started) so the observer can attribute intervals.
type callTag struct{}

type interval struct {
	key, tag   string
	start, end time.Time
}

func (i interval) overlaps(o interval) bool { return i.start.Before(o.end) && o.start.Before(i.end) }

// syncObserver instruments the cluster pipeline's sync stage: it records
// every execution interval and the peak number of concurrent executions,
// per object key and across distinct keys.
type syncObserver struct {
	mu          sync.Mutex
	open        map[string][]time.Time
	active      map[string]int
	intervals   []interval
	maxSameKey  int
	maxDistinct int
}

func newSyncObserver() *syncObserver {
	return &syncObserver{open: map[string][]time.Time{}, active: map[string]int{}}
}

func tagOf(ctx context.Context) string {
	if tag, ok := ctx.Value(callTag{}).(string); ok {
		return tag
	}
	return "manager"
}

func (o *syncObserver) StageStarted(ctx context.Context, controller, stage, key string) {
	if controller != "cluster" || stage != lifecycle.StageSync {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.open[key+"|"+tagOf(ctx)] = append(o.open[key+"|"+tagOf(ctx)], time.Now())
	o.active[key]++
	o.maxSameKey = max(o.maxSameKey, o.active[key])
	o.maxDistinct = max(o.maxDistinct, len(o.active))
}

func (o *syncObserver) StageFinished(ctx context.Context, controller, stage, key string, _ lifecycle.Outcome, _ time.Duration) {
	if controller != "cluster" || stage != lifecycle.StageSync {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	id := key + "|" + tagOf(ctx)
	starts := o.open[id]
	o.intervals = append(o.intervals, interval{key: key, tag: tagOf(ctx), start: starts[0], end: time.Now()})
	o.open[id] = starts[1:]
	o.active[key]--
	if o.active[key] == 0 {
		delete(o.active, key)
	}
}

func (o *syncObserver) snapshot() (intervals []interval, maxSameKey, maxDistinct int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]interval(nil), o.intervals...), o.maxSameKey, o.maxDistinct
}

// slowClusterSync widens every sync stage so overlaps are observable.
type slowClusterSync struct {
	managerDelay atomic.Int64 // nanoseconds for manager-initiated reconciles
	directDelay  time.Duration
}

func (s *slowClusterSync) EnsureFromCRD(ctx context.Context, spec lifecycle.ClusterSpec) (lifecycle.ClusterStatus, error) {
	delay := time.Duration(s.managerDelay.Load())
	if tagOf(ctx) != "manager" {
		delay = s.directDelay
	}
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return lifecycle.ClusterStatus{}, ctx.Err()
	}
	return lifecycle.ClusterStatus{ClusterID: "id-" + spec.Name, Phase: "active"}, nil
}

func (s *slowClusterSync) DeleteByName(context.Context, string) error { return nil }

type nopProjectSync struct{}

func (nopProjectSync) EnsureFromCRD(context.Context, lifecycle.ProjectSpec) (lifecycle.ProjectStatus, error) {
	return lifecycle.ProjectStatus{}, nil
}
func (nopProjectSync) DeleteByName(context.Context, string) error { return nil }

// renderCRDs renders the chart's management CRDs (the manifests a deploy
// installs) into a directory envtest can load.
func renderCRDs(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatalf("helm is required to render the chart CRDs: %v", err)
	}
	_, here, _, _ := runtime.Caller(0)
	chart := filepath.Join(filepath.Dir(here), "..", "..", "..", "deploy", "chart")
	args := []string{"template", "astronomer", chart,
		"-f", filepath.Join(chart, "values.yaml"), "-f", filepath.Join(chart, "values-dev.yaml"),
		"--set", "crds.enabled=true",
		"--set", "secrets.secretKey=envtest-render-jwt-signing-key",
		"--set", "secrets.encryptionKey=AGE-SECRET-KEY-1YXRFCUA5M305R99XC5G7YRD9UFTGUE3PLFAC2Z2U8GSVLPZ9LQCQFHSDY9",
		"--show-only", "templates/crd-cluster.yaml",
		"--show-only", "templates/crd-project.yaml",
		"--show-only", "templates/crd-agentprofile.yaml",
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("helm", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template: %v\n%s", err, stderr.String())
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "crds.yaml"), stdout.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func startEnvtest(t *testing.T) *envtest.Environment {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set; run `make envtest` (or make test-integration) to fetch the envtest binaries")
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{renderCRDs(t)}, ErrorIfCRDPathMissing: true}
	if _, err := env.Start(); err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	return env
}

func waitReady(ctx context.Context, t *testing.T, c client.Client, names ...string) {
	t.Helper()
	for _, name := range names {
		for {
			var cluster lifecycle.Cluster
			err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, &cluster)
			if err == nil && len(cluster.Status.Conditions) == 1 && cluster.Status.Conditions[0].Status == metav1.ConditionTrue {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatalf("cluster %s never became Ready (last err %v)", name, err)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

func assertNoSameKeyOverlap(t *testing.T, intervals []interval) {
	t.Helper()
	for i := range intervals {
		for j := i + 1; j < len(intervals); j++ {
			a, b := intervals[i], intervals[j]
			if a.key == b.key && a.overlaps(b) {
				t.Fatalf("sync stage overlapped for %s: %s [%s..%s] vs %s [%s..%s]", a.key,
					a.tag, a.start.Format(time.StampMicro), a.end.Format(time.StampMicro),
					b.tag, b.start.Format(time.StampMicro), b.end.Format(time.StampMicro))
			}
		}
	}
}

func logIntervals(t *testing.T, title string, intervals []interval, origin time.Time) {
	t.Helper()
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	t.Log(title)
	for _, iv := range intervals {
		t.Logf("  %-24s %-9s %7.1fms → %7.1fms", iv.key, iv.tag,
			float64(iv.start.Sub(origin).Microseconds())/1000, float64(iv.end.Sub(origin).Microseconds())/1000)
	}
}

func directReconcile(ctx context.Context, t *testing.T, r reconcile.Reconciler, name, tag string, start <-chan struct{}, wg *sync.WaitGroup) {
	t.Helper()
	wg.Go(func() {
		<-start
		req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: name}}
		if _, err := r.Reconcile(context.WithValue(ctx, callTag{}, tag), req); err != nil {
			t.Errorf("direct reconcile %s/%s: %v", name, tag, err)
		}
	})
}

type harness struct {
	op          *wire.Operator
	client      client.Client
	observer    *syncObserver
	clusterSync *slowClusterSync
}

// startOperator composes the operator with every controller (the default
// configuration) and runs its manager until the test ends.
func startOperator(ctx context.Context, t *testing.T, env *envtest.Environment) *harness {
	t.Helper()
	h := &harness{observer: newSyncObserver(), clusterSync: &slowClusterSync{directDelay: 400 * time.Millisecond}}
	h.clusterSync.managerDelay.Store(int64(400 * time.Millisecond))
	op, err := wire.New(wire.Config{
		K8sConfig:               env.Config,
		WatchNamespace:          testNamespace,
		ClusterSync:             h.clusterSync,
		ProjectSync:             nopProjectSync{},
		Observer:                h.observer,
		PollPeriod:              time.Hour,
		MaxConcurrentReconciles: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.Cluster == nil || op.Project == nil || op.AgentProfile == nil {
		t.Fatal("default configuration must register every controller")
	}
	h.op = op
	t.Logf("cluster pipeline stages: %v", op.Cluster.StageNames())

	mgrCtx, stopMgr := context.WithCancel(ctx)
	mgrDone := make(chan error, 1)
	go func() { mgrDone <- op.Manager.Start(mgrCtx) }()
	t.Cleanup(func() {
		stopMgr()
		if err := <-mgrDone; err != nil {
			t.Errorf("manager: %v", err)
		}
	})
	h.client, err = client.New(env.Config, client.Options{Scheme: op.Manager.GetScheme()})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.client.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}); err != nil {
		t.Fatal(err)
	}
	return h
}

// managerPhase lets the manager's workers reconcile two new Clusters and
// asserts their sync stages overlapped while no key overlapped itself.
func managerPhase(ctx context.Context, t *testing.T, h *harness) int {
	t.Helper()
	origin := time.Now()
	for _, name := range []string{"alpha", "beta"} {
		cluster := &lifecycle.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: name}, Spec: lifecycle.ClusterSpec{Name: name}}
		if err := h.client.Create(ctx, cluster); err != nil {
			t.Fatal(err)
		}
	}
	waitReady(ctx, t, h.client, "alpha", "beta")
	h.clusterSync.managerDelay.Store(0)
	intervals, sameKey, distinct := h.observer.snapshot()
	logIntervals(t, "phase 1 (manager workers, two Clusters):", intervals, origin)
	t.Logf("phase 1: peak concurrent sync per key=%d, peak distinct keys in sync=%d", sameKey, distinct)
	if distinct < 2 {
		t.Fatalf("different Clusters never overlapped in the sync stage (peak distinct=%d)", distinct)
	}
	assertNoSameKeyOverlap(t, intervals)
	return len(intervals)
}

// directPhase fires two concurrent Reconcile calls for one Cluster and one
// for another from a single barrier.
func directPhase(ctx context.Context, t *testing.T, h *harness, skip int) {
	t.Helper()
	origin := time.Now()
	start := make(chan struct{})
	var wg sync.WaitGroup
	directReconcile(ctx, t, h.op.Cluster, "alpha", "direct-1", start, &wg)
	directReconcile(ctx, t, h.op.Cluster, "alpha", "direct-2", start, &wg)
	directReconcile(ctx, t, h.op.Cluster, "beta", "direct-3", start, &wg)
	close(start)
	wg.Wait()

	all, sameKey, _ := h.observer.snapshot()
	logIntervals(t, "phase 2 (direct concurrent Reconcile calls):", all[skip:], origin)
	direct := map[string]interval{}
	for _, iv := range all[skip:] {
		if iv.tag != "manager" {
			direct[iv.tag] = iv
		}
	}
	if len(direct) != 3 {
		t.Fatalf("expected 3 direct sync executions, got %d", len(direct))
	}
	assertNoSameKeyOverlap(t, all)
	if sameKey != 1 {
		t.Fatalf("peak concurrent sync executions for one key = %d, want 1", sameKey)
	}
	a1, a2, b := direct["direct-1"], direct["direct-2"], direct["direct-3"]
	if a1.overlaps(a2) {
		t.Fatal("the two reconciles of alpha overlapped")
	}
	if !b.overlaps(a1) && !b.overlaps(a2) {
		t.Fatal("the reconcile of beta did not overlap either reconcile of alpha")
	}
	first, second := a1, a2
	if second.start.Before(first.start) {
		first, second = second, first
	}
	t.Logf("alpha serialized: %s ended %s, %s started %s (gap %s); beta (%s) overlapped alpha",
		first.tag, first.end.Format(time.StampMicro), second.tag, second.start.Format(time.StampMicro), second.start.Sub(first.end), b.tag)
}

// TestOperatorSerializesPerObjectAndOverlapsAcrossObjects runs the composed
// operator against a real kube-apiserver (envtest) with the chart's CRDs and
// proves the lifecycle lock: two concurrent reconciles of one Cluster never
// execute the sync stage at the same time, while reconciles of two different
// Clusters do.
func TestOperatorSerializesPerObjectAndOverlapsAcrossObjects(t *testing.T) {
	t.Parallel()
	env := startEnvtest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h := startOperator(ctx, t, env)
	skip := managerPhase(ctx, t, h)
	directPhase(ctx, t, h, skip)
}
