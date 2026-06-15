package crd

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ---------------------------------------------------------------------------
// Test fakes — minimal in-memory ClusterSync / ProjectSync implementations.
// ---------------------------------------------------------------------------

// fakeClusterSync records the EnsureFromCRD / DeleteByName calls and returns
// canned ClusterStatus values so we can assert what the reconciler patched.
type fakeClusterSync struct {
	mu          sync.Mutex
	ensured     []ClusterSpec
	deleted     []string
	ownership   []ObjectRef
	resp        ClusterStatus
	ensureErr   error
	ownerErr    error
	validateErr error
	// deletePolicy gates the controller's finalizer behaviour:
	//   "ok"          → DeleteByName returns nil immediately (finalizer drops)
	//   "in_progress"        → returns ErrInProgress on the first call, nil after
	//   "always_in_progress" → always returns ErrInProgress
	//   "fail"               → returns a synthetic error
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
	case "always_in_progress":
		return ErrInProgress
	default:
		return nil
	}
}

func (f *fakeClusterSync) ValidateClusterOwnership(context.Context, ClusterSpec, ObjectRef) error {
	return f.validateErr
}

func (f *fakeClusterSync) RecordClusterOwnership(_ context.Context, _ ClusterSpec, ref ObjectRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ownerErr != nil {
		return f.ownerErr
	}
	f.ownership = append(f.ownership, ref)
	return nil
}

// fakeProjectSync mirrors fakeClusterSync for the Project CRD.
type fakeProjectSync struct {
	mu           sync.Mutex
	ensured      []ProjectSpec
	deleted      []string
	ownership    []ObjectRef
	resp         ProjectStatus
	ensureErr    error
	ownerErr     error
	validateErr  error
	deletePolicy string
	deleteCalls  int
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
	f.deleteCalls++
	switch f.deletePolicy {
	case "always_in_progress":
		return ErrInProgress
	case "fail":
		return errors.New("fake project delete failed")
	default:
		return nil
	}
}

func (f *fakeProjectSync) ValidateProjectOwnership(context.Context, ProjectSpec, ObjectRef) error {
	return f.validateErr
}

func (f *fakeProjectSync) RecordProjectOwnership(_ context.Context, _ ProjectSpec, ref ObjectRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ownerErr != nil {
		return f.ownerErr
	}
	f.ownership = append(f.ownership, ref)
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
	s.AddKnownTypeWithName(applicationSetGVK, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(applicationSetGVK.GroupVersion().WithKind("ApplicationSetList"), &unstructured.UnstructuredList{})
	s.AddKnownTypeWithName(applicationGVK, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(applicationGVK.GroupVersion().WithKind("ApplicationList"), &unstructured.UnstructuredList{})
	s.AddKnownTypeWithName(configMapGVK, &unstructured.Unstructured{})
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
		WithStatusSubresource(&Cluster{}, &Project{}, &ClusterBaseline{}, &ComponentBundle{}, &AgentProfile{}, &GitOpsTarget{}).
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
		WithStatusSubresource(&Cluster{}, &Project{}, &ClusterBaseline{}, &ComponentBundle{}, &AgentProfile{}, &GitOpsTarget{}).
		Build()
	return &ProjectReconciler{
		Client: c,
		Sync:   sync,
		Log:    slog.Default(),
	}, c
}

func newValidationClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&Cluster{}, &Project{}, &ClusterBaseline{}, &ComponentBundle{}, &AgentProfile{}, &GitOpsTarget{}).
		Build()
}

func newValidationClientWithInterceptors(t *testing.T, interceptors interceptor.Funcs, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&Cluster{}, &Project{}, &ClusterBaseline{}, &ComponentBundle{}, &AgentProfile{}, &GitOpsTarget{}).
		WithInterceptorFuncs(interceptors).
		Build()
}

// ---------------------------------------------------------------------------
// Cluster reconciler tests.
// ---------------------------------------------------------------------------

func TestClusterCRD_ReconcileCreates(t *testing.T) {
	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-us-east", Namespace: "astronomer-mgmt", Generation: 7},
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
	if len(sync.ownership) != 1 {
		t.Fatalf("expected 1 ownership call, got %d", len(sync.ownership))
	}
	if got := sync.ownership[0]; got.APIVersion != GroupVersion.String() || got.Kind != "Cluster" || got.Namespace != "astronomer-mgmt" || got.Name != "prod-us-east" || got.Generation != 7 {
		t.Fatalf("unexpected ownership ref: %+v", got)
	}
}

func TestClusterCRD_ResolvesAgentProfileRefBeforeSync(t *testing.T) {
	profile := &AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "team-operator", Namespace: "astronomer-mgmt"},
		Spec: AgentProfileSpec{
			PrivilegeProfile: "namespace-operator",
			NamespaceScope:   []string{"platform"},
			Install: AgentProfileInstallSpec{
				Image:              "registry.example.com/agent:v9",
				ServiceAccountName: "team-agent",
				PodLabels:          map[string]string{"team": "platform"},
			},
		},
	}
	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-us-east", Namespace: "astronomer-mgmt", Generation: 7},
		Spec: ClusterSpec{
			Name: "prod-us-east",
			Agent: ClusterAgentSpec{
				ProfileRef: "team-operator",
			},
		},
	}
	sync := &fakeClusterSync{}
	r, _ := newClusterReconciler(t, sync, cluster, profile)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-us-east", Namespace: "astronomer-mgmt"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(sync.ensured) != 1 {
		t.Fatalf("expected 1 EnsureFromCRD call, got %d", len(sync.ensured))
	}
	got := sync.ensured[0]
	if got.Agent.PrivilegeProfile != "namespace-operator" {
		t.Fatalf("resolved privilege profile = %q, want namespace-operator", got.Agent.PrivilegeProfile)
	}
	if got.Annotations["management.astronomer.io/agent-profile-ref"] != "team-operator" {
		t.Fatalf("agent profile ref annotation not projected: %+v", got.Annotations)
	}
	if got.Annotations["management.astronomer.io/agent-profile-api-version"] != GroupVersion.String() {
		t.Fatalf("agent profile api version annotation not projected: %+v", got.Annotations)
	}
	if got.Annotations[agenttemplate.AgentImageAnnotation] != "registry.example.com/agent:v9" {
		t.Fatalf("agent image annotation not projected: %+v", got.Annotations)
	}
	if got.Annotations[agenttemplate.AgentServiceAccountNameAnnotation] != "team-agent" {
		t.Fatalf("agent service account annotation not projected: %+v", got.Annotations)
	}
	var podLabels map[string]string
	if err := json.Unmarshal([]byte(got.Annotations[agenttemplate.AgentPodLabelsAnnotation]), &podLabels); err != nil {
		t.Fatalf("pod labels annotation was not JSON: %v", err)
	}
	if podLabels["team"] != "platform" {
		t.Fatalf("agent pod labels annotation not projected: %+v", podLabels)
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

func TestClusterCRD_OwnershipValidationFailureStopsSync(t *testing.T) {
	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-us-east", Namespace: "astronomer-mgmt"},
		Spec:       ClusterSpec{Name: "prod-us-east"},
	}
	sync := &fakeClusterSync{validateErr: errors.New("takeover refused")}
	r, _ := newClusterReconciler(t, sync, cluster)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-us-east", Namespace: "astronomer-mgmt"}}
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatalf("expected ownership validation error")
	}
	if len(sync.ensured) != 0 {
		t.Fatalf("EnsureFromCRD should not be called after validation failure")
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
		for _, f := range after.Finalizers {
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
	for _, f := range mid.Finalizers {
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

func TestClusterCRD_DeleteSurfacesFinalizerTimeout(t *testing.T) {
	old := metav1.NewTime(time.Now().Add(-finalizerTimeout - time.Minute))
	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "prod-us-east",
			Namespace:         "astronomer-mgmt",
			Finalizers:        []string{FinalizerCluster},
			DeletionTimestamp: &old,
			Generation:        5,
		},
		Spec: ClusterSpec{Name: "prod-us-east"},
	}
	sync := &fakeClusterSync{deletePolicy: "always_in_progress"}
	r, c := newClusterReconciler(t, sync, cluster)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-us-east", Namespace: "astronomer-mgmt"}}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("expected requeue while delete is in progress, got %+v", res)
	}
	var after Cluster
	if err := c.Get(context.Background(), req.NamespacedName, &after); err != nil {
		t.Fatalf("Get after timeout: %v", err)
	}
	if after.Status.Phase != "DeletingTimedOut" {
		t.Fatalf("expected DeletingTimedOut phase, got %+v", after.Status)
	}
	if conditionReason(after.Status.Conditions, "Ready") != "FinalizerTimeout" {
		t.Fatalf("Ready condition did not surface finalizer timeout: %+v", after.Status.Conditions)
	}
	if !hasFinalizer(after.Finalizers, FinalizerCluster) {
		t.Fatalf("finalizer removed despite timed-out in-progress delete")
	}
}

