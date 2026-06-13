package crd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ClusterSync is the narrow handler-facing interface the cluster reconciler
// depends on. Defined here (not in handler) so the controller stays trivially
// fakeable and so handler does not pick up a transitive dependency on
// controller-runtime.
//
// The server-side wiring (internal/server/crd_wiring.go) ships an adapter that
// translates between this shape and the existing ClusterQuerier /
// ProjectQuerier surfaces — the handlers themselves do not need to change.
type ClusterSync interface {
	// EnsureFromCRD upserts a cluster row to match the supplied spec and
	// returns the DB-side status the controller should reflect back onto the
	// CR's .status subresource. The implementation is expected to be
	// idempotent: calling it twice with the same spec is a no-op.
	EnsureFromCRD(ctx context.Context, spec ClusterSpec) (ClusterStatus, error)

	// DeleteByName starts the decommission flow for the named cluster.
	// Returns nil when the cluster is gone from the DB (the controller can
	// then drop the finalizer); returns an error when the work is still in
	// progress so the controller requeues without removing the finalizer.
	//
	// Implementations may also return a non-nil "in progress" sentinel —
	// see ErrInProgress — to distinguish a transient retry from a permanent
	// failure that should bubble to the user.
	DeleteByName(ctx context.Context, name string) error
}

// ProjectSync is the project-side equivalent of ClusterSync.
type ProjectSync interface {
	// EnsureFromCRD upserts a project row from the spec. The spec's first
	// Clusters[] entry is resolved to a cluster.id by the implementation;
	// the resolved ID is reflected back on the returned status.
	EnsureFromCRD(ctx context.Context, spec ProjectSpec) (ProjectStatus, error)

	// DeleteByName drops the named project. Project deletion is synchronous
	// today (no decommission CR), so a nil return means the row is gone.
	DeleteByName(ctx context.Context, name string) error
}

// ObjectRef identifies the Kubernetes object currently driving a DB row.
type ObjectRef struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	Generation int64
}

// ClusterOwnershipSync is an optional extension implemented by production
// sync adapters that can stamp DB ownership metadata after a successful sync.
type ClusterOwnershipSync interface {
	ValidateClusterOwnership(ctx context.Context, spec ClusterSpec, ref ObjectRef) error
	RecordClusterOwnership(ctx context.Context, spec ClusterSpec, ref ObjectRef) error
}

// ProjectOwnershipSync is the project-side ownership metadata hook.
type ProjectOwnershipSync interface {
	ValidateProjectOwnership(ctx context.Context, spec ProjectSpec, ref ObjectRef) error
	RecordProjectOwnership(ctx context.Context, spec ProjectSpec, ref ObjectRef) error
}

// ErrInProgress is the sentinel a Sync implementation can return when the
// delete work has been enqueued but is not yet complete. The controller
// requeues without removing the finalizer when it sees this error.
var ErrInProgress = errors.New("crd: delete in progress")

// ControllerConfig bundles the dependencies the manager needs.
//
// Lifecycle: the manager runs until ctx is cancelled (passed to
// manager.Start). On cancellation it waits for in-flight reconciles to drain
// before returning.
type ControllerConfig struct {
	// K8sConfig is the rest.Config used to build the manager's client. Always
	// produced from rest.InClusterConfig at server boot — the controller
	// expects to run inside a cluster.
	K8sConfig *rest.Config

	// WatchNamespace scopes the cache to a single namespace. Empty string
	// means cluster-scope watch (not recommended; the chart sets the value
	// to the management namespace).
	WatchNamespace string

	// ClusterHandler is the cluster-side sync entrypoint.
	ClusterHandler ClusterSync

	// ProjectHandler is the project-side sync entrypoint.
	ProjectHandler ProjectSync

	// Log is the slog logger used for controller-emitted lifecycle messages.
	// Required (nil-safe: defaults to slog.Default at New time).
	Log *slog.Logger

	// PollPeriod is the RequeueAfter cadence used for steady-state status
	// reconciles, i.e. the rate at which a Cluster/Project CR is re-examined
	// to pick up DB-side drift. Defaults to 60s when zero — see the spec's
	// "polling reconcile every 60s" guidance.
	PollPeriod time.Duration

	// LeaderElection enables controller-runtime lease coordination. Enable this
	// whenever the controller runs embedded in a Deployment with more than one
	// server replica, otherwise every server pod reconciles the same CRs.
	LeaderElection bool

	// LeaderElectionNamespace is where the Lease object is stored. Defaults to
	// WatchNamespace when empty. Required by controller-runtime for in-cluster
	// workloads that cannot infer a namespace.
	LeaderElectionNamespace string
}

