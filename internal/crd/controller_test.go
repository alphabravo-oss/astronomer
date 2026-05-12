package crd

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ---------------------------------------------------------------------------
// Test fakes — minimal in-memory ClusterSync / ProjectSync implementations.
// ---------------------------------------------------------------------------

// fakeClusterSync records the EnsureFromCRD / DeleteByName calls and returns
// canned ClusterStatus values so we can assert what the reconciler patched.
type fakeClusterSync struct {
	mu        sync.Mutex
	ensured   []ClusterSpec
	deleted   []string
	resp      ClusterStatus
	ensureErr error
	// deletePolicy gates the controller's finalizer behaviour:
	//   "ok"          → DeleteByName returns nil immediately (finalizer drops)
	//   "in_progress" → returns ErrInProgress on the first call, nil after
	//   "fail"        → returns a synthetic error
	deletePolicy string
	deleteCalls  int
}

func (f *fakeClusterSync) EnsureFromCRD(_ context.Context, spec ClusterSpec) (ClusterStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, spec)
	if f.ensureErr != nil {
		return ClusterStatus{}, f.ensureErr
	}
	resp := f.resp
	if resp.ClusterID == "" {
		// Default canned response so the patch path runs.
		resp = ClusterStatus{
			ClusterID:    "11111111-1111-1111-1111-111111111111",
			Phase:        "registered",
			AgentVersion: "v0.0.1",
		}
	}
	return resp, nil
}

func (f *fakeClusterSync) DeleteByName(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, name)
	f.deleteCalls++
	switch f.deletePolicy {
	case "fail":
		return errors.New("fake delete failed")
	case "in_progress":
		if f.deleteCalls == 1 {
			return ErrInProgress
		}
		return nil
	default:
		return nil
	}
}

// fakeProjectSync mirrors fakeClusterSync for the Project CRD.
type fakeProjectSync struct {
	mu        sync.Mutex
	ensured   []ProjectSpec
	deleted   []string
	resp      ProjectStatus
	ensureErr error
}

func (f *fakeProjectSync) EnsureFromCRD(_ context.Context, spec ProjectSpec) (ProjectStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, spec)
	if f.ensureErr != nil {
		return ProjectStatus{}, f.ensureErr
	}
	resp := f.resp
	if resp.ProjectID == "" {
		resp = ProjectStatus{
			ProjectID:         "22222222-2222-2222-2222-222222222222",
			Phase:             "active",
			ResolvedClusterID: "11111111-1111-1111-1111-111111111111",
		}
	}
	return resp, nil
}

func (f *fakeProjectSync) DeleteByName(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, name)
	return nil
}

// newTestScheme builds a runtime.Scheme registered with the CRD kinds — the
// fake client needs to know about them before it can store typed objects.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

// newClusterReconciler builds an isolated reconciler + fake client for a test.
// Tests should pass the initial CR objects via objs so the fake client knows
// about them ahead of Reconcile.
func newClusterReconciler(t *testing.T, sync ClusterSync, objs ...client.Object) (*ClusterReconciler, client.Client) {
	t.Helper()
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		// The fake client doesn't auto-merge the status subresource unless
		// we register the types — without this, status patches are dropped
		// and TestCRDStatus_PatchesAfterReconcile would fail spuriously.
		WithStatusSubresource(&Cluster{}, &Project{}).
		Build()
	return &ClusterReconciler{
		Client: c,
		Sync:   sync,
		Log:    slog.Default(),
	}, c
}

func newProjectReconciler(t *testing.T, sync ProjectSync, objs ...client.Object) (*ProjectReconciler, client.Client) {
	t.Helper()
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&Cluster{}, &Project{}).
		Build()
	return &ProjectReconciler{
		Client: c,
		Sync:   sync,
		Log:    slog.Default(),
	}, c
}

// ---------------------------------------------------------------------------
// Cluster reconciler tests.
// ---------------------------------------------------------------------------

func TestClusterCRD_ReconcileCreates(t *testing.T) {
	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-us-east", Namespace: "astronomer-mgmt"},
		Spec: ClusterSpec{
			Name:        "prod-us-east",
			Environment: "production",
			Region:      "us-east-1",
			Labels:      map[string]string{"tier": "prod"},
		},
	}
	sync := &fakeClusterSync{}
	r, _ := newClusterReconciler(t, sync, cluster)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-us-east", Namespace: "astronomer-mgmt"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(sync.ensured) != 1 {
		t.Fatalf("expected 1 EnsureFromCRD call, got %d", len(sync.ensured))
	}
	if got := sync.ensured[0]; got.Name != "prod-us-east" || got.Environment != "production" {
		t.Fatalf("unexpected spec passed: %+v", got)
	}
}