// ---------------------------------------------------------------------------
// Project reconciler tests.
// ---------------------------------------------------------------------------

func TestProjectCRD_ReconcileCreates(t *testing.T) {
	project := &Project{
		ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: "astronomer-mgmt", Generation: 11},
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
	if len(sync.ownership) != 1 {
		t.Fatalf("expected 1 ownership call, got %d", len(sync.ownership))
	}
	if got := sync.ownership[0]; got.APIVersion != GroupVersion.String() || got.Kind != "Project" || got.Namespace != "astronomer-mgmt" || got.Name != "platform" || got.Generation != 11 {
		t.Fatalf("unexpected ownership ref: %+v", got)
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

func TestProjectCRD_OwnershipValidationFailureStopsSync(t *testing.T) {
	project := &Project{
		ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: "astronomer-mgmt"},
		Spec:       ProjectSpec{Name: "platform", Clusters: []string{"prod-us-east"}},
	}
	sync := &fakeProjectSync{validateErr: errors.New("takeover refused")}
	r, _ := newProjectReconciler(t, sync, project)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "platform", Namespace: "astronomer-mgmt"}}
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatalf("expected ownership validation error")
	}
	if len(sync.ensured) != 0 {
		t.Fatalf("EnsureFromCRD should not be called after validation failure")
	}
}

func TestProjectCRD_DeleteSurfacesFinalizerTimeout(t *testing.T) {
	old := metav1.NewTime(time.Now().Add(-finalizerTimeout - time.Minute))
	project := &Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "platform",
			Namespace:         "astronomer-mgmt",
			Finalizers:        []string{FinalizerProject},
			DeletionTimestamp: &old,
			Generation:        6,
		},
		Spec: ProjectSpec{Name: "platform", Clusters: []string{"prod-us-east"}},
	}
	sync := &fakeProjectSync{deletePolicy: "always_in_progress"}
	r, c := newProjectReconciler(t, sync, project)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "platform", Namespace: "astronomer-mgmt"}}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("expected requeue while project delete is in progress, got %+v", res)
	}
	var after Project
	if err := c.Get(context.Background(), req.NamespacedName, &after); err != nil {
		t.Fatalf("Get after timeout: %v", err)
	}
	if after.Status.Phase != "DeletingTimedOut" {
		t.Fatalf("expected DeletingTimedOut phase, got %+v", after.Status)
	}
	if conditionReason(after.Status.Conditions, "Ready") != "FinalizerTimeout" {
		t.Fatalf("Ready condition did not surface finalizer timeout: %+v", after.Status.Conditions)
	}
	if !hasFinalizer(after.Finalizers, FinalizerProject) {
		t.Fatalf("finalizer removed despite timed-out in-progress delete")
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
			ArgoCD: ClusterArgoCDStatus{
				Phase:             "registered",
				ClusterSecretName: "astronomer-prod-us-east",
			},
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
	if after.Status.ArgoCD.Phase != "registered" {
		t.Fatalf("status.argocd.phase not patched: %q", after.Status.ArgoCD.Phase)
	}
	if after.Status.ArgoCD.ClusterSecretName != "astronomer-prod-us-east" {
		t.Fatalf("status.argocd.clusterSecretName not patched: %q", after.Status.ArgoCD.ClusterSecretName)
	}
	if after.Status.LastReconciled.IsZero() {
		t.Fatalf("status.lastReconciled was not stamped")
	}
	if len(after.Status.ObservedProjectRefs) != 2 {
		t.Fatalf("status.observedProjectRefs not echoed: %+v", after.Status.ObservedProjectRefs)
	}
	// Finalizer should have been installed on the first pass too.
	foundFinalizer := false
	for _, f := range after.Finalizers {
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

// ---------------------------------------------------------------------------
// Validation/status reconcilers for schema-only CRDs.
// ---------------------------------------------------------------------------

func TestClusterBaselineReconciler_PatchesReadyAndValidationStatus(t *testing.T) {
	bundle := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "ingress-nginx", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version:          "1.0.0",
			DefaultNamespace: "ingress-nginx",
			Source: ComponentBundleSourceSpec{
				Type:           "helm",
				RepoURL:        "https://kubernetes.github.io/ingress-nginx",
				Chart:          "ingress-nginx",
				TargetRevision: "4.12.0",
			},
		},
	}
	baseline := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "baseline", Namespace: "astronomer-mgmt", Generation: 3},
		Spec: ClusterBaselineSpec{
			ClusterSelector: LabelSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			Bundles:         []ClusterBaselineBundleRef{{Name: "ingress-nginx", Version: "1.0.0"}},
		},
	}
	invalid := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "invalid", Namespace: "astronomer-mgmt", Generation: 4},
		Spec:       ClusterBaselineSpec{},
	}
	suspended := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "suspended", Namespace: "astronomer-mgmt", Generation: 5},
		Spec:       ClusterBaselineSpec{Suspended: true},
	}
	c := newValidationClient(t, bundle, baseline, invalid, suspended)
	r := &ClusterBaselineReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "baseline", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile baseline: %v", err)
	}
	var after ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "baseline", Namespace: "astronomer-mgmt"}, &after); err != nil {
		t.Fatalf("Get baseline: %v", err)
	}
	if after.Status.Phase != "Ready" {
		t.Fatalf("expected Ready phase, got %+v", after.Status)
	}
	if after.Status.ObservedGeneration != 3 {
		t.Fatalf("observed generation not patched: %+v", after.Status)
	}
	if len(after.Status.TargetedClusters) != 1 || after.Status.TargetedClusters[0] != "prod-us-east" {
		t.Fatalf("targeted clusters not echoed: %+v", after.Status.TargetedClusters)
	}
	if conditionStatus(after.Status.Conditions, "Accepted") != metav1.ConditionTrue {
		t.Fatalf("Accepted condition not true: %+v", after.Status.Conditions)
	}
	if !hasFinalizer(after.Finalizers, FinalizerClusterBaseline) {
		t.Fatalf("finalizer not installed: %+v", after.Finalizers)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "invalid", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile invalid: %v", err)
	}
	var invalidAfter ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "invalid", Namespace: "astronomer-mgmt"}, &invalidAfter); err != nil {
		t.Fatalf("Get invalid: %v", err)
	}
	if invalidAfter.Status.Phase != "Degraded" {
		t.Fatalf("expected Degraded phase, got %+v", invalidAfter.Status)
	}
	if conditionStatus(invalidAfter.Status.Conditions, "Accepted") != metav1.ConditionFalse {
		t.Fatalf("Accepted condition not false: %+v", invalidAfter.Status.Conditions)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "suspended", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile suspended: %v", err)
	}
	var suspendedAfter ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "suspended", Namespace: "astronomer-mgmt"}, &suspendedAfter); err != nil {
		t.Fatalf("Get suspended: %v", err)
	}
	if suspendedAfter.Status.Phase != "Suspended" {
		t.Fatalf("expected Suspended phase, got %+v", suspendedAfter.Status)
	}
	if conditionStatus(suspendedAfter.Status.Conditions, "Accepted") != metav1.ConditionTrue {
		t.Fatalf("Accepted condition not true for suspended object: %+v", suspendedAfter.Status.Conditions)
	}
	if conditionStatus(suspendedAfter.Status.Conditions, "Ready") != metav1.ConditionFalse {
		t.Fatalf("Ready condition not false for suspended object: %+v", suspendedAfter.Status.Conditions)
	}
}