// defaultPollPeriod is the fallback for ControllerConfig.PollPeriod.
const defaultPollPeriod = 60 * time.Second

// New constructs a controller-runtime Manager wired with both reconcilers.
// The caller starts it via manager.Start(ctx); cancelling ctx drains in-flight
// reconciles and returns from Start.
func New(cfg ControllerConfig) (manager.Manager, error) {
	if cfg.K8sConfig == nil {
		return nil, errors.New("crd.New: K8sConfig is required")
	}
	if cfg.ClusterHandler == nil {
		return nil, errors.New("crd.New: ClusterHandler is required")
	}
	if cfg.ProjectHandler == nil {
		return nil, errors.New("crd.New: ProjectHandler is required")
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	poll := cfg.PollPeriod
	if poll <= 0 {
		poll = defaultPollPeriod
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("crd.New: register core kinds: %w", err)
	}
	if err := AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("crd.New: register CRD kinds: %w", err)
	}

	mgrOpts := manager.Options{
		Scheme: scheme,
		// We don't run the metrics endpoint inside the server pod — the
		// existing /metrics on the server already exposes prometheus via the
		// observability stack, and binding a second :8080 would conflict.
		// BindAddress "0" tells controller-runtime to skip starting the
		// metrics listener entirely.
		Metrics: metricsserver.Options{BindAddress: "0"},
		// Health-probe HTTP server is also unused: the server pod has its
		// own /health endpoint handled by chi. Empty BindAddress = disabled.
		HealthProbeBindAddress: "0",
		LeaderElection:         cfg.LeaderElection,
	}
	if cfg.LeaderElection {
		leaderNamespace := cfg.LeaderElectionNamespace
		if leaderNamespace == "" {
			leaderNamespace = cfg.WatchNamespace
		}
		mgrOpts.LeaderElectionNamespace = leaderNamespace
		mgrOpts.LeaderElectionID = "astronomer-management-crd-controller"
		mgrOpts.LeaderElectionResourceLock = "leases"
	}
	if ns := cfg.WatchNamespace; ns != "" {
		mgrOpts.Cache = cache.Options{DefaultNamespaces: map[string]cache.Config{ns: {}}}
	}

	mgr, err := ctrl.NewManager(cfg.K8sConfig, mgrOpts)
	if err != nil {
		return nil, fmt.Errorf("crd.New: build manager: %w", err)
	}

	cr := &ClusterReconciler{
		Client: mgr.GetClient(),
		Sync:   cfg.ClusterHandler,
		Log:    log.With("controller", "cluster"),
		Poll:   poll,
	}
	if err := cr.SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("crd.New: cluster reconciler: %w", err)
	}

	pr := &ProjectReconciler{
		Client: mgr.GetClient(),
		Sync:   cfg.ProjectHandler,
		Log:    log.With("controller", "project"),
		Poll:   poll,
	}
	if err := pr.SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("crd.New: project reconciler: %w", err)
	}

	return mgr, nil
}

// ClusterReconciler watches Cluster CRs and reflects their spec into the DB.
//
// Exported for the test package — the tests construct a Reconciler directly
// against a fake client rather than spinning a full manager.
type ClusterReconciler struct {
	Client client.Client
	Sync   ClusterSync
	Log    *slog.Logger
	// Poll is the steady-state RequeueAfter; zero falls back to defaultPollPeriod.
	Poll time.Duration
}