func TestClusterCRD_ReconcileUpdates(t *testing.T) {
	// A spec change → another EnsureFromCRD pass on the next Reconcile.
	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-us-east", Namespace: "astronomer-mgmt"},
		Spec:       ClusterSpec{Name: "prod-us-east", Environment: "staging"},
	}
	sync := &fakeClusterSync{}
	r, c := newClusterReconciler(t, sync, cluster)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-us-east", Namespace: "astronomer-mgmt"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile 1: %v", err)
	}

	// Refresh + mutate the CR spec (operator did `kubectl edit`).
	var current Cluster
	if err := c.Get(context.Background(), req.NamespacedName, &current); err != nil {
		t.Fatalf("Get current: %v", err)
	}
	current.Spec.Environment = "production"
	current.Spec.Region = "us-east-1"
	if err := c.Update(context.Background(), &current); err != nil {
		t.Fatalf("Update spec: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile 2: %v", err)
	}
	if len(sync.ensured) != 2 {
		t.Fatalf("expected 2 EnsureFromCRD calls, got %d", len(sync.ensured))
	}
	if got := sync.ensured[1]; got.Environment != "production" || got.Region != "us-east-1" {
		t.Fatalf("update pass did not propagate spec change: %+v", got)
	}
}

func TestClusterCRD_ReconcileDeletes(t *testing.T) {
	now := metav1.Now()
	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "prod-us-east",
			Namespace:         "astronomer-mgmt",
			Finalizers:        []string{FinalizerCluster},
			DeletionTimestamp: &now,
		},
		Spec: ClusterSpec{Name: "prod-us-east"},
	}
	sync := &fakeClusterSync{deletePolicy: "ok"}
	r, c := newClusterReconciler(t, sync, cluster)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-us-east", Namespace: "astronomer-mgmt"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(sync.deleted) != 1 || sync.deleted[0] != "prod-us-east" {
		t.Fatalf("expected DeleteByName(prod-us-east), got %v", sync.deleted)
	}

	// After a successful delete the finalizer should be gone and the fake
	// client should report the object NotFound (deletion cascade kicked in).
	var after Cluster
	err := c.Get(context.Background(), req.NamespacedName, &after)
	if err == nil {
		// fake client may still return the object minus the finalizer; verify.
		for _, f := range after.ObjectMeta.Finalizers {
			if f == FinalizerCluster {
				t.Fatalf("finalizer still present after delete")
			}
		}
	}
}

func TestClusterCRD_DeleteRequeuesWhileInProgress(t *testing.T) {
	// First Reconcile sees ErrInProgress → finalizer stays, requeued.
	// Second Reconcile sees nil → finalizer dropped.
	now := metav1.Now()
	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "prod-us-east",
			Namespace:         "astronomer-mgmt",
			Finalizers:        []string{FinalizerCluster},
			DeletionTimestamp: &now,
		},
		Spec: ClusterSpec{Name: "prod-us-east"},
	}
	sync := &fakeClusterSync{deletePolicy: "in_progress"}
	r, c := newClusterReconciler(t, sync, cluster)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-us-east", Namespace: "astronomer-mgmt"}}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile 1: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("expected requeue while delete is in progress, got %+v", res)
	}

	// Finalizer should still be present after the first pass.
	var mid Cluster
	if err := c.Get(context.Background(), req.NamespacedName, &mid); err != nil {
		t.Fatalf("Get mid: %v", err)
	}
	found := false
	for _, f := range mid.ObjectMeta.Finalizers {
		if f == FinalizerCluster {
			found = true
		}
	}
	if !found {
		t.Fatalf("finalizer removed prematurely")
	}

	// Second pass — finalizer drops.
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile 2: %v", err)
	}
	if sync.deleteCalls != 2 {
		t.Fatalf("expected 2 DeleteByName calls, got %d", sync.deleteCalls)
	}
}

// ---------------------------------------------------------------------------
// Project reconciler tests.
// ---------------------------------------------------------------------------

func TestProjectCRD_ReconcileCreates(t *testing.T) {
	project := &Project{
		ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: "astronomer-mgmt"},
		Spec: ProjectSpec{
			Name:               "platform",
			Description:        "Platform team",
			PodSecurityProfile: "baseline",
			NetworkPolicyMode:  "isolated",
			ResourceQuota:      ProjectResourceQuota{CPULimit: "16", MemoryLimit: "32Gi", PodCount: 50},
			Clusters:           []string{"prod-us-east"},
		},
	}
	sync := &fakeProjectSync{}
	r, _ := newProjectReconciler(t, sync, project)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "platform", Namespace: "astronomer-mgmt"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(sync.ensured) != 1 {
		t.Fatalf("expected 1 EnsureFromCRD call, got %d", len(sync.ensured))
	}
	got := sync.ensured[0]
	if got.Name != "platform" || got.PodSecurityProfile != "baseline" {
		t.Fatalf("unexpected spec passed: %+v", got)
	}
	if got.ResourceQuota.CPULimit != "16" || got.ResourceQuota.PodCount != 50 {
		t.Fatalf("quota not propagated: %+v", got.ResourceQuota)
	}
	if len(got.Clusters) != 1 || got.Clusters[0] != "prod-us-east" {
		t.Fatalf("cluster ref not propagated: %+v", got.Clusters)
	}
}