func TestClusterBaselineReconciler_GeneratesBundleApplicationSetsAndDeletesStale(t *testing.T) {
	bundle := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "ingress", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version:          "1.0.0",
			DefaultNamespace: "ingress-nginx",
			Source: ComponentBundleSourceSpec{
				Type:           "helm",
				RepoURL:        "https://kubernetes.github.io/ingress-nginx",
				Chart:          "ingress-nginx",
				TargetRevision: "4.12.0",
			},
		},
	}
	baseline := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-baseline", Namespace: "astronomer-mgmt", Generation: 2},
		Spec: ClusterBaselineSpec{
			ClusterSelector: LabelSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			Bundles: []ClusterBaselineBundleRef{{
				Name:       "ingress",
				Version:    "1.0.0",
				Values:     map[string]string{"controller.metrics.enabled": "true"},
				ValuesFrom: []ClusterBaselineValuesSource{{Type: "git", Path: "values/prod.yaml"}},
			}},
			SyncPolicy: ClusterBaselineSyncPolicy{Automated: true, Prune: true, SelfHeal: true},
		},
	}
	stale := testApplicationSet("astronomer-baseline-stale", defaultArgoNamespace, crdSourceLabels("ClusterBaseline", "astronomer-mgmt", "prod-baseline"))
	appLabels := crdSourceLabels("ClusterBaseline", "astronomer-mgmt", "prod-baseline")
	appLabels["astronomer.io/bundle-name"] = dnsLabel("ingress")
	childApp := testApplication("astro-prod-baseline-ingress-prod-us-east", defaultArgoNamespace, appLabels, "OutOfSync", "Progressing")
	c := newValidationClient(t, bundle, baseline, stale, childApp)
	r := &ClusterBaselineReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile baseline: %v", err)
	}
	var after ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}, &after); err != nil {
		t.Fatalf("Get baseline: %v", err)
	}
	if after.Status.Phase != "Ready" {
		t.Fatalf("expected Ready phase, got %+v", after.Status)
	}
	if len(after.Status.Applications) != 1 {
		t.Fatalf("expected 1 generated ApplicationSet status, got %+v", after.Status.Applications)
	}
	if after.Status.Applications[0].SyncStatus != "OutOfSync" || after.Status.Applications[0].Health != "Progressing" || after.Status.Applications[0].ApplicationCount != 1 {
		t.Fatalf("generated ApplicationSet rollup not patched: %+v", after.Status.Applications[0])
	}
	if len(after.Status.Applications[0].ChildApplications) != 1 {
		t.Fatalf("expected child Application details, got %+v", after.Status.Applications[0].ChildApplications)
	}
	childStatus := after.Status.Applications[0].ChildApplications[0]
	if childStatus.Name != "astro-prod-baseline-ingress-prod-us-east" || childStatus.Revision != "abc123" || childStatus.OperationPhase != "Running" || len(childStatus.Resources) != 1 {
		t.Fatalf("child Application details not patched: %+v", childStatus)
	}
	var appSet unstructured.Unstructured
	appSet.SetGroupVersionKind(applicationSetGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: after.Status.Applications[0].Name, Namespace: defaultArgoNamespace}, &appSet); err != nil {
		t.Fatalf("Get generated ApplicationSet: %v", err)
	}
	if appSet.GetAnnotations()[applicationSetSpecHashAnnotation] == "" {
		t.Fatalf("generated ApplicationSet missing desired spec hash annotation: %+v", appSet.GetAnnotations())
	}
	generators, found, err := unstructured.NestedSlice(appSet.Object, "spec", "generators")
	if err != nil || !found || len(generators) != 1 {
		t.Fatalf("unexpected generated generators found=%v len=%d err=%v", found, len(generators), err)
	}
	generator := generators[0].(map[string]any)
	clusters := generator["clusters"].(map[string]any)
	selector := clusters["selector"].(map[string]any)
	expressions := selector["matchExpressions"].([]any)
	if len(expressions) != 1 {
		t.Fatalf("expected clusterRefs match expression, got %+v", selector)
	}
	revision, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "targetRevision")
	if revision != "4.12.0" {
		t.Fatalf("generated targetRevision = %q", revision)
	}
	namespace, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "destination", "namespace")
	if namespace != "ingress-nginx" {
		t.Fatalf("generated destination namespace = %q", namespace)
	}
	parameters, found, err := unstructured.NestedSlice(appSet.Object, "spec", "template", "spec", "source", "helm", "parameters")
	if err != nil || !found || len(parameters) != 1 {
		t.Fatalf("expected helm parameter override, found=%v len=%d err=%v", found, len(parameters), err)
	}
	valueFiles, found, err := unstructured.NestedStringSlice(appSet.Object, "spec", "template", "spec", "source", "helm", "valueFiles")
	if err != nil || !found || len(valueFiles) != 1 || valueFiles[0] != "values/prod.yaml" {
		t.Fatalf("expected helm value file override, found=%v values=%+v err=%v", found, valueFiles, err)
	}
	prune, _, _ := unstructured.NestedBool(appSet.Object, "spec", "template", "spec", "syncPolicy", "automated", "prune")
	selfHeal, _, _ := unstructured.NestedBool(appSet.Object, "spec", "template", "spec", "syncPolicy", "automated", "selfHeal")
	if !prune || !selfHeal {
		t.Fatalf("generated automated sync policy prune=%v selfHeal=%v", prune, selfHeal)
	}
	var staleAfter unstructured.Unstructured
	staleAfter.SetGroupVersionKind(applicationSetGVK)
	err = c.Get(context.Background(), types.NamespacedName{Name: stale.GetName(), Namespace: stale.GetNamespace()}, &staleAfter)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("stale ApplicationSet still exists or unexpected error: %v", err)
	}
}

func TestClusterBaselineReconciler_RefusesToOverwriteUnownedApplicationSet(t *testing.T) {
	bundle := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "ingress", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version:          "1.0.0",
			DefaultNamespace: "ingress-nginx",
			Source: ComponentBundleSourceSpec{
				Type:           "helm",
				RepoURL:        "https://kubernetes.github.io/ingress-nginx",
				Chart:          "ingress-nginx",
				TargetRevision: "4.12.0",
			},
		},
	}
	ref := ClusterBaselineBundleRef{Name: "ingress", Version: "1.0.0"}
	baseline := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-baseline", Namespace: "astronomer-mgmt", Generation: 2},
		Spec: ClusterBaselineSpec{
			ClusterSelector: LabelSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			Bundles:         []ClusterBaselineBundleRef{ref},
		},
	}
	conflict := testApplicationSet(clusterBaselineApplicationSetName(*baseline, ref), defaultArgoNamespace, map[string]string{"app.kubernetes.io/managed-by": "other"})
	c := newValidationClient(t, bundle, baseline, conflict)
	r := &ClusterBaselineReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile baseline: %v", err)
	}
	var after ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}, &after); err != nil {
		t.Fatalf("Get baseline: %v", err)
	}
	if after.Status.Phase != "Degraded" {
		t.Fatalf("expected Degraded status after ownership conflict, got %+v", after.Status)
	}
	var appSet unstructured.Unstructured
	appSet.SetGroupVersionKind(applicationSetGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: conflict.GetName(), Namespace: conflict.GetNamespace()}, &appSet); err != nil {
		t.Fatalf("Get conflicting ApplicationSet: %v", err)
	}
	if appSet.GetLabels()["app.kubernetes.io/managed-by"] != "other" {
		t.Fatalf("conflicting ApplicationSet was overwritten: %+v", appSet.GetLabels())
	}
}

func TestClusterBaselineReconciler_DegradesOnBundleVersionMismatch(t *testing.T) {
	bundle := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "ingress", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version: "1.0.0",
			Source: ComponentBundleSourceSpec{
				Type:           "helm",
				RepoURL:        "https://kubernetes.github.io/ingress-nginx",
				Chart:          "ingress-nginx",
				TargetRevision: "4.12.0",
			},
		},
	}
	baseline := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-baseline", Namespace: "astronomer-mgmt", Generation: 2},
		Spec: ClusterBaselineSpec{
			ClusterSelector: LabelSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			Bundles:         []ClusterBaselineBundleRef{{Name: "ingress", Version: "2.0.0"}},
		},
	}
	c := newValidationClient(t, bundle, baseline)
	r := &ClusterBaselineReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile baseline: %v", err)
	}
	var after ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}, &after); err != nil {
		t.Fatalf("Get baseline: %v", err)
	}
	if after.Status.Phase != "Degraded" {
		t.Fatalf("expected Degraded status after version mismatch, got %+v", after.Status)
	}
}

func TestClusterBaselineReconciler_ResolvesVersionedComponentBundleCatalog(t *testing.T) {
	bundle := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "ingress", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version:          "1.0.0",
			DefaultNamespace: "ingress-legacy",
			Source: ComponentBundleSourceSpec{
				Type:           "helm",
				RepoURL:        "https://kubernetes.github.io/ingress-nginx",
				Chart:          "ingress-nginx-legacy",
				TargetRevision: "4.12.0",
			},
			Versions: []ComponentBundleVersionSpec{{
				Version:          "2.0.0",
				DefaultNamespace: "ingress-v2",
				Source: ComponentBundleSourceSpec{
					Type:           "helm",
					RepoURL:        "https://kubernetes.github.io/ingress-nginx",
					Chart:          "ingress-nginx",
					TargetRevision: "4.13.0",
				},
			}},
		},
	}
	baseline := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-baseline", Namespace: "astronomer-mgmt", Generation: 2},
		Spec: ClusterBaselineSpec{
			ClusterSelector: LabelSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			Bundles:         []ClusterBaselineBundleRef{{Name: "ingress", Version: "2.0.0"}},
		},
	}
	c := newValidationClient(t, bundle, baseline)
	r := &ClusterBaselineReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile baseline: %v", err)
	}
	var after ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}, &after); err != nil {
		t.Fatalf("Get baseline: %v", err)
	}
	if after.Status.Phase != "Ready" {
		t.Fatalf("expected Ready status for versioned catalog ref, got %+v", after.Status)
	}
	var appSet unstructured.Unstructured
	appSet.SetGroupVersionKind(applicationSetGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: after.Status.Applications[0].Name, Namespace: defaultArgoNamespace}, &appSet); err != nil {
		t.Fatalf("Get generated ApplicationSet: %v", err)
	}
	chart, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "chart")
	if chart != "ingress-nginx" {
		t.Fatalf("generated chart = %q", chart)
	}
	revision, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "targetRevision")
	if revision != "4.13.0" {
		t.Fatalf("generated targetRevision = %q", revision)
	}
	namespace, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "destination", "namespace")
	if namespace != "ingress-v2" {
		t.Fatalf("generated destination namespace = %q", namespace)
	}
}

