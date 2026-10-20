package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type fakeClusterSync struct {
	ensureErr error
	deleteErr error
	ensured   []ClusterSpec
	deleted   []string
}

func (f *fakeClusterSync) EnsureFromCRD(_ context.Context, spec ClusterSpec) (ClusterStatus, error) {
	f.ensured = append(f.ensured, spec)
	if f.ensureErr != nil {
		return ClusterStatus{}, f.ensureErr
	}
	return ClusterStatus{ClusterID: "c-1", Phase: "active"}, nil
}

func (f *fakeClusterSync) DeleteByName(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	return f.deleteErr
}

func newFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&Cluster{}, &Project{}, &AgentProfile{}).Build()
}

var clusterKey = types.NamespacedName{Namespace: "mgmt", Name: "edge-1"}

func TestClusterReconcilerHappyPathPublishesReady(t *testing.T) {
	t.Parallel()
	c := newFakeClient(t, &Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "mgmt", Name: "edge-1", Generation: 3}})
	sync := &fakeClusterSync{}
	r, err := NewClusterReconciler(c, sync, Options{Poll: 7 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: clusterKey})
	if err != nil || res.RequeueAfter != 7*time.Second {
		t.Fatalf("result = %+v, %v", res, err)
	}
	var got Cluster
	if err := c.Get(context.Background(), clusterKey, &got); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&got, FinalizerCluster) {
		t.Fatal("finalizer not added")
	}
	if got.Status.ClusterID != "c-1" || len(got.Status.Conditions) != 1 || got.Status.Conditions[0].Status != metav1.ConditionTrue || got.Status.Conditions[0].ObservedGeneration != 3 {
		t.Fatalf("status = %+v", got.Status)
	}
	if len(sync.ensured) != 1 || sync.ensured[0].Name != "edge-1" {
		t.Fatalf("ensured = %+v", sync.ensured)
	}
}

func TestClusterReconcilerSyncFailureRecordsReasonAndRequeues(t *testing.T) {
	t.Parallel()
	c := newFakeClient(t, &Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "mgmt", Name: "edge-1"}})
	r, err := NewClusterReconciler(c, &fakeClusterSync{ensureErr: errors.New("db down")}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: clusterKey}); err == nil {
		t.Fatal("expected the sync error to be returned for requeue")
	}
	var got Cluster
	if err := c.Get(context.Background(), clusterKey, &got); err != nil {
		t.Fatal(err)
	}
	cond := got.Status.Conditions
	if len(cond) != 1 || cond[0].Reason != "SyncFailed" || cond[0].Message != "db down" || cond[0].Status != metav1.ConditionFalse {
		t.Fatalf("conditions = %+v", cond)
	}
}

func TestClusterReconcilerInvalidProfileRefIsInvalidAgentProfile(t *testing.T) {
	t.Parallel()
	c := newFakeClient(t, &Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "mgmt", Name: "edge-1"}, Spec: ClusterSpec{Agent: ClusterAgentSpec{ProfileRef: "other/p"}}})
	sync := &fakeClusterSync{}
	r, err := NewClusterReconciler(c, sync, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: clusterKey}); err == nil {
		t.Fatal("expected error")
	}
	var got Cluster
	_ = c.Get(context.Background(), clusterKey, &got)
	if len(got.Status.Conditions) != 1 || got.Status.Conditions[0].Reason != "InvalidAgentProfile" {
		t.Fatalf("conditions = %+v", got.Status.Conditions)
	}
	if len(sync.ensured) != 0 {
		t.Fatal("sync ran after resolve-spec failed")
	}
}

func TestClusterReconcilerDeleteInProgressPolls(t *testing.T) {
	t.Parallel()
	now := metav1.Now()
	c := newFakeClient(t, &Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "mgmt", Name: "edge-1", DeletionTimestamp: &now, Finalizers: []string{FinalizerCluster}}})
	sync := &fakeClusterSync{deleteErr: ErrInProgress}
	r, err := NewClusterReconciler(c, sync, Options{Poll: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: clusterKey})
	if err != nil || res.RequeueAfter != 3*time.Second {
		t.Fatalf("result = %+v, %v", res, err)
	}
	sync.deleteErr = nil
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: clusterKey}); err != nil {
		t.Fatal(err)
	}
	var got Cluster
	if err := c.Get(context.Background(), clusterKey, &got); err == nil {
		t.Fatalf("cluster still present after finalizer removal: %+v", got.Finalizers)
	}
	if len(sync.deleted) != 2 || len(sync.ensured) != 0 {
		t.Fatalf("deleted=%v ensured=%v", sync.deleted, sync.ensured)
	}
}

func TestClusterReconcilerMissingObjectIsNoop(t *testing.T) {
	t.Parallel()
	r, err := NewClusterReconciler(newFakeClient(t), &fakeClusterSync{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: clusterKey}); err != nil || res != (reconcile.Result{}) {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestAgentProfileReconcilerPublishesValidation(t *testing.T) {
	t.Parallel()
	key := types.NamespacedName{Namespace: "mgmt", Name: "p"}
	c := newFakeClient(t, &AgentProfile{ObjectMeta: metav1.ObjectMeta{Namespace: "mgmt", Name: "p"}, Spec: AgentProfileSpec{PrivilegeProfile: "namespace-viewer"}})
	r, err := NewAgentProfileReconciler(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	var got AgentProfile
	if err := c.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != "Invalid" || !controllerutil.ContainsFinalizer(&got, FinalizerAgentProfile) {
		t.Fatalf("status = %+v finalizers=%v", got.Status, got.Finalizers)
	}
}