func TestProjectCRD_ReconcileUpdates(t *testing.T) {
	project := &Project{
		ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: "astronomer-mgmt"},
		Spec: ProjectSpec{
			Name:     "platform",
			Clusters: []string{"prod-us-east"},
		},
	}
	sync := &fakeProjectSync{}
	r, c := newProjectReconciler(t, sync, project)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "platform", Namespace: "astronomer-mgmt"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile 1: %v", err)
	}

	var current Project
	if err := c.Get(context.Background(), req.NamespacedName, &current); err != nil {
		t.Fatalf("Get: %v", err)
	}
	current.Spec.ResourceQuota = ProjectResourceQuota{CPULimit: "32", MemoryLimit: "64Gi", PodCount: 200}
	current.Spec.PodSecurityProfile = "restricted"
	if err := c.Update(context.Background(), &current); err != nil {
		t.Fatalf("Update spec: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile 2: %v", err)
	}
	if len(sync.ensured) != 2 {
		t.Fatalf("expected 2 EnsureFromCRD calls, got %d", len(sync.ensured))
	}
	if sync.ensured[1].ResourceQuota.PodCount != 200 || sync.ensured[1].PodSecurityProfile != "restricted" {
		t.Fatalf("update pass did not propagate spec change: %+v", sync.ensured[1])
	}
}

// ---------------------------------------------------------------------------
// Status patching.
// ---------------------------------------------------------------------------

func TestCRDStatus_PatchesAfterReconcile(t *testing.T) {
	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-us-east", Namespace: "astronomer-mgmt"},
		Spec: ClusterSpec{
			Name:        "prod-us-east",
			ProjectRefs: []string{"platform", "billing"},
		},
	}
	sync := &fakeClusterSync{
		resp: ClusterStatus{
			ClusterID:    "abc",
			Phase:        "registered",
			AgentVersion: "v9.9.9",
		},
	}
	r, c := newClusterReconciler(t, sync, cluster)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-us-east", Namespace: "astronomer-mgmt"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var after Cluster
	if err := c.Get(context.Background(), req.NamespacedName, &after); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Status.ClusterID != "abc" {
		t.Fatalf("status.clusterId not patched: %q", after.Status.ClusterID)
	}
	if after.Status.Phase != "registered" {
		t.Fatalf("status.phase not patched: %q", after.Status.Phase)
	}
	if after.Status.AgentVersion != "v9.9.9" {
		t.Fatalf("status.agentVersion not patched: %q", after.Status.AgentVersion)
	}
	if after.Status.LastReconciled.IsZero() {
		t.Fatalf("status.lastReconciled was not stamped")
	}
	if len(after.Status.ObservedProjectRefs) != 2 {
		t.Fatalf("status.observedProjectRefs not echoed: %+v", after.Status.ObservedProjectRefs)
	}
	// Finalizer should have been installed on the first pass too.
	foundFinalizer := false
	for _, f := range after.ObjectMeta.Finalizers {
		if f == FinalizerCluster {
			foundFinalizer = true
			break
		}
	}
	if !foundFinalizer {
		t.Fatalf("finalizer not installed by Reconcile")
	}
}

func TestProjectCRD_StatusReflectsResolvedCluster(t *testing.T) {
	project := &Project{
		ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: "astronomer-mgmt"},
		Spec:       ProjectSpec{Name: "platform", Clusters: []string{"prod-us-east", "ignored-extra"}},
	}
	sync := &fakeProjectSync{
		resp: ProjectStatus{ProjectID: "p1", Phase: "active", ResolvedClusterID: "c1"},
	}
	r, c := newProjectReconciler(t, sync, project)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "platform", Namespace: "astronomer-mgmt"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var after Project
	if err := c.Get(context.Background(), req.NamespacedName, &after); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Status.ResolvedClusterID != "c1" {
		t.Fatalf("resolved cluster not echoed: %+v", after.Status)
	}
	if len(after.Status.ObservedClusters) != 2 {
		t.Fatalf("status.observedClusters not echoed: %+v", after.Status.ObservedClusters)
	}
}