func TestClusterBaselineReconciler_ValidatesBundleValuesAgainstSchema(t *testing.T) {
	schema := testConfigMapData("ingress-values", "astronomer-mgmt", map[string]string{
		componentBundleValuesSchemaDefaultKey: `{
			"type": "object",
			"properties": {
				"controller": {
					"type": "object",
					"properties": {
						"metrics": {
							"type": "object",
							"properties": {
								"enabled": {"type": "boolean"}
							}
						}
					}
				}
			}
		}`,
	})
	bundle := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "ingress", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version:          "1.0.0",
			DefaultNamespace: "ingress-nginx",
			Source: ComponentBundleSourceSpec{
				Type:            "helm",
				RepoURL:         "https://kubernetes.github.io/ingress-nginx",
				Chart:           "ingress-nginx",
				TargetRevision:  "4.12.0",
				ValuesSchemaRef: "ingress-values",
			},
		},
	}
	valid := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "valid-values", Namespace: "astronomer-mgmt"},
		Spec: ClusterBaselineSpec{
			ClusterSelector: LabelSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			Bundles: []ClusterBaselineBundleRef{{
				Name:    "ingress",
				Version: "1.0.0",
				Values:  map[string]string{"controller.metrics.enabled": "true"},
			}},
		},
	}
	invalid := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "invalid-values", Namespace: "astronomer-mgmt"},
		Spec: ClusterBaselineSpec{
			ClusterSelector: LabelSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			Bundles: []ClusterBaselineBundleRef{{
				Name:    "ingress",
				Version: "1.0.0",
				Values:  map[string]string{"controller.metrics.enabled": "not-bool"},
			}},
		},
	}
	c := newValidationClient(t, schema, bundle, valid, invalid)
	r := &ClusterBaselineReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "valid-values", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile valid values: %v", err)
	}
	var validAfter ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "valid-values", Namespace: "astronomer-mgmt"}, &validAfter); err != nil {
		t.Fatalf("Get valid values: %v", err)
	}
	if validAfter.Status.Phase != "Ready" {
		t.Fatalf("expected Ready phase, got %+v", validAfter.Status)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "invalid-values", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile invalid values: %v", err)
	}
	var invalidAfter ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "invalid-values", Namespace: "astronomer-mgmt"}, &invalidAfter); err != nil {
		t.Fatalf("Get invalid values: %v", err)
	}
	if invalidAfter.Status.Phase != "Degraded" {
		t.Fatalf("expected Degraded phase for invalid values, got %+v", invalidAfter.Status)
	}
}

func TestClusterBaselineReconciler_ValidatesValuesFromSources(t *testing.T) {
	baseline := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "invalid-values-from", Namespace: "astronomer-mgmt"},
		Spec: ClusterBaselineSpec{
			ClusterSelector: LabelSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			Bundles: []ClusterBaselineBundleRef{{
				Name: "ingress",
				ValuesFrom: []ClusterBaselineValuesSource{
					{Type: "git", Path: "../secret-values.yaml"},
					{Type: "secret", Path: "values/prod.yaml"},
					{Type: "configMap"},
					{Type: "inline", Name: "not-supported"},
				},
			}},
		},
	}
	c := newValidationClient(t, baseline)
	r := &ClusterBaselineReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "invalid-values-from", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile invalid valuesFrom: %v", err)
	}
	var after ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "invalid-values-from", Namespace: "astronomer-mgmt"}, &after); err != nil {
		t.Fatalf("Get invalid valuesFrom: %v", err)
	}
	if after.Status.Phase != "Degraded" {
		t.Fatalf("expected Degraded phase for invalid valuesFrom, got %+v", after.Status)
	}
	if len(after.Status.Conditions) == 0 || !strings.Contains(after.Status.Conditions[0].Message, "valuesFrom") {
		t.Fatalf("expected valuesFrom validation condition, got %+v", after.Status.Conditions)
	}
}

func TestClusterBaselineReconciler_DeletesGeneratedApplicationSetsBeforeRemovingFinalizer(t *testing.T) {
	now := metav1.Now()
	baseline := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "prod-baseline",
			Namespace:         "astronomer-mgmt",
			Finalizers:        []string{FinalizerClusterBaseline},
			DeletionTimestamp: &now,
		},
		Spec: ClusterBaselineSpec{ClusterSelector: LabelSelectorSpec{ClusterRefs: []string{"prod-us-east"}}},
	}
	appSet := testApplicationSet("astronomer-baseline-prod", defaultArgoNamespace, crdSourceLabels("ClusterBaseline", "astronomer-mgmt", "prod-baseline"))
	c := newValidationClient(t, baseline, appSet)
	r := &ClusterBaselineReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile deleting baseline: %v", err)
	}
	var afterAppSet unstructured.Unstructured
	afterAppSet.SetGroupVersionKind(applicationSetGVK)
	err := c.Get(context.Background(), types.NamespacedName{Name: appSet.GetName(), Namespace: appSet.GetNamespace()}, &afterAppSet)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("generated ApplicationSet still exists or unexpected error: %v", err)
	}
	var afterBaseline ClusterBaseline
	if err := c.Get(context.Background(), types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}, &afterBaseline); err == nil && hasFinalizer(afterBaseline.Finalizers, FinalizerClusterBaseline) {
		t.Fatalf("ClusterBaseline finalizer still present after child cleanup")
	}
}