// SetupWithManager registers the reconciler with the manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&Cluster{}).
		Complete(r)
}

// Reconcile applies the CRD spec to the DB and writes status back.
//
// Steps:
//  1. Get the Cluster CR. On NotFound (CR already gone) return without error
//     — the finalizer path drops the finalizer on delete, so by the time we
//     see a NotFound the row is also gone.
//  2. If DeletionTimestamp is non-zero, run the decommission path. The
//     finalizer comes off only when the Sync.DeleteByName completes.
//  3. Otherwise install the finalizer (idempotent) and call Sync.EnsureFromCRD.
//  4. Patch .status with the DB-side outcome.
//  5. Requeue at the poll period to refresh status against DB-side drift.
func (r *ClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.Log.With("name", req.Name, "namespace", req.Namespace)
	poll := r.Poll
	if poll <= 0 {
		poll = defaultPollPeriod
	}

	var obj Cluster
	if err := r.Client.Get(ctx, req.NamespacedName, &obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Deletion path.
	if !obj.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&obj, FinalizerCluster) {
			// Finalizer already removed — nothing to do.
			return reconcile.Result{}, nil
		}
		name := obj.Spec.Name
		if name == "" {
			name = obj.ObjectMeta.Name
		}
		log.Info("crd_cluster_delete_starting", "cluster_name", name)
		if err := r.Sync.DeleteByName(ctx, name); err != nil {
			if errors.Is(err, ErrInProgress) {
				return reconcile.Result{RequeueAfter: poll}, nil
			}
			log.Warn("crd_cluster_delete_failed", "error", err)
			return reconcile.Result{}, err
		}
		// DB row is gone; drop the finalizer so the API server can complete
		// object deletion.
		patch := client.MergeFrom(obj.DeepCopy())
		controllerutil.RemoveFinalizer(&obj, FinalizerCluster)
		if err := r.Client.Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("remove finalizer: %w", err)
		}
		return reconcile.Result{}, nil
	}

	// Steady-state path: install finalizer + sync.
	if !controllerutil.ContainsFinalizer(&obj, FinalizerCluster) {
		patch := client.MergeFrom(obj.DeepCopy())
		controllerutil.AddFinalizer(&obj, FinalizerCluster)
		if err := r.Client.Patch(ctx, &obj, patch); err != nil {
			return reconcile.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
	}

	ref := ObjectRef{
		APIVersion: GroupVersion.String(),
		Kind:       "Cluster",
		Namespace:  obj.Namespace,
		Name:       obj.Name,
		Generation: obj.Generation,
	}
	if ownershipSync, ok := r.Sync.(ClusterOwnershipSync); ok {
		if err := ownershipSync.ValidateClusterOwnership(ctx, obj.Spec, ref); err != nil {
			log.Warn("crd_cluster_ownership_invalid", "error", err)
			return reconcile.Result{}, err
		}
	}
	status, err := r.Sync.EnsureFromCRD(ctx, obj.Spec)
	if err != nil {
		log.Warn("crd_cluster_sync_failed", "error", err)
		// Don't burn the queue on hard sync failures — exponential backoff.
		return reconcile.Result{}, err
	}
	if ownershipSync, ok := r.Sync.(ClusterOwnershipSync); ok {
		if err := ownershipSync.RecordClusterOwnership(ctx, obj.Spec, ref); err != nil {
			log.Warn("crd_cluster_ownership_failed", "error", err)
			return reconcile.Result{}, err
		}
	}
	if status.LastReconciled.IsZero() {
		status.LastReconciled = metav1.Time{Time: time.Now().UTC()}
	}

	// Echo spec.projectRefs onto status.observedProjectRefs so kubectl describe
	// shows what the controller picked up; the spec stays authoritative.
	if len(obj.Spec.ProjectRefs) > 0 {
		status.ObservedProjectRefs = append([]string(nil), obj.Spec.ProjectRefs...)
	}

	// Patch status if it actually changed. Avoids hot-looping the API server
	// on every poll when the DB state is steady.
	if !reflect.DeepEqual(obj.Status, status) {
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Status = status
		if err := r.Client.Status().Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("patch status: %w", err)
		}
	}
	return reconcile.Result{RequeueAfter: poll}, nil
}