func TestComponentBundleReconciler_ValidatesSourceShape(t *testing.T) {
	valid := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx", Namespace: "astronomer-mgmt", Generation: 2},
		Spec: ComponentBundleSpec{
			Version:          "1.0.0",
			DefaultNamespace: "ingress-nginx",
			Source: ComponentBundleSourceSpec{
				Type:            "helm",
				RepoURL:         "https://kubernetes.github.io/ingress-nginx",
				Chart:           "ingress-nginx",
				TargetRevision:  "4.12.0",
				ValuesSchemaRef: "ingress-values/schema.json",
				SecretRefs:      []ComponentBundleSecretRef{{Name: "repo-creds", Key: "token"}},
			},
			CapabilityRequirements: []ComponentBundleRequirement{{Feature: "ingress"}},
			HealthChecks:           []ComponentBundleHealthCheck{{Type: "argocd"}},
			Versions: []ComponentBundleVersionSpec{{
				Version:          "1.1.0",
				DefaultNamespace: "ingress-nginx-v2",
				Source: ComponentBundleSourceSpec{
					Type:            "helm",
					RepoURL:         "https://kubernetes.github.io/ingress-nginx",
					Chart:           "ingress-nginx",
					TargetRevision:  "4.13.0",
					ValuesSchemaRef: "ingress-values/v2-schema.json",
					SecretRefs:      []ComponentBundleSecretRef{{Name: "repo-creds-v2", Key: "token"}},
				},
				CapabilityRequirements: []ComponentBundleRequirement{{Feature: "ingress"}},
				HealthChecks:           []ComponentBundleHealthCheck{{Type: "argocd"}},
			}},
		},
	}
	invalid := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "broken", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version: "1.0.0",
			Source:  ComponentBundleSourceSpec{Type: "helm", RepoURL: "https://example.com/charts"},
		},
	}
	invalidRefs := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-refs", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version: "1.0.0",
			Source: ComponentBundleSourceSpec{
				Type:            "helm",
				RepoURL:         "https://example.com/charts",
				Chart:           "example",
				ValuesSchemaRef: "https://schemas.example.test/bundle.json",
				SecretRefs:      []ComponentBundleSecretRef{{Name: "repo-creds", Namespace: "other", Key: "bad/key"}},
			},
		},
	}
	invalidCatalog := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-catalog", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version: "1.0.0",
			Source: ComponentBundleSourceSpec{
				Type:    "helm",
				RepoURL: "https://example.com/charts",
				Chart:   "example",
			},
			Versions: []ComponentBundleVersionSpec{{
				Version: "1.0.0",
				Source: ComponentBundleSourceSpec{
					Type:    "helm",
					RepoURL: "https://example.com/charts",
					Chart:   "example-v2",
				},
			}},
		},
	}
	c := newValidationClient(t, valid, invalid, invalidRefs, invalidCatalog)
	r := &ComponentBundleReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "nginx", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile valid: %v", err)
	}
	var validAfter ComponentBundle
	if err := c.Get(context.Background(), types.NamespacedName{Name: "nginx", Namespace: "astronomer-mgmt"}, &validAfter); err != nil {
		t.Fatalf("Get valid: %v", err)
	}
	if validAfter.Status.Phase != "Valid" {
		t.Fatalf("expected Valid phase, got %+v", validAfter.Status)
	}
	if validAfter.Status.ResolvedRevision != "4.12.0" {
		t.Fatalf("resolved revision not echoed: %+v", validAfter.Status)
	}
	if got := validAfter.Status.AvailableVersions; len(got) != 2 || got[0] != "1.0.0" || got[1] != "1.1.0" {
		t.Fatalf("available versions not patched: %+v", validAfter.Status)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "broken", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile invalid: %v", err)
	}
	var invalidAfter ComponentBundle
	if err := c.Get(context.Background(), types.NamespacedName{Name: "broken", Namespace: "astronomer-mgmt"}, &invalidAfter); err != nil {
		t.Fatalf("Get invalid: %v", err)
	}
	if invalidAfter.Status.Phase != "Invalid" {
		t.Fatalf("expected Invalid phase, got %+v", invalidAfter.Status)
	}
	if conditionStatus(invalidAfter.Status.Conditions, "Accepted") != metav1.ConditionFalse {
		t.Fatalf("Accepted condition not false: %+v", invalidAfter.Status.Conditions)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "bad-refs", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile invalid refs: %v", err)
	}
	var invalidRefsAfter ComponentBundle
	if err := c.Get(context.Background(), types.NamespacedName{Name: "bad-refs", Namespace: "astronomer-mgmt"}, &invalidRefsAfter); err != nil {
		t.Fatalf("Get invalid refs: %v", err)
	}
	if invalidRefsAfter.Status.Phase != "Invalid" {
		t.Fatalf("expected Invalid phase for refs, got %+v", invalidRefsAfter.Status)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "bad-catalog", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile invalid catalog: %v", err)
	}
	var invalidCatalogAfter ComponentBundle
	if err := c.Get(context.Background(), types.NamespacedName{Name: "bad-catalog", Namespace: "astronomer-mgmt"}, &invalidCatalogAfter); err != nil {
		t.Fatalf("Get invalid catalog: %v", err)
	}
	if invalidCatalogAfter.Status.Phase != "Invalid" {
		t.Fatalf("expected Invalid phase for duplicate version catalog, got %+v", invalidCatalogAfter.Status)
	}
}

func TestAgentProfileReconciler_ValidatesNamespaceScopeAndEffectiveRBAC(t *testing.T) {
	valid := &AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "team-operator", Namespace: "astronomer-mgmt"},
		Spec: AgentProfileSpec{
			PrivilegeProfile: "namespace-operator",
			NamespaceScope:   []string{"platform", "observability"},
			Capabilities:     map[string]bool{"exec": true, "secrets": false},
			AllowedRules: []AgentProfilePolicyRule{{
				Resources: []string{"pods", "deployments"},
				Verbs:     []string{"get", "list", "watch"},
			}},
			NetworkEgress: AgentProfileNetworkEgressSpec{Mode: "restricted"},
		},
	}
	invalid := &AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "missing-scope", Namespace: "astronomer-mgmt"},
		Spec:       AgentProfileSpec{PrivilegeProfile: "namespace-operator"},
	}
	hostAccessInvalid := &AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "host-access", Namespace: "astronomer-mgmt"},
		Spec: AgentProfileSpec{
			PrivilegeProfile: "operator",
			HostAccess:       AgentProfileHostAccessSpec{HostNetwork: true},
		},
	}
	installInvalid := &AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-install", Namespace: "astronomer-mgmt"},
		Spec: AgentProfileSpec{
			PrivilegeProfile: "operator",
			Install: AgentProfileInstallSpec{
				Image:              "registry.example.com/agent:v1\nbad",
				ServiceAccountName: "Bad_Name",
				PodLabels:          map[string]string{"bad key": "value"},
			},
		},
	}
	capabilityInvalid := &AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-capability", Namespace: "astronomer-mgmt"},
		Spec: AgentProfileSpec{
			PrivilegeProfile: "viewer",
			Capabilities:     map[string]bool{"exec": true},
		},
	}
	ruleInvalid := &AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-rule", Namespace: "astronomer-mgmt"},
		Spec: AgentProfileSpec{
			PrivilegeProfile: "namespace-operator",
			NamespaceScope:   []string{"platform"},
			AllowedRules: []AgentProfilePolicyRule{{
				Resources: []string{"secrets"},
				Verbs:     []string{"get"},
			}},
		},
	}
	customRuleValid := &AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "custom-exec", Namespace: "astronomer-mgmt"},
		Spec: AgentProfileSpec{
			PrivilegeProfile: "custom",
			Capabilities:     map[string]bool{"exec": true, "custom_rbac": true},
			AllowedRules: []AgentProfilePolicyRule{{
				Resources: []string{"pods/exec"},
				Verbs:     []string{"create"},
			}},
		},
	}
	c := newValidationClient(t, valid, invalid, hostAccessInvalid, installInvalid, capabilityInvalid, ruleInvalid, customRuleValid)
	r := &AgentProfileReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "team-operator", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile valid: %v", err)
	}
	var validAfter AgentProfile
	if err := c.Get(context.Background(), types.NamespacedName{Name: "team-operator", Namespace: "astronomer-mgmt"}, &validAfter); err != nil {
		t.Fatalf("Get valid: %v", err)
	}
	if validAfter.Status.Phase != "Ready" {
		t.Fatalf("expected Ready phase, got %+v", validAfter.Status)
	}
	if !containsString(validAfter.Status.EffectiveRBAC, "namespaces:observability,platform") {
		t.Fatalf("effective RBAC does not include sorted namespace scope: %+v", validAfter.Status.EffectiveRBAC)
	}
	if !containsString(validAfter.Status.EffectiveRBAC, "capability:exec=true") || !containsString(validAfter.Status.EffectiveRBAC, "capability:secrets=false") {
		t.Fatalf("effective RBAC does not include capabilities: %+v", validAfter.Status.EffectiveRBAC)
	}
	if !containsString(validAfter.Status.EffectiveRBAC, "custom-rules:1") || !containsString(validAfter.Status.EffectiveRBAC, "egress:restricted") {
		t.Fatalf("effective RBAC does not include custom rules and egress: %+v", validAfter.Status.EffectiveRBAC)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "missing-scope", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile invalid: %v", err)
	}
	var invalidAfter AgentProfile
	if err := c.Get(context.Background(), types.NamespacedName{Name: "missing-scope", Namespace: "astronomer-mgmt"}, &invalidAfter); err != nil {
		t.Fatalf("Get invalid: %v", err)
	}
	if invalidAfter.Status.Phase != "Invalid" {
		t.Fatalf("expected Invalid phase, got %+v", invalidAfter.Status)
	}
	if conditionStatus(invalidAfter.Status.Conditions, "Accepted") != metav1.ConditionFalse {
		t.Fatalf("Accepted condition not false: %+v", invalidAfter.Status.Conditions)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "host-access", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile host-access invalid: %v", err)
	}
	var hostAccessAfter AgentProfile
	if err := c.Get(context.Background(), types.NamespacedName{Name: "host-access", Namespace: "astronomer-mgmt"}, &hostAccessAfter); err != nil {
		t.Fatalf("Get host-access invalid: %v", err)
	}
	if hostAccessAfter.Status.Phase != "Invalid" {
		t.Fatalf("expected Invalid phase for host access, got %+v", hostAccessAfter.Status)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "bad-install", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile install invalid: %v", err)
	}
	var installAfter AgentProfile
	if err := c.Get(context.Background(), types.NamespacedName{Name: "bad-install", Namespace: "astronomer-mgmt"}, &installAfter); err != nil {
		t.Fatalf("Get install invalid: %v", err)
	}
	if installAfter.Status.Phase != "Invalid" {
		t.Fatalf("expected Invalid phase for install metadata, got %+v", installAfter.Status)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "bad-capability", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile capability invalid: %v", err)
	}
	var capabilityAfter AgentProfile
	if err := c.Get(context.Background(), types.NamespacedName{Name: "bad-capability", Namespace: "astronomer-mgmt"}, &capabilityAfter); err != nil {
		t.Fatalf("Get capability invalid: %v", err)
	}
	if capabilityAfter.Status.Phase != "Invalid" {
		t.Fatalf("expected Invalid phase for denied capability, got %+v", capabilityAfter.Status)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "bad-rule", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile rule invalid: %v", err)
	}
	var ruleAfter AgentProfile
	if err := c.Get(context.Background(), types.NamespacedName{Name: "bad-rule", Namespace: "astronomer-mgmt"}, &ruleAfter); err != nil {
		t.Fatalf("Get rule invalid: %v", err)
	}
	if ruleAfter.Status.Phase != "Invalid" {
		t.Fatalf("expected Invalid phase for denied rule, got %+v", ruleAfter.Status)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "custom-exec", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile custom rule valid: %v", err)
	}
	var customAfter AgentProfile
	if err := c.Get(context.Background(), types.NamespacedName{Name: "custom-exec", Namespace: "astronomer-mgmt"}, &customAfter); err != nil {
		t.Fatalf("Get custom rule valid: %v", err)
	}
	if customAfter.Status.Phase != "Ready" {
		t.Fatalf("expected Ready phase for custom rule-backed capability, got %+v", customAfter.Status)
	}
}

func TestGitOpsTargetReconciler_EnforcesAstronomerManagedSelector(t *testing.T) {
	project := &Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform",
			Namespace: "astronomer-mgmt",
			Labels:    map[string]string{"team": "platform"},
		},
		Spec: ProjectSpec{Name: "platform", Clusters: []string{"prod-us-east"}},
	}
	bundle := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "observability", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version:          "1.0.0",
			DefaultNamespace: "monitoring",
			Source: ComponentBundleSourceSpec{
				Type:           "helm",
				RepoURL:        "https://prometheus-community.github.io/helm-charts",
				Chart:          "kube-prometheus-stack",
				TargetRevision: "65.0.0",
			},
		},
	}
	valid := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "core-platform", Namespace: "astronomer-mgmt"},
		Spec: GitOpsTargetSpec{
			Selector: GitOpsTargetSelectorSpec{
				MatchLabels: map[string]string{"astronomer.io/managed-by": "astronomer", "tier": "prod"},
				ClusterRefs: []string{"prod-us-east"},
			},
			ProjectSelector: LabelSelectorSpec{MatchLabels: map[string]string{"team": "platform"}},
			BundleRef:       GitOpsTargetBundleRef{Name: "observability", Version: "1.0.0"},
			ApplicationSet:  GitOpsTargetApplicationSetSpec{TemplateRef: "cluster-baseline"},
			SyncPolicy:      GitOpsTargetSyncPolicy{Automated: true, Prune: true, SelfHeal: true},
			SyncWindows:     []GitOpsTargetSyncWindowSpec{{Kind: "allow", Schedule: "0 1 * * *", Duration: "2h"}},
		},
	}
	templateOnly := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "template-only", Namespace: "astronomer-mgmt"},
		Spec: GitOpsTargetSpec{
			Selector:       GitOpsTargetSelectorSpec{MatchLabels: map[string]string{"astronomer.io/managed-by": "astronomer"}},
			ApplicationSet: GitOpsTargetApplicationSetSpec{TemplateRef: "future-template"},
		},
	}
	crossTenant := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-tenant", Namespace: "astronomer-mgmt"},
		Spec: GitOpsTargetSpec{
			Selector: GitOpsTargetSelectorSpec{
				ClusterRefs: []string{"prod-west"},
			},
			ProjectSelector: LabelSelectorSpec{MatchLabels: map[string]string{"team": "platform"}},
			ApplicationSet:  GitOpsTargetApplicationSetSpec{SourceRepo: "https://github.com/example/platform.git", Path: "clusters/prod"},
		},
	}
	labelOnlyTenant := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "label-only-tenant", Namespace: "astronomer-mgmt"},
		Spec: GitOpsTargetSpec{
			Selector: GitOpsTargetSelectorSpec{
				MatchLabels: map[string]string{"astronomer.io/managed-by": "astronomer", "tier": "prod"},
			},
			ProjectSelector: LabelSelectorSpec{MatchLabels: map[string]string{"team": "platform"}},
			ApplicationSet:  GitOpsTargetApplicationSetSpec{SourceRepo: "https://github.com/example/platform.git", Path: "clusters/prod"},
		},
	}
	labelOnlyPlatform := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "label-only-platform", Namespace: "astronomer-mgmt"},
		Spec: GitOpsTargetSpec{
			Selector: GitOpsTargetSelectorSpec{
				MatchLabels: map[string]string{
					"astronomer.io/managed-by":       "astronomer",
					"astronomer.io/project.platform": "true",
				},
			},
			ProjectSelector: LabelSelectorSpec{MatchLabels: map[string]string{"team": "platform"}},
			ApplicationSet:  GitOpsTargetApplicationSetSpec{SourceRepo: "https://github.com/example/platform.git", Path: "clusters/prod"},
		},
	}
	invalid := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "unsafe-selector", Namespace: "astronomer-mgmt"},
		Spec: GitOpsTargetSpec{
			Selector:       GitOpsTargetSelectorSpec{MatchLabels: map[string]string{"tier": "prod"}},
			ApplicationSet: GitOpsTargetApplicationSetSpec{SourceRepo: "https://github.com/example/platform.git", Path: "clusters/prod"},
		},
	}
	childApp := testApplication("astro-core-platform-prod-us-east", defaultArgoNamespace, crdSourceLabels("GitOpsTarget", "astronomer-mgmt", "core-platform"), "Synced", "Healthy")
	c := newValidationClient(t, project, bundle, valid, templateOnly, crossTenant, labelOnlyTenant, labelOnlyPlatform, invalid, childApp)
	r := &GitOpsTargetReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "core-platform", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile valid: %v", err)
	}
	var validAfter GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "core-platform", Namespace: "astronomer-mgmt"}, &validAfter); err != nil {
		t.Fatalf("Get valid: %v", err)
	}
	if validAfter.Status.Phase != "Ready" {
		t.Fatalf("expected Ready phase, got %+v", validAfter.Status)
	}
	if validAfter.Status.ApplicationSetName != "astronomer-gitops-astronomer-mgmt-core-platform" {
		t.Fatalf("application set name not echoed: %+v", validAfter.Status)
	}
	if validAfter.Status.SyncStatus != "Synced" || validAfter.Status.Health != "Healthy" || validAfter.Status.ApplicationCount != 1 {
		t.Fatalf("application rollup not patched: %+v", validAfter.Status)
	}
	if len(validAfter.Status.Applications) != 1 {
		t.Fatalf("expected child Application details, got %+v", validAfter.Status.Applications)
	}
	if validAfter.Status.Applications[0].Name != "astro-core-platform-prod-us-east" || len(validAfter.Status.Applications[0].Resources) != 1 || validAfter.Status.Applications[0].Resources[0].Kind != "Deployment" {
		t.Fatalf("child Application details not patched: %+v", validAfter.Status.Applications[0])
	}
	var appSet unstructured.Unstructured
	appSet.SetGroupVersionKind(applicationSetGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: validAfter.Status.ApplicationSetName, Namespace: defaultArgoNamespace}, &appSet); err != nil {
		t.Fatalf("Get generated ApplicationSet: %v", err)
	}
	if appSet.GetAnnotations()[applicationSetSpecHashAnnotation] == "" {
		t.Fatalf("generated ApplicationSet missing desired spec hash annotation: %+v", appSet.GetAnnotations())
	}
	generators, found, err := unstructured.NestedSlice(appSet.Object, "spec", "generators")
	if err != nil || !found || len(generators) != 1 {
		t.Fatalf("unexpected generated generators found=%v len=%d err=%v", found, len(generators), err)
	}
	generator := generators[0].(map[string]any)
	clusters := generator["clusters"].(map[string]any)
	selector := clusters["selector"].(map[string]any)
	matchLabels := selector["matchLabels"].(map[string]any)
	if matchLabels["astronomer.io/managed-by"] != "astronomer" || matchLabels["tier"] != "prod" {
		t.Fatalf("unexpected generated selector labels: %+v", matchLabels)
	}
	repo, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "repoURL")
	if repo != "https://prometheus-community.github.io/helm-charts" {
		t.Fatalf("generated repoURL = %q", repo)
	}
	chart, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "chart")
	if chart != "kube-prometheus-stack" {
		t.Fatalf("generated chart = %q", chart)
	}
	revision, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "targetRevision")
	if revision != "65.0.0" {
		t.Fatalf("generated targetRevision = %q", revision)
	}
	namespace, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "destination", "namespace")
	if namespace != "monitoring" {
		t.Fatalf("generated destination namespace = %q", namespace)
	}
	prune, _, _ := unstructured.NestedBool(appSet.Object, "spec", "template", "spec", "syncPolicy", "automated", "prune")
	selfHeal, _, _ := unstructured.NestedBool(appSet.Object, "spec", "template", "spec", "syncPolicy", "automated", "selfHeal")
	if !prune || !selfHeal {
		t.Fatalf("generated automated sync policy prune=%v selfHeal=%v", prune, selfHeal)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "template-only", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile template-only: %v", err)
	}
	var templateOnlyAfter GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "template-only", Namespace: "astronomer-mgmt"}, &templateOnlyAfter); err != nil {
		t.Fatalf("Get template-only: %v", err)
	}
	if templateOnlyAfter.Status.Phase != "Degraded" {
		t.Fatalf("expected template-only target to be Degraded until template catalog exists, got %+v", templateOnlyAfter.Status)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "unsafe-selector", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile invalid: %v", err)
	}
	var invalidAfter GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "unsafe-selector", Namespace: "astronomer-mgmt"}, &invalidAfter); err != nil {
		t.Fatalf("Get invalid: %v", err)
	}
	if invalidAfter.Status.Phase != "Degraded" {
		t.Fatalf("expected Degraded phase, got %+v", invalidAfter.Status)
	}
	if conditionStatus(invalidAfter.Status.Conditions, "Accepted") != metav1.ConditionFalse {
		t.Fatalf("Accepted condition not false: %+v", invalidAfter.Status.Conditions)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "cross-tenant", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile cross-tenant: %v", err)
	}
	var crossTenantAfter GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "cross-tenant", Namespace: "astronomer-mgmt"}, &crossTenantAfter); err != nil {
		t.Fatalf("Get cross-tenant: %v", err)
	}
	if crossTenantAfter.Status.Phase != "Degraded" {
		t.Fatalf("expected cross-tenant target to degrade, got %+v", crossTenantAfter.Status)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "label-only-tenant", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile label-only tenant: %v", err)
	}
	var labelOnlyAfter GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "label-only-tenant", Namespace: "astronomer-mgmt"}, &labelOnlyAfter); err != nil {
		t.Fatalf("Get label-only tenant: %v", err)
	}
	if labelOnlyAfter.Status.Phase != "Degraded" {
		t.Fatalf("expected label-only tenant target to degrade without durable project label, got %+v", labelOnlyAfter.Status)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "label-only-platform", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile label-only platform: %v", err)
	}
	var labelOnlyPlatformAfter GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "label-only-platform", Namespace: "astronomer-mgmt"}, &labelOnlyPlatformAfter); err != nil {
		t.Fatalf("Get label-only platform: %v", err)
	}
	if labelOnlyPlatformAfter.Status.Phase != "Ready" {
		t.Fatalf("expected label-only platform target to be Ready with durable membership label, got %+v", labelOnlyPlatformAfter.Status)
	}
}