// ProjectReconciler is the project-side equivalent of ClusterReconciler.
type ProjectReconciler struct {
	Client client.Client
	Sync   ProjectSync
	Log    *slog.Logger
	Poll   time.Duration
}

// SetupWithManager registers the reconciler with the manager.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&Project{}).
		Complete(r)
}

// Reconcile mirrors the Cluster reconciler shape against the Project CR.
func (r *ProjectReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.Log.With("name", req.Name, "namespace", req.Namespace)
	poll := r.Poll
	if poll <= 0 {
		poll = defaultPollPeriod
	}

	var obj Project
	if err := r.Client.Get(ctx, req.NamespacedName, &obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if !obj.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&obj, FinalizerProject) {
			return reconcile.Result{}, nil
		}
		name := obj.Spec.Name
		if name == "" {
			name = obj.ObjectMeta.Name
		}
		log.Info("crd_project_delete_starting", "project_name", name)
		if err := r.Sync.DeleteByName(ctx, name); err != nil {
			if errors.Is(err, ErrInProgress) {
				return reconcile.Result{RequeueAfter: poll}, nil
			}
			log.Warn("crd_project_delete_failed", "error", err)
			return reconcile.Result{}, err
		}
		patch := client.MergeFrom(obj.DeepCopy())
		controllerutil.RemoveFinalizer(&obj, FinalizerProject)
		if err := r.Client.Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("remove finalizer: %w", err)
		}
		return reconcile.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&obj, FinalizerProject) {
		patch := client.MergeFrom(obj.DeepCopy())
		controllerutil.AddFinalizer(&obj, FinalizerProject)
		if err := r.Client.Patch(ctx, &obj, patch); err != nil {
			return reconcile.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
	}

	ref := ObjectRef{
		APIVersion: GroupVersion.String(),
		Kind:       "Project",
		Namespace:  obj.Namespace,
		Name:       obj.Name,
		Generation: obj.Generation,
	}
	if ownershipSync, ok := r.Sync.(ProjectOwnershipSync); ok {
		if err := ownershipSync.ValidateProjectOwnership(ctx, obj.Spec, ref); err != nil {
			log.Warn("crd_project_ownership_invalid", "error", err)
			return reconcile.Result{}, err
		}
	}
	status, err := r.Sync.EnsureFromCRD(ctx, obj.Spec)
	if err != nil {
		log.Warn("crd_project_sync_failed", "error", err)
		return reconcile.Result{}, err
	}
	if ownershipSync, ok := r.Sync.(ProjectOwnershipSync); ok {
		if err := ownershipSync.RecordProjectOwnership(ctx, obj.Spec, ref); err != nil {
			log.Warn("crd_project_ownership_failed", "error", err)
			return reconcile.Result{}, err
		}
	}
	if status.LastReconciled.IsZero() {
		status.LastReconciled = metav1.Time{Time: time.Now().UTC()}
	}
	if len(obj.Spec.Clusters) > 0 {
		status.ObservedClusters = append([]string(nil), obj.Spec.Clusters...)
	}

	if !reflect.DeepEqual(obj.Status, status) {
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Status = status
		if err := r.Client.Status().Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("patch status: %w", err)
		}
	}
	return reconcile.Result{RequeueAfter: poll}, nil
}

// NamespacedNameFromMeta is a small helper for callers building a
// reconcile.Request from a *metav1.ObjectMeta — saves importing types in the
// integration test glue.
func NamespacedNameFromMeta(meta metav1.Object) types.NamespacedName {
	return types.NamespacedName{Name: meta.GetName(), Namespace: meta.GetNamespace()}
}