func TestGitOpsTargetReconciler_ResolvesVersionedComponentBundleCatalog(t *testing.T) {
	bundle := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "observability", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version:          "1.0.0",
			DefaultNamespace: "monitoring-legacy",
			Source: ComponentBundleSourceSpec{
				Type:           "helm",
				RepoURL:        "https://prometheus-community.github.io/helm-charts",
				Chart:          "kube-prometheus-stack-legacy",
				TargetRevision: "64.0.0",
			},
			Versions: []ComponentBundleVersionSpec{{
				Version:          "2.0.0",
				DefaultNamespace: "monitoring-v2",
				Source: ComponentBundleSourceSpec{
					Type:           "helm",
					RepoURL:        "https://prometheus-community.github.io/helm-charts",
					Chart:          "kube-prometheus-stack",
					TargetRevision: "65.0.0",
				},
			}},
		},
	}
	target := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "observability-target", Namespace: "astronomer-mgmt", Generation: 2},
		Spec: GitOpsTargetSpec{
			Selector:  GitOpsTargetSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			BundleRef: GitOpsTargetBundleRef{Name: "observability", Version: "2.0.0"},
		},
	}
	c := newValidationClient(t, bundle, target)
	r := &GitOpsTargetReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "observability-target", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile target: %v", err)
	}
	var after GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "observability-target", Namespace: "astronomer-mgmt"}, &after); err != nil {
		t.Fatalf("Get target: %v", err)
	}
	if after.Status.Phase != "Ready" {
		t.Fatalf("expected Ready status for versioned catalog ref, got %+v", after.Status)
	}
	var appSet unstructured.Unstructured
	appSet.SetGroupVersionKind(applicationSetGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: after.Status.ApplicationSetName, Namespace: defaultArgoNamespace}, &appSet); err != nil {
		t.Fatalf("Get generated ApplicationSet: %v", err)
	}
	chart, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "chart")
	if chart != "kube-prometheus-stack" {
		t.Fatalf("generated chart = %q", chart)
	}
	revision, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "targetRevision")
	if revision != "65.0.0" {
		t.Fatalf("generated targetRevision = %q", revision)
	}
	namespace, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "destination", "namespace")
	if namespace != "monitoring-v2" {
		t.Fatalf("generated destination namespace = %q", namespace)
	}
}

func TestGitOpsTargetReconciler_ResolvesTemplateRefConfigMap(t *testing.T) {
	template := testGitOpsTemplateConfigMap(
		"platform-template",
		"astronomer-mgmt",
		`{
			"project": "platform",
			"destinationNamespace": "platform-system",
			"source": {
				"type": "git-path",
				"repoURL": "https://github.com/example/platform.git",
				"path": "apps/platform",
				"targetRevision": "main"
			}
		}`,
		map[string]string{gitOpsTemplateLabelKey: gitOpsTemplateLabelValue},
	)
	target := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-template-target", Namespace: "astronomer-mgmt", Generation: 2},
		Spec: GitOpsTargetSpec{
			Selector: GitOpsTargetSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			ApplicationSet: GitOpsTargetApplicationSetSpec{
				TemplateRef: "platform-template",
				Parameters:  map[string]string{"namespace": "platform-override"},
			},
		},
	}
	childApp := testApplication("astro-platform-template-prod-us-east", defaultArgoNamespace, crdSourceLabels("GitOpsTarget", "astronomer-mgmt", "platform-template-target"), "Synced", "Healthy")
	c := newValidationClient(t, template, target, childApp)
	r := &GitOpsTargetReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "platform-template-target", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile target: %v", err)
	}
	var after GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "platform-template-target", Namespace: "astronomer-mgmt"}, &after); err != nil {
		t.Fatalf("Get target: %v", err)
	}
	if after.Status.Phase != "Ready" || after.Status.SyncStatus != "Synced" || after.Status.Health != "Healthy" || after.Status.ApplicationCount != 1 {
		t.Fatalf("unexpected template target status: %+v", after.Status)
	}
	var appSet unstructured.Unstructured
	appSet.SetGroupVersionKind(applicationSetGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: after.Status.ApplicationSetName, Namespace: defaultArgoNamespace}, &appSet); err != nil {
		t.Fatalf("Get generated ApplicationSet: %v", err)
	}
	repo, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "repoURL")
	path, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "path")
	revision, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "targetRevision")
	project, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "project")
	namespace, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "destination", "namespace")
	if repo != "https://github.com/example/platform.git" || path != "apps/platform" || revision != "main" {
		t.Fatalf("unexpected source repo=%q path=%q revision=%q", repo, path, revision)
	}
	if project != "platform" {
		t.Fatalf("generated project = %q", project)
	}
	if namespace != "platform-override" {
		t.Fatalf("generated namespace = %q", namespace)
	}
}

func TestGitOpsTargetReconciler_RepairsDriftedOwnedApplicationSet(t *testing.T) {
	target := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "drifted-target", Namespace: "astronomer-mgmt", Generation: 3},
		Spec: GitOpsTargetSpec{
			Selector: GitOpsTargetSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			ApplicationSet: GitOpsTargetApplicationSetSpec{
				SourceRepo: "https://github.com/example/platform.git",
				Path:       "clusters/prod",
				Revision:   "main",
			},
		},
	}
	existing := testApplicationSet(gitOpsTargetApplicationSetName(*target), defaultArgoNamespace, crdSourceLabels("GitOpsTarget", "astronomer-mgmt", "drifted-target"))
	existing.SetAnnotations(mapStringAnyToString(crdSourceAnnotations("GitOpsTarget", "astronomer-mgmt", "drifted-target")))
	existing.Object["spec"] = map[string]any{
		"template": map[string]any{
			"spec": map[string]any{
				"source": map[string]any{
					"repoURL": "https://github.com/example/old.git",
					"path":    "old",
				},
			},
		},
	}
	c := newValidationClient(t, target, existing)
	r := &GitOpsTargetReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "drifted-target", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile drifted target: %v", err)
	}
	var after GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "drifted-target", Namespace: "astronomer-mgmt"}, &after); err != nil {
		t.Fatalf("Get drifted target: %v", err)
	}
	if reason := conditionReason(after.Status.Conditions, "Ready"); reason != "ApplicationSetDriftRepaired" {
		t.Fatalf("Ready reason = %q, want ApplicationSetDriftRepaired: %+v", reason, after.Status.Conditions)
	}
	var appSet unstructured.Unstructured
	appSet.SetGroupVersionKind(applicationSetGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: gitOpsTargetApplicationSetName(*target), Namespace: defaultArgoNamespace}, &appSet); err != nil {
		t.Fatalf("Get repaired ApplicationSet: %v", err)
	}
	repo, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "repoURL")
	path, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "path")
	if repo != "https://github.com/example/platform.git" || path != "clusters/prod" {
		t.Fatalf("ApplicationSet drift not repaired repo=%q path=%q", repo, path)
	}
	if appSet.GetAnnotations()[applicationSetSpecHashAnnotation] == "" {
		t.Fatalf("repaired ApplicationSet missing desired spec hash annotation: %+v", appSet.GetAnnotations())
	}
}

func TestGitOpsTargetReconciler_DeletesGeneratedApplicationSetBeforeRemovingFinalizer(t *testing.T) {
	now := metav1.Now()
	target := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "core-platform",
			Namespace:         "astronomer-mgmt",
			Finalizers:        []string{FinalizerGitOpsTarget},
			DeletionTimestamp: &now,
		},
		Spec: GitOpsTargetSpec{
			Selector:       GitOpsTargetSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			ApplicationSet: GitOpsTargetApplicationSetSpec{SourceRepo: "https://github.com/example/platform.git", Path: "clusters/prod"},
		},
	}
	appSet := &unstructured.Unstructured{}
	appSet.SetGroupVersionKind(applicationSetGVK)
	appSet.SetName(gitOpsTargetApplicationSetName(*target))
	appSet.SetNamespace(defaultArgoNamespace)
	c := newValidationClient(t, target, appSet)
	r := &GitOpsTargetReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "core-platform", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile deleting target: %v", err)
	}
	var afterAppSet unstructured.Unstructured
	afterAppSet.SetGroupVersionKind(applicationSetGVK)
	err := c.Get(context.Background(), types.NamespacedName{Name: appSet.GetName(), Namespace: defaultArgoNamespace}, &afterAppSet)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("generated ApplicationSet still exists or unexpected error: %v", err)
	}
	var afterTarget GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "core-platform", Namespace: "astronomer-mgmt"}, &afterTarget); err == nil && hasFinalizer(afterTarget.Finalizers, FinalizerGitOpsTarget) {
		t.Fatalf("GitOpsTarget finalizer still present after child cleanup")
	}
}

func TestGitOpsTargetReconciler_FinalizerTimeoutStatusOnCleanupFailure(t *testing.T) {
	old := metav1.NewTime(time.Now().Add(-finalizerTimeout - time.Minute))
	target := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "core-platform",
			Namespace:         "astronomer-mgmt",
			Finalizers:        []string{FinalizerGitOpsTarget},
			DeletionTimestamp: &old,
			Generation:        4,
		},
		Spec: GitOpsTargetSpec{
			Selector:       GitOpsTargetSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			ApplicationSet: GitOpsTargetApplicationSetSpec{SourceRepo: "https://github.com/example/platform.git", Path: "clusters/prod"},
		},
	}
	appSet := testApplicationSet(gitOpsTargetApplicationSetName(*target), defaultArgoNamespace, crdSourceLabels("GitOpsTarget", "astronomer-mgmt", "core-platform"))
	c := newValidationClientWithInterceptors(t, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if obj.GetObjectKind().GroupVersionKind() == applicationSetGVK {
				return errors.New("blocked ApplicationSet delete")
			}
			return c.Delete(ctx, obj, opts...)
		},
	}, target, appSet)
	r := &GitOpsTargetReconciler{Client: c, Log: slog.Default()}

	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "core-platform", Namespace: "astronomer-mgmt"}})
	if err == nil {
		t.Fatalf("expected cleanup failure")
	}
	var after GitOpsTarget
	if getErr := c.Get(context.Background(), types.NamespacedName{Name: "core-platform", Namespace: "astronomer-mgmt"}, &after); getErr != nil {
		t.Fatalf("Get target after timeout: %v", getErr)
	}
	if after.Status.Phase != "DeletingTimedOut" {
		t.Fatalf("expected DeletingTimedOut phase, got %+v", after.Status)
	}
	if conditionReason(after.Status.Conditions, "Ready") != "FinalizerTimeout" {
		t.Fatalf("Ready condition did not surface finalizer timeout: %+v", after.Status.Conditions)
	}
	if !hasFinalizer(after.Finalizers, FinalizerGitOpsTarget) {
		t.Fatalf("finalizer removed despite failed ApplicationSet cleanup")
	}
}

func TestGitOpsTargetReconciler_RefusesToOverwriteUnownedApplicationSet(t *testing.T) {
	target := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "core-platform", Namespace: "astronomer-mgmt", Generation: 2},
		Spec: GitOpsTargetSpec{
			Selector:       GitOpsTargetSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			ApplicationSet: GitOpsTargetApplicationSetSpec{SourceRepo: "https://github.com/example/platform.git", Path: "clusters/prod"},
		},
	}
	conflict := testApplicationSet(gitOpsTargetApplicationSetName(*target), defaultArgoNamespace, map[string]string{"app.kubernetes.io/managed-by": "other"})
	c := newValidationClient(t, target, conflict)
	r := &GitOpsTargetReconciler{Client: c, Log: slog.Default()}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "core-platform", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("Reconcile target: %v", err)
	}
	var after GitOpsTarget
	if err := c.Get(context.Background(), types.NamespacedName{Name: "core-platform", Namespace: "astronomer-mgmt"}, &after); err != nil {
		t.Fatalf("Get target: %v", err)
	}
	if after.Status.Phase != "Degraded" {
		t.Fatalf("expected Degraded status after ownership conflict, got %+v", after.Status)
	}
	var appSet unstructured.Unstructured
	appSet.SetGroupVersionKind(applicationSetGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: conflict.GetName(), Namespace: conflict.GetNamespace()}, &appSet); err != nil {
		t.Fatalf("Get conflicting ApplicationSet: %v", err)
	}
	if appSet.GetLabels()["app.kubernetes.io/managed-by"] != "other" {
		t.Fatalf("conflicting ApplicationSet was overwritten: %+v", appSet.GetLabels())
	}
}

func testApplicationSet(name, namespace string, labels map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": applicationSetGVK.GroupVersion().String(),
			"kind":       applicationSetGVK.Kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
		},
	}
	obj.SetGroupVersionKind(applicationSetGVK)
	obj.SetLabels(labels)
	return obj
}

func testApplication(name, namespace string, labels map[string]string, syncStatus, health string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": applicationGVK.GroupVersion().String(),
			"kind":       applicationGVK.Kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"status": map[string]any{
				"sync": map[string]any{
					"status":   syncStatus,
					"revision": "abc123",
				},
				"health": map[string]any{
					"status": health,
				},
				"operationState": map[string]any{
					"phase":   "Running",
					"message": "reconciling",
				},
				"resources": []any{
					map[string]any{
						"group":     "apps",
						"kind":      "Deployment",
						"namespace": "default",
						"name":      name,
						"status":    syncStatus,
						"health":    health,
					},
				},
			},
		},
	}
	obj.SetGroupVersionKind(applicationGVK)
	obj.SetLabels(labels)
	return obj
}

func testGitOpsTemplateConfigMap(name, namespace, templateJSON string, labels map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": configMapGVK.GroupVersion().String(),
			"kind":       configMapGVK.Kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"data": map[string]any{
				gitOpsTemplateDataKey: templateJSON,
			},
		},
	}
	obj.SetGroupVersionKind(configMapGVK)
	obj.SetLabels(labels)
	return obj
}

func testConfigMapData(name, namespace string, data map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": configMapGVK.GroupVersion().String(),
			"kind":       configMapGVK.Kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"data": stringMapToAny(data),
		},
	}
	obj.SetGroupVersionKind(configMapGVK)
	return obj
}

func hasFinalizer(finalizers []string, want string) bool {
	for _, finalizer := range finalizers {
		if finalizer == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func conditionStatus(conditions []metav1.Condition, conditionType string) metav1.ConditionStatus {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status
		}
	}
	return metav1.ConditionUnknown
}

func conditionReason(conditions []metav1.Condition, conditionType string) string {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Reason
		}
	}
	return ""
}
